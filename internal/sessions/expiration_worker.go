package sessions

import (
	"context"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// cleanPrice removes decimal points from price strings for BigInt compatibility
func cleanPrice(price string) string {
	if idx := strings.Index(price, "."); idx != -1 {
		return price[:idx]
	}
	return price
}

const (
	// DefaultExpirationInterval is how often to check for expired sessions
	DefaultExpirationInterval = 5 * time.Second

	// ExpirationBatchLimit prevents long-running queries
	ExpirationBatchLimit = 100
)

// ExpirationWorker monitors extended sessions and auto-terminates them at expiration
// V4: Uses a single K8s JobExecutor — no Docker/remote branching.
type ExpirationWorker struct {
	sessionRepo domain.RentalSessionRepository
	manager     *SessionManager
	nodeRepo    domain.NodeRepository
	executor    domain.JobExecutor // K8s executor (single path)
	interval    time.Duration
	logger      *slog.Logger
}

// NewExpirationWorker creates a new background worker for auto-expiring sessions
func NewExpirationWorker(
	sessionRepo domain.RentalSessionRepository,
	manager *SessionManager,
	nodeRepo domain.NodeRepository,
	executor domain.JobExecutor,
	logger *slog.Logger,
) *ExpirationWorker {
	return &ExpirationWorker{
		sessionRepo: sessionRepo,
		manager:     manager,
		nodeRepo:    nodeRepo,
		executor:    executor,
		interval:    DefaultExpirationInterval,
		logger:      logger,
	}
}

// WithInterval sets a custom check interval (useful for testing)
func (w *ExpirationWorker) WithInterval(interval time.Duration) *ExpirationWorker {
	w.interval = interval
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

	// Delete K8s Pod via executor
	if w.executor != nil {
		if err := w.executor.DeleteGPUSession(ctx, session); err != nil {
			w.logger.Error("failed to delete GPU session for expired session",
				"sessionId", session.ID,
				"error", err,
			)
		} else {
			w.logger.Info("deleted GPU session for expired session",
				"sessionId", session.ID,
			)
		}
	}

	// Calculate settlement amount = duration * price_per_second
	endTime := time.Now()
	settlementAmount := "0"
	if session.StartTime != nil {
		duration := endTime.Sub(*session.StartTime).Seconds()
		if duration > 0 {
			pricePerSecond, ok := new(big.Int).SetString(cleanPrice(session.PricePerSecond), 10)
			if ok {
				durationBig := big.NewInt(int64(duration))
				total := new(big.Int).Mul(pricePerSecond, durationBig)
				settlementAmount = total.String()
				w.logger.Info("calculated settlement amount",
					"sessionId", session.ID,
					"duration", duration,
					"pricePerSecond", session.PricePerSecond,
					"settlementAmount", settlementAmount,
				)
			}
		}
	}

	// Transition to STOPPED with calculated settlement amount
	if err := w.manager.TransitionToStopped(ctx, session.ID, endTime, settlementAmount, "", 0); err != nil {
		w.logger.Error("failed to transition expired session to STOPPED",
			"sessionId", session.ID,
			"error", err,
		)
		return
	}

	w.logger.Info("session expired and stopped",
		"sessionId", session.ID,
		"settlementAmount", settlementAmount,
	)
}

// ProcessOnce runs a single expiration check (useful for testing)
func (w *ExpirationWorker) ProcessOnce(ctx context.Context) {
	w.processExpiredSessions(ctx)
}
