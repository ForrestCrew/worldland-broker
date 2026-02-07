package sessions

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/remote"
)

// RemoteStateHandler implements remote.StateChangeHandler to bridge
// container state updates from remote nodes to the SessionManager.
// This replaces K8sStateHandler for Docker-based providers.
type RemoteStateHandler struct {
	sessionManager *SessionManager
	sessionRepo    domain.RentalSessionRepository
	logger         *slog.Logger
}

// NewRemoteStateHandler creates a new RemoteStateHandler
func NewRemoteStateHandler(
	sessionManager *SessionManager,
	sessionRepo domain.RentalSessionRepository,
	logger *slog.Logger,
) *RemoteStateHandler {
	return &RemoteStateHandler{
		sessionManager: sessionManager,
		sessionRepo:    sessionRepo,
		logger:         logger,
	}
}

// Ensure RemoteStateHandler implements remote.StateChangeHandler
var _ remote.StateChangeHandler = (*RemoteStateHandler)(nil)

// calculateSettlementAmount calculates settlement = duration * price_per_second
func (h *RemoteStateHandler) calculateSettlementAmount(session *domain.RentalSession, endTime time.Time) string {
	if session.StartTime == nil {
		return "0"
	}
	duration := endTime.Sub(*session.StartTime).Seconds()
	if duration <= 0 {
		return "0"
	}
	pricePerSecond, ok := new(big.Int).SetString(cleanPrice(session.PricePerSecond), 10)
	if !ok {
		return "0"
	}
	durationBig := big.NewInt(int64(duration))
	total := new(big.Int).Mul(pricePerSecond, durationBig)
	return total.String()
}

// OnContainerRunning is called when a Docker container becomes running on a remote node
func (h *RemoteStateHandler) OnContainerRunning(ctx context.Context, sessionID string) error {
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// If session is RUNNING, update heartbeat
	if session.State == domain.RentalStateRunning {
		if err := h.sessionRepo.TouchSession(ctx, sessionID); err != nil {
			h.logger.Warn("failed to touch session (heartbeat)",
				"sessionId", sessionID,
				"error", err,
			)
		}
		return nil
	}

	// If PENDING, container is ready before blockchain confirmation
	if session.State == domain.RentalStatePending {
		h.logger.Info("container running but session still PENDING (awaiting blockchain confirmation)",
			"sessionId", sessionID,
		)
		return nil
	}

	h.logger.Info("container running state sync",
		"sessionId", sessionID,
		"currentState", session.State,
	)
	return nil
}

// OnContainerFailed is called when a Docker container fails on a remote node
func (h *RemoteStateHandler) OnContainerFailed(ctx context.Context, sessionID string, reason string) error {
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	if session.IsTerminal() {
		return nil
	}

	if session.State == domain.RentalStateRunning || session.State == domain.RentalStatePending {
		h.logger.Warn("container failed - transitioning session to FAILED",
			"sessionId", sessionID,
			"currentState", session.State,
			"reason", reason,
		)

		if err := h.sessionManager.TransitionToFailed(ctx, sessionID, fmt.Sprintf("container failed: %s", reason)); err != nil {
			return fmt.Errorf("failed to transition to FAILED: %w", err)
		}
	}

	return nil
}

// OnContainerStopped is called when a Docker container stops on a remote node
func (h *RemoteStateHandler) OnContainerStopped(ctx context.Context, sessionID string) error {
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		h.logger.Warn("container stopped but session not found (orphan container)",
			"sessionId", sessionID,
		)
		return nil
	}

	if session.IsTerminal() {
		return nil
	}

	if session.State == domain.RentalStateRunning {
		endTime := time.Now()
		settlementAmount := h.calculateSettlementAmount(session, endTime)
		h.logger.Info("container stopped - transitioning session to STOPPED",
			"sessionId", sessionID,
			"settlementAmount", settlementAmount,
		)

		if err := h.sessionManager.TransitionToStopped(ctx, sessionID, endTime, settlementAmount, "", 0); err != nil {
			return fmt.Errorf("failed to transition to STOPPED: %w", err)
		}
	}

	return nil
}
