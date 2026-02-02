package sessions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
)

// K8sStateHandler implements k8s.StateChangeHandler to bridge PodWatcher and SessionManager
type K8sStateHandler struct {
	sessionManager *SessionManager
	sessionRepo    domain.RentalSessionRepository
	logger         *slog.Logger
}

// NewK8sStateHandler creates a new K8sStateHandler
func NewK8sStateHandler(
	sessionManager *SessionManager,
	sessionRepo domain.RentalSessionRepository,
	logger *slog.Logger,
) *K8sStateHandler {
	return &K8sStateHandler{
		sessionManager: sessionManager,
		sessionRepo:    sessionRepo,
		logger:         logger,
	}
}

// Ensure K8sStateHandler implements StateChangeHandler interface
var _ k8s.StateChangeHandler = (*K8sStateHandler)(nil)

// OnPodRunning is called when a Pod becomes Running and Ready
func (h *K8sStateHandler) OnPodRunning(ctx context.Context, sessionID string) error {
	// Load session from repo
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Check if already RUNNING (idempotent)
	if session.State == domain.RentalStateRunning {
		h.logger.Debug("pod running but session already RUNNING (idempotent)",
			"sessionId", sessionID,
			"state", session.State,
		)
		return nil
	}

	// If PENDING, this means Pod is ready BEFORE blockchain confirmation
	// Blockchain confirmation still needed - log and return
	if session.State == domain.RentalStatePending {
		h.logger.Info("pod running but session still PENDING (awaiting blockchain confirmation)",
			"sessionId", sessionID,
			"state", session.State,
		)
		return nil
	}

	// For any other state, just log
	h.logger.Info("pod running state sync",
		"sessionId", sessionID,
		"currentState", session.State,
	)

	return nil
}

// OnPodFailed is called when a Pod enters Failed phase
func (h *K8sStateHandler) OnPodFailed(ctx context.Context, sessionID string, reason string) error {
	// Load session from repo
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// If already terminal state, return nil (idempotent)
	if session.IsTerminal() {
		h.logger.Debug("pod failed but session already terminal (idempotent)",
			"sessionId", sessionID,
			"state", session.State,
		)
		return nil
	}

	// If RUNNING or PENDING, transition to FAILED
	if session.State == domain.RentalStateRunning || session.State == domain.RentalStatePending {
		h.logger.Warn("pod failed - transitioning session to FAILED",
			"sessionId", sessionID,
			"currentState", session.State,
			"reason", reason,
		)

		if err := h.sessionManager.TransitionToFailed(ctx, sessionID, fmt.Sprintf("pod failed: %s", reason)); err != nil {
			return fmt.Errorf("failed to transition to FAILED: %w", err)
		}

		h.logger.Info("session transitioned to FAILED due to pod failure",
			"sessionId", sessionID,
			"reason", reason,
		)
	}

	return nil
}

// OnPodSucceeded is called when a Pod completes successfully
func (h *K8sStateHandler) OnPodSucceeded(ctx context.Context, sessionID string) error {
	// Load session from repo
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// If already terminal, return nil (idempotent)
	if session.IsTerminal() {
		h.logger.Debug("pod succeeded but session already terminal (idempotent)",
			"sessionId", sessionID,
			"state", session.State,
		)
		return nil
	}

	// Transition to STOPPED (Pod completed normally)
	h.logger.Info("pod succeeded - transitioning session to STOPPED",
		"sessionId", sessionID,
		"currentState", session.State,
	)

	if err := h.sessionManager.TransitionToStopped(ctx, sessionID, session.UpdatedAt, "0", "", 0); err != nil {
		return fmt.Errorf("failed to transition to STOPPED: %w", err)
	}

	h.logger.Info("session transitioned to STOPPED due to pod success",
		"sessionId", sessionID,
	)

	return nil
}

// OnPodDeleted is called when a Pod is deleted
func (h *K8sStateHandler) OnPodDeleted(ctx context.Context, sessionID string) error {
	// Load session from repo
	session, err := h.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		// If session doesn't exist, log orphan pod deletion (ok)
		h.logger.Warn("pod deleted but session not found (orphan pod)",
			"sessionId", sessionID,
		)
		return nil
	}

	// If session exists and RUNNING, transition to STOPPED (unexpected deletion)
	if session.State == domain.RentalStateRunning {
		h.logger.Warn("pod deleted unexpectedly - transitioning session to STOPPED",
			"sessionId", sessionID,
			"state", session.State,
		)

		if err := h.sessionManager.TransitionToStopped(ctx, sessionID, session.UpdatedAt, "0", "", 0); err != nil {
			return fmt.Errorf("failed to transition to STOPPED: %w", err)
		}

		h.logger.Info("session transitioned to STOPPED due to unexpected pod deletion",
			"sessionId", sessionID,
		)
		return nil
	}

	// For other states, just log
	h.logger.Info("pod deleted",
		"sessionId", sessionID,
		"state", session.State,
	)

	return nil
}
