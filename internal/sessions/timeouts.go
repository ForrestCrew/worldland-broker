package sessions

import (
	"context"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

const (
	// PendingTimeout is how long a session can stay PENDING before soft deletion
	// PENDING sessions without tx_hash are soft-deleted after this duration (Phase 14 ADR-001)
	PendingTimeout = 10 * time.Minute

	// RunningTimeout is how long a RUNNING session can go without heartbeat before failing
	// RUNNING sessions with no node heartbeat are failed after this duration
	// Note: Currently there's no heartbeat mechanism, so this is effectively
	// the maximum session duration. Set to 24 hours as a reasonable default.
	// TODO: Implement proper heartbeat updates from pod watcher
	RunningTimeout = 24 * time.Hour

	// DefaultCheckInterval is how often to check for timeouts (Phase 14: updated to 1 minute)
	DefaultCheckInterval = 1 * time.Minute
)

// SessionFailer defines the interface for failing sessions (for testability)
type SessionFailer interface {
	TransitionToFailed(ctx context.Context, sessionID string, reason string) error
}

// SoftDeleter defines interface for soft-deleting expired sessions
// SoftDeletePendingBefore excludes sessions with tx_hash (confirmation in progress)
type SoftDeleter interface {
	SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// TimeoutEnforcer runs background timeout checks to fail stale sessions
type TimeoutEnforcer struct {
	failer        SessionFailer
	softDeleter   SoftDeleter // for soft delete operations on PENDING sessions
	repo          domain.RentalSessionRepository
	checkInterval time.Duration
	logger        *slog.Logger
}

// NewTimeoutEnforcer creates a new timeout enforcer
// failer: typically a *SessionManager that can transition sessions to FAILED state
// softDeleter: repository implementing SoftDeletePendingBefore (protects sessions with tx_hash)
// repo: repository to find stale sessions
// logger: structured logger for timeout events
func NewTimeoutEnforcer(
	failer SessionFailer,
	softDeleter SoftDeleter,
	repo domain.RentalSessionRepository,
	logger *slog.Logger,
) *TimeoutEnforcer {
	return &TimeoutEnforcer{
		failer:        failer,
		softDeleter:   softDeleter,
		repo:          repo,
		checkInterval: DefaultCheckInterval,
		logger:        logger,
	}
}

// WithCheckInterval sets a custom check interval (useful for testing)
func (t *TimeoutEnforcer) WithCheckInterval(interval time.Duration) *TimeoutEnforcer {
	t.checkInterval = interval
	return t
}

// Start begins the background timeout enforcement loop
// Returns when context is cancelled (returns ctx.Err())
func (t *TimeoutEnforcer) Start(ctx context.Context) error {
	t.logger.Info("timeout enforcer started",
		"pendingTimeout", PendingTimeout,
		"runningTimeout", RunningTimeout,
		"checkInterval", t.checkInterval,
	)

	ticker := time.NewTicker(t.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := t.checkTimeouts(ctx); err != nil {
				t.logger.Error("timeout check failed", "error", err)
			}
		case <-ctx.Done():
			t.logger.Info("timeout enforcer stopped")
			return ctx.Err()
		}
	}
}

// checkTimeouts checks for and enforces all timeout conditions
func (t *TimeoutEnforcer) checkTimeouts(ctx context.Context) error {
	// Check PENDING timeouts (sessions waiting for RentalStarted event)
	if err := t.enforcePendingTimeouts(ctx); err != nil {
		t.logger.Error("pending timeout enforcement failed", "error", err)
		// Continue to check running timeouts even if pending check failed
	}

	// Check RUNNING timeouts (sessions with no heartbeat)
	if err := t.enforceRunningTimeouts(ctx); err != nil {
		t.logger.Error("running timeout enforcement failed", "error", err)
	}

	return nil
}

// enforcePendingTimeouts soft-deletes PENDING sessions that have timed out
// Per CONTEXT.md Phase 14: PENDING sessions without tx_hash older than 10 minutes are soft-deleted
// Sessions with tx_hash set are preserved (confirmation in progress per RESEARCH.md Pitfall #3)
func (t *TimeoutEnforcer) enforcePendingTimeouts(ctx context.Context) error {
	cutoff := time.Now().Add(-PendingTimeout)

	// Use soft delete instead of TransitionToFailed
	// SoftDeletePendingBefore excludes sessions with tx_hash (confirmation in progress)
	count, err := t.softDeleter.SoftDeletePendingBefore(ctx, cutoff)
	if err != nil {
		t.logger.Error("failed to soft delete expired PENDING sessions", "error", err)
		return err
	}

	if count > 0 {
		t.logger.Info("TTL cleanup: soft deleted expired PENDING sessions",
			"count", count,
			"cutoffAge", PendingTimeout,
		)
	}

	return nil
}

// enforceRunningTimeouts fails RUNNING sessions with no recent heartbeat
// Per CONTEXT.md: RUNNING sessions without heartbeat for 30 seconds are failed
// Note: Uses UpdatedAt as a proxy for heartbeat time
func (t *TimeoutEnforcer) enforceRunningTimeouts(ctx context.Context) error {
	staleSessions, err := t.repo.FindStale(ctx, domain.RentalStateRunning, RunningTimeout)
	if err != nil {
		return err
	}

	for _, session := range staleSessions {
		t.logger.Info("failing stale RUNNING session",
			"sessionId", session.ID,
			"lastUpdated", session.UpdatedAt,
			"age", time.Since(session.UpdatedAt),
		)

		transitionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := t.failer.TransitionToFailed(transitionCtx, session.ID, "RUNNING timeout: node unresponsive")
		cancel()

		if err != nil {
			t.logger.Error("failed to transition stale RUNNING session",
				"sessionId", session.ID,
				"error", err,
			)
			// Continue to next session
		}
	}

	if len(staleSessions) > 0 {
		t.logger.Info("running timeout check complete", "failedCount", len(staleSessions))
	}

	return nil
}

// CheckOnce runs a single timeout check (useful for testing)
func (t *TimeoutEnforcer) CheckOnce(ctx context.Context) error {
	return t.checkTimeouts(ctx)
}

// Compile-time verification that SessionManager implements SessionFailer
var _ SessionFailer = (*SessionManager)(nil)
