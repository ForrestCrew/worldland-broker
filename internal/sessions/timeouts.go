package sessions

import (
	"context"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

const (
	// PendingTimeout is how long a session can stay PENDING before failing
	// PENDING sessions without a RentalStarted blockchain event are failed after this duration
	PendingTimeout = 5 * time.Minute

	// RunningTimeout is how long a RUNNING session can go without heartbeat before failing
	// RUNNING sessions with no node heartbeat are failed after this duration
	RunningTimeout = 30 * time.Second

	// DefaultCheckInterval is how often to check for timeouts
	DefaultCheckInterval = 30 * time.Second
)

// SessionFailer defines the interface for failing sessions (for testability)
type SessionFailer interface {
	TransitionToFailed(ctx context.Context, sessionID string, reason string) error
}

// TimeoutEnforcer runs background timeout checks to fail stale sessions
type TimeoutEnforcer struct {
	failer        SessionFailer
	repo          domain.RentalSessionRepository
	checkInterval time.Duration
	logger        *slog.Logger
}

// NewTimeoutEnforcer creates a new timeout enforcer
// failer: typically a *SessionManager that can transition sessions to FAILED state
// repo: repository to find stale sessions
// logger: structured logger for timeout events
func NewTimeoutEnforcer(
	failer SessionFailer,
	repo domain.RentalSessionRepository,
	logger *slog.Logger,
) *TimeoutEnforcer {
	return &TimeoutEnforcer{
		failer:        failer,
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

// enforcePendingTimeouts fails sessions that have been PENDING too long
// Per CONTEXT.md: PENDING sessions without RentalStarted event within 5 minutes are failed
func (t *TimeoutEnforcer) enforcePendingTimeouts(ctx context.Context) error {
	staleSessions, err := t.repo.FindStale(ctx, domain.RentalStatePending, PendingTimeout)
	if err != nil {
		return err
	}

	for _, session := range staleSessions {
		t.logger.Info("failing stale PENDING session",
			"sessionId", session.ID,
			"createdAt", session.CreatedAt,
			"age", time.Since(session.CreatedAt),
		)

		transitionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := t.failer.TransitionToFailed(transitionCtx, session.ID, "PENDING timeout: no RentalStarted event received")
		cancel()

		if err != nil {
			t.logger.Error("failed to transition stale PENDING session",
				"sessionId", session.ID,
				"error", err,
			)
			// Continue to next session - don't let one failure stop others
		}
	}

	if len(staleSessions) > 0 {
		t.logger.Info("pending timeout check complete", "failedCount", len(staleSessions))
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
