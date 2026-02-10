package sessions

import (
	"context"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/domain"
)

const (
	// DefaultConfirmationInterval is how often to check for pending confirmations
	DefaultConfirmationInterval = 10 * time.Second

	// VerificationTimeout is the timeout for each verification attempt
	VerificationTimeout = 30 * time.Second
)

// ConfirmationWorker processes pending rental confirmations by verifying blockchain transactions
// and transitioning sessions to RUNNING state when confirmed.
// V4: Uses a single K8s JobExecutor — no Docker/remote branching.
type ConfirmationWorker struct {
	sessionRepo  domain.RentalSessionRepository
	verifier     *blockchain.TransactionVerifier
	manager      *SessionManager
	nodeRepo     domain.NodeRepository
	executor     domain.JobExecutor // K8s executor (single path)
	interval     time.Duration
	logger       *slog.Logger
	defaultImage string
}

// NewConfirmationWorker creates a new background worker for processing pending confirmations.
func NewConfirmationWorker(
	sessionRepo domain.RentalSessionRepository,
	verifier *blockchain.TransactionVerifier,
	manager *SessionManager,
	nodeRepo domain.NodeRepository,
	executor domain.JobExecutor,
	logger *slog.Logger,
) *ConfirmationWorker {
	return &ConfirmationWorker{
		sessionRepo:  sessionRepo,
		verifier:     verifier,
		manager:      manager,
		nodeRepo:     nodeRepo,
		executor:     executor,
		interval:     DefaultConfirmationInterval,
		logger:       logger,
		defaultImage: "nvidia/cuda:12.0.0-devel-ubuntu22.04",
	}
}

// WithInterval sets a custom check interval (useful for testing)
func (w *ConfirmationWorker) WithInterval(interval time.Duration) *ConfirmationWorker {
	w.interval = interval
	return w
}

// WithDefaultImage sets the default container image
func (w *ConfirmationWorker) WithDefaultImage(image string) *ConfirmationWorker {
	if image != "" {
		w.defaultImage = image
	}
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
	if session.TxHash == nil {
		return
	}

	w.logger.Info("verifying transaction",
		"sessionId", session.ID,
		"txHash", *session.TxHash,
	)

	verifyCtx, cancel := context.WithTimeout(ctx, VerificationTimeout)
	defer cancel()

	txHash := common.HexToHash(*session.TxHash)
	userAddr := common.HexToAddress(session.UserAddress)
	providerAddr := common.HexToAddress(session.ProviderAddress)

	result, err := w.verifier.VerifyTransaction(verifyCtx, txHash, userAddr, providerAddr)
	if err != nil {
		w.logger.Error("verification error",
			"sessionId", session.ID,
			"error", err,
		)
		return
	}

	switch result.Status {
	case blockchain.StatusPending:
		w.logger.Debug("transaction still pending",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
		)

	case blockchain.StatusFailed:
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
		w.logger.Info("transaction confirmed",
			"sessionId", session.ID,
			"txHash", *session.TxHash,
			"rentalId", result.RentalID,
			"blockNumber", result.BlockNumber,
		)
		w.handleConfirmedTransaction(ctx, session, result)
	}
}

// handleConfirmedTransaction provisions a K8s Pod and transitions the session to RUNNING
func (w *ConfirmationWorker) handleConfirmedTransaction(
	ctx context.Context,
	session *domain.RentalSession,
	result blockchain.VerificationResult,
) {
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

	containerImage := session.DockerImage
	if containerImage == "" {
		containerImage = w.defaultImage
	}

	if w.executor == nil {
		w.logger.Error("no executor configured", "sessionId", session.ID)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "no executor configured"); err != nil {
			w.logger.Error("failed to transition to FAILED", "error", err)
		}
		return
	}

	// Determine GPU count — CPU-only nodes cannot serve GPU rentals
	gpuCount := session.GPUCount
	if gpuCount <= 0 {
		gpuCount = 1
	}
	if node.TotalGPUs <= 0 {
		// Node has no GPUs — cap to 0 (CPU-only workload)
		gpuCount = 0
	} else if gpuCount > node.TotalGPUs {
		// Don't request more GPUs than the node has
		gpuCount = node.TotalGPUs
	}

	// Use session resource specs if available, otherwise defaults
	cpuCores := session.CPUCores
	if cpuCores <= 0 {
		cpuCores = 4
	}
	memoryGB := session.MemoryGB
	if memoryGB <= 0 {
		memoryGB = 16
	}
	storageGB := session.StorageGB
	if storageGB <= 0 {
		storageGB = 50
	}

	// Cap CPU/Memory to node capacity (same pattern as GPU cap above)
	if node.TotalCPUCores > 0 && cpuCores > node.TotalCPUCores {
		cpuCores = node.TotalCPUCores
	}
	if node.TotalMemoryGB > 0 && memoryGB > node.TotalMemoryGB {
		memoryGB = node.TotalMemoryGB
	}

	spec := domain.JobSpec{
		SessionID:   session.ID,
		UserAddress: session.UserAddress,
		ProviderID:  node.ProviderID,
		NodeID:      session.NodeID,
		GPUCount:    gpuCount,
		GPUModel:    node.GPUType,
		Image:       containerImage,
		CPUCores:    cpuCores,
		MemoryGB:    memoryGB,
		StorageGB:   storageGB,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}

	password, err := w.executor.CreateGPUSession(ctx, spec)
	if err != nil {
		w.logger.Error("failed to create GPU session",
			"sessionId", session.ID,
			"image", containerImage,
			"error", err,
		)
		if err := w.manager.TransitionToFailed(ctx, session.ID, "failed to create GPU session: "+err.Error()); err != nil {
			w.logger.Error("failed to transition to FAILED", "error", err)
		}
		return
	}

	w.logger.Info("created GPU session via K8s executor",
		"sessionId", session.ID,
		"image", containerImage,
	)

	// Store SSH password in DB for reliable retrieval (even if K8s Secret is lost)
	if password != "" {
		if err := w.sessionRepo.UpdateSSHInfo(ctx, session.ID, "", 0, "root", password); err != nil {
			w.logger.Warn("failed to store SSH password",
				"sessionId", session.ID,
				"error", err,
			)
		}
	}

	if err := w.manager.TransitionToRunning(
		ctx,
		session.ID,
		result.RentalID,
		result.BlockNumber,
		*session.TxHash,
		result.StartTime,
	); err != nil {
		w.logger.Error("failed to transition to RUNNING, cleaning up Pod",
			"sessionId", session.ID,
			"error", err,
		)
		// Fix 5: Clean up Pod on transition failure
		if w.executor != nil {
			_ = w.executor.DeleteGPUSession(ctx, session)
		}
		return
	}

	w.logger.Info("session transitioned to RUNNING",
		"sessionId", session.ID,
		"rentalId", result.RentalID,
	)
}

// ProcessOnce runs a single confirmation check (useful for testing)
func (w *ConfirmationWorker) ProcessOnce(ctx context.Context) {
	w.processPendingConfirmations(ctx)
}
