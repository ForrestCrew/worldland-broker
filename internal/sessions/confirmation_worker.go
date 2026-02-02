package sessions

import (
	"context"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
	"github.com/worldland/worldland-hub/internal/rental"
)

const (
	// DefaultConfirmationInterval is how often to check for pending confirmations
	DefaultConfirmationInterval = 10 * time.Second

	// VerificationTimeout is the timeout for each verification attempt
	VerificationTimeout = 30 * time.Second
)

// ConfirmationWorker processes pending rental confirmations by verifying blockchain transactions
// and transitioning sessions to RUNNING state when confirmed.
type ConfirmationWorker struct {
	sessionRepo domain.RentalSessionRepository
	verifier    *blockchain.TransactionVerifier
	manager     *SessionManager
	nodeClient  rental.NodeClientInterface
	nodeRepo    domain.NodeRepository
	interval    time.Duration
	logger      *slog.Logger
	// NEW: K8s integration
	jobManager   *k8s.JobManager
	tenantOrch   *k8s.TenantOrchestrator
	defaultImage string // Default container image for GPU sessions
}

// NewConfirmationWorker creates a new background worker for processing pending confirmations.
//
// Parameters:
//   - sessionRepo: Repository for rental session persistence
//   - verifier: TransactionVerifier for blockchain verification
//   - manager: SessionManager for state transitions
//   - nodeClient: Client for Hub-to-Node communication
//   - nodeRepo: Repository for node lookup
//   - logger: Structured logger
func NewConfirmationWorker(
	sessionRepo domain.RentalSessionRepository,
	verifier *blockchain.TransactionVerifier,
	manager *SessionManager,
	nodeClient rental.NodeClientInterface,
	nodeRepo domain.NodeRepository,
	logger *slog.Logger,
) *ConfirmationWorker {
	return &ConfirmationWorker{
		sessionRepo:  sessionRepo,
		verifier:     verifier,
		manager:      manager,
		nodeClient:   nodeClient,
		nodeRepo:     nodeRepo,
		interval:     DefaultConfirmationInterval,
		logger:       logger,
		defaultImage: "ubuntu:22.04", // Default image if K8s is not configured
	}
}

// WithK8s configures K8s integration for Pod creation on confirmation
func (w *ConfirmationWorker) WithK8s(jobManager *k8s.JobManager, tenantOrch *k8s.TenantOrchestrator, defaultImage string) *ConfirmationWorker {
	w.jobManager = jobManager
	w.tenantOrch = tenantOrch
	if defaultImage != "" {
		w.defaultImage = defaultImage
	}
	return w
}

// WithInterval sets a custom check interval (useful for testing)
func (w *ConfirmationWorker) WithInterval(interval time.Duration) *ConfirmationWorker {
	w.interval = interval
	return w
}

// Start begins the background confirmation processing loop.
// Returns when context is cancelled (returns ctx.Err())
func (w *ConfirmationWorker) Start(ctx context.Context) error {
	w.logger.Info("confirmation worker started",
		"interval", w.interval,
	)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.processPendingConfirmations(ctx)
		case <-ctx.Done():
			w.logger.Info("confirmation worker stopped")
			return ctx.Err()
		}
	}
}

// processPendingConfirmations processes all PENDING sessions that have a txHash set
func (w *ConfirmationWorker) processPendingConfirmations(ctx context.Context) {
	// Get all PENDING sessions with txHash
	sessions, err := w.sessionRepo.ListPendingWithTxHash(ctx)
	if err != nil {
		w.logger.Error("failed to list pending confirmations", "error", err)
		return
	}

	if len(sessions) == 0 {
		return
	}

	w.logger.Info("processing pending confirmations", "count", len(sessions))

	for _, session := range sessions {
		select {
		case <-ctx.Done():
			return
		default:
			w.processSession(ctx, session)
		}
	}
}

// processSession processes a single session for confirmation
func (w *ConfirmationWorker) processSession(ctx context.Context, session *domain.RentalSession) {
	// Skip if no txHash (shouldn't happen with ListPendingWithTxHash, but be safe)
	if session.TxHash == nil {
		return
	}

	w.logger.Info("verifying transaction",
		"sessionId", session.ID,
		"txHash", *session.TxHash,
	)

	// Create timeout context for verification
	verifyCtx, cancel := context.WithTimeout(ctx, VerificationTimeout)
	defer cancel()

	// Parse addresses for verification
	txHash := common.HexToHash(*session.TxHash)
	userAddr := common.HexToAddress(session.UserAddress)
	providerAddr := common.HexToAddress(session.ProviderAddress)

	// Verify transaction
	result, err := w.verifier.VerifyTransaction(verifyCtx, txHash, userAddr, providerAddr)
	if err != nil {
		w.logger.Error("verification error",
			"sessionId", session.ID,
			"error", err,
		)
		return // Don't fail session on RPC errors, will retry next tick
	}

	// Handle verification result
	switch result.Status {
	case blockchain.StatusPending:
		// Transaction still pending, skip (will retry next tick)
		w.logger.Debug("transaction still pending",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
		)

	case blockchain.StatusFailed:
		// Transaction reverted on-chain
		w.logger.Info("transaction failed",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
			"reason", result.Reason,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "transaction failed: "+result.Reason); err != nil {
			w.logger.Error("failed to transition to FAILED",
				"sessionId", session.ID,
				"error", err,
			)
		}

	case blockchain.StatusInvalid:
		// Transaction exists but doesn't match expected parameters
		w.logger.Info("transaction invalid",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
			"reason", result.Reason,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "invalid transaction: "+result.Reason); err != nil {
			w.logger.Error("failed to transition to FAILED",
				"sessionId", session.ID,
				"error", err,
			)
		}

	case blockchain.StatusConfirmed:
		// Transaction confirmed - start the rental
		w.logger.Info("transaction confirmed",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
			"rentalId", result.RentalID,
			"blockNumber", result.BlockNumber,
		)
		w.handleConfirmedTransaction(ctx, session, result)
	}
}

