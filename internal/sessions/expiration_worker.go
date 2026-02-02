package sessions

import (
	"context"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
	"github.com/worldland/worldland-hub/internal/rental"
)

const (
	// DefaultExpirationInterval is how often to check for expired sessions
	// CONTEXT.md specifies 1 minute check interval
	DefaultExpirationInterval = 1 * time.Minute

	// ExpirationBatchLimit prevents long-running queries
	ExpirationBatchLimit = 100
)

// ExpirationWorker monitors extended sessions and auto-terminates them at expiration
type ExpirationWorker struct {
	sessionRepo domain.RentalSessionRepository
	manager     *SessionManager
	nodeClient  rental.NodeClientInterface
	nodeRepo    domain.NodeRepository
	interval    time.Duration
	logger      *slog.Logger
	// NEW: K8s integration
	jobManager *k8s.JobManager
}

// NewExpirationWorker creates a new background worker for auto-expiring sessions
func NewExpirationWorker(
	sessionRepo domain.RentalSessionRepository,
	manager *SessionManager,
	nodeClient rental.NodeClientInterface,
	nodeRepo domain.NodeRepository,
	logger *slog.Logger,
) *ExpirationWorker {
	return &ExpirationWorker{
		sessionRepo: sessionRepo,
		manager:     manager,
		nodeClient:  nodeClient,
		nodeRepo:    nodeRepo,
		interval:    DefaultExpirationInterval,
		logger:      logger,
	}
}

// WithInterval sets a custom check interval (useful for testing)
func (w *ExpirationWorker) WithInterval(interval time.Duration) *ExpirationWorker {
	w.interval = interval
	return w
}

// WithK8s configures K8s integration for Pod deletion on expiration
func (w *ExpirationWorker) WithK8s(jobManager *k8s.JobManager) *ExpirationWorker {
	w.jobManager = jobManager
	return w
}

// Start begins the background expiration processing loop
func (w *ExpirationWorker) Start(ctx context.Context) error {
	w.logger.Info("expiration worker started",
		"interval", w.interval,
	)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.processExpiredSessions(ctx)
		case <-ctx.Done():
			w.logger.Info("expiration worker stopped")
			return ctx.Err()
		}
	}
}

// processExpiredSessions finds and terminates expired sessions
func (w *ExpirationWorker) processExpiredSessions(ctx context.Context) {
	// Find sessions that have passed their extended_until time
	cutoff := time.Now()
	sessions, err := w.sessionRepo.FindExpiringSessions(ctx, cutoff)
	if err != nil {
		w.logger.Error("failed to find expiring sessions", "error", err)
		return
	}

	if len(sessions) == 0 {
		return
	}

	w.logger.Info("processing expired sessions", "count", len(sessions))

	for _, session := range sessions {
		select {
		case <-ctx.Done():
			return
		default:
			w.expireSession(ctx, session)
		}
	}
}

// expireSession terminates a single expired session
func (w *ExpirationWorker) expireSession(ctx context.Context, session *domain.RentalSession) {
	w.logger.Info("expiring session",
		"sessionId", session.ID,
		"extendedUntil", session.ExtendedUntil,
	)

	// NEW: Delete K8s Pod first (idempotent)
	if w.jobManager != nil {
		if err := w.jobManager.DeleteGPUSession(ctx, session.UserAddress, session.ID); err != nil {
			w.logger.Error("failed to delete K8s pod for expired session",
				"sessionId", session.ID,
				"error", err,
			)
			// Continue - Pod might not exist or already deleted
		} else {
			w.logger.Info("deleted K8s pod for expired session",
				"sessionId", session.ID,
			)
		}
	}

	// Lookup node
	node, err := w.nodeRepo.GetByID(ctx, session.NodeID)
	if err != nil {
		w.logger.Error("failed to lookup node for expiration",
			"sessionId", session.ID,
			"nodeId", session.NodeID,
			"error", err,
		)
		// Still try to transition state
		if err := w.manager.TransitionToStopped(ctx, session.ID, time.Now(), "0", "", 0); err != nil {
			w.logger.Error("failed to transition expired session to STOPPED", "error", err)
		}
		return
	}

	// Call node to stop container (best effort)
	if node.APIEndpoint != "" {
		stopReq := rental.StopRentalRequest{
			SessionID: session.ID,
		}
		if _, err := w.nodeClient.StopRental(ctx, node.APIEndpoint, stopReq); err != nil {
			w.logger.Error("failed to stop container on node (will still mark STOPPED)",
				"sessionId", session.ID,
				"nodeId", session.NodeID,
				"error", err,
			)
			// Continue - database is source of truth
		}
	}

	// Transition to STOPPED
	// Note: In production, this would involve blockchain stopRental call
	// For now, we just update database state - blockchain settlement happens separately
	if err := w.manager.TransitionToStopped(ctx, session.ID, time.Now(), "0", "", 0); err != nil {
		w.logger.Error("failed to transition expired session to STOPPED",
			"sessionId", session.ID,
			"error", err,
		)
		return
	}

	w.logger.Info("session expired and stopped",
		"sessionId", session.ID,
	)
}

// ProcessOnce runs a single expiration check (useful for testing)
func (w *ExpirationWorker) ProcessOnce(ctx context.Context) {
	w.processExpiredSessions(ctx)
}