// handleConfirmedTransaction handles a confirmed blockchain transaction by provisioning
// the container on the node and transitioning the session to RUNNING
func (w *ConfirmationWorker) handleConfirmedTransaction(
	ctx context.Context,
	session *domain.RentalSession,
	result blockchain.VerificationResult,
) {
	// Lookup node for API endpoint
	node, err := w.nodeRepo.GetByID(ctx, session.NodeID)
	if err != nil {
		w.logger.Error("failed to lookup node",
			"sessionId", session.ID,
			"nodeId", session.NodeID,
			"error", err,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "node not found"); err != nil {
			w.logger.Error("failed to transition to FAILED", "error", err)
		}
		return
	}

	if node.APIEndpoint == "" {
		w.logger.Error("node has no API endpoint",
			"sessionId", session.ID,
			"nodeId", session.NodeID,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "node API endpoint not configured"); err != nil {
			w.logger.Error("failed to transition to FAILED", "error", err)
		}
		return
	}

	// NEW: Create K8s Pod (in addition to existing Node API call)
	if w.jobManager != nil {
		// Ensure tenant namespace exists
		if w.tenantOrch != nil {
			_, err := w.tenantOrch.EnsureTenant(ctx, session.UserAddress, 1)
			if err != nil {
				w.logger.Error("failed to ensure tenant namespace", "error", err)
				// Continue - Pod creation might still work if namespace exists
			}
		}

		// Determine GPU count (default to 1)
		gpuCount := 1

		spec := k8s.GPUJobSpec{
			SessionID:     session.ID,
			UserAddress:   session.UserAddress,
			ProviderID:    node.ProviderID,
			GPUCount:      gpuCount,
			GPUModel:      node.GPUType,
			Image:         w.defaultImage,
			CPURequest:    "4",
			MemoryRequest: "16Gi",
			CPULimit:      "8",
			MemoryLimit:   "32Gi",
			ExpiresAt:     time.Now().Add(24 * time.Hour),
		}

		password, err := w.jobManager.CreateGPUSession(ctx, spec)
		if err != nil {
			w.logger.Error("failed to create K8s pod", "sessionId", session.ID, "error", err)
			// Non-fatal: Node API is primary for now
		} else {
			w.logger.Info("created K8s pod for session",
				"sessionId", session.ID,
				"password", "[REDACTED]",
			)

			// TODO: Store SSH password in session for later API retrieval
			// Note: Check if session repo has UpdateSSHPassword method, if not, skip
			_ = password // password available for future use
		}
	}

	// Call node to start the rental container
	// Note: For automatic confirmation flow, we use a default SSH key placeholder
	// The actual SSH key should be provided through a separate mechanism or stored earlier
	nodeReq := rental.StartRentalRequest{
		SessionID:   session.ID,
		GPUDeviceID: node.GPUUUID,
		Image:       "nvidia/cuda:12.1-runtime-ubuntu22.04", // Default image
		MemoryBytes: 8 * 1024 * 1024 * 1024,                 // 8GB default
		CPUCount:    4,                                      // 4 CPUs default
		// Note: SSHPublicKey would come from session data or a separate user profile
		// For now, the node can generate a key pair if needed
	}

	nodeResp, err := w.nodeClient.StartRental(ctx, node.APIEndpoint, nodeReq)
	if err != nil {
		w.logger.Error("failed to start rental on node",
			"sessionId", session.ID,
			"nodeId", session.NodeID,
			"nodeURL", node.APIEndpoint,
			"error", err,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "failed to provision container: "+err.Error()); err != nil {
			w.logger.Error("failed to transition to FAILED", "error", err)
		}
		return
	}

	w.logger.Info("rental started on node",
		"sessionId", session.ID,
		"sshHost", nodeResp.SSHHost,
		"sshPort", nodeResp.SSHPort,
		"sshUser", nodeResp.SSHUser,
	)

	// Transition to RUNNING with blockchain data
	if err := w.manager.TransitionToRunning(
		ctx,
		session.ID,
		result.RentalID,
		result.BlockNumber,
		*session.TxHash,
		result.StartTime,
	); err != nil {
		w.logger.Error("failed to transition to RUNNING",
			"sessionId", session.ID,
			"error", err,
		)
		return
	}

	w.logger.Info("session transitioned to RUNNING",
		"sessionId", session.ID,
		"rentalId", result.RentalID,
	)

	// TODO: Store SSH credentials in session or cache for GetSession to return
	// This would require adding SSHCredentials to the session model or a separate cache
}

// ProcessOnce runs a single confirmation check (useful for testing)
func (w *ConfirmationWorker) ProcessOnce(ctx context.Context) {
	w.processPendingConfirmations(ctx)
}
