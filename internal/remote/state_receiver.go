package remote

import (
	"context"
	"fmt"
	"log/slog"
)

// StateChangeHandler handles session state changes from remote container events.
// This is the same interface as k8s.StateChangeHandler for compatibility.
type StateChangeHandler interface {
	OnContainerRunning(ctx context.Context, sessionID string) error
	OnContainerFailed(ctx context.Context, sessionID string, reason string) error
	OnContainerStopped(ctx context.Context, sessionID string) error
}

// StateReceiver receives container state updates from remote nodes via mTLS
// and translates them into session state transitions.
type StateReceiver struct {
	stateHandler StateChangeHandler
	jobManager   *JobManager
	logger       *slog.Logger
}

// NewStateReceiver creates a new StateReceiver
func NewStateReceiver(stateHandler StateChangeHandler, jobManager *JobManager, logger *slog.Logger) *StateReceiver {
	return &StateReceiver{
		stateHandler: stateHandler,
		jobManager:   jobManager,
		logger:       logger,
	}
}

// HandleStateUpdate processes a container state update from a node.
// Called when a node sends a state_update message via mTLS.
func (r *StateReceiver) HandleStateUpdate(ctx context.Context, nodeID string, update ContainerStateUpdate) error {
	r.logger.Info("received container state update",
		"nodeId", nodeID,
		"sessionId", update.SessionID,
		"state", update.State,
	)

	// Update SSH info in job manager if available
	if update.SSHHost != "" && update.SSHPort > 0 {
		r.jobManager.UpdateSSHInfo(update.SessionID, update.SSHHost, update.SSHPort)
	}

	switch update.State {
	case "running":
		if err := r.stateHandler.OnContainerRunning(ctx, update.SessionID); err != nil {
			return fmt.Errorf("failed to handle container running: %w", err)
		}

	case "stopped", "exited":
		if err := r.stateHandler.OnContainerStopped(ctx, update.SessionID); err != nil {
			return fmt.Errorf("failed to handle container stopped: %w", err)
		}

	case "failed", "dead":
		reason := update.Error
		if reason == "" {
			reason = "container failed"
		}
		if err := r.stateHandler.OnContainerFailed(ctx, update.SessionID, reason); err != nil {
			return fmt.Errorf("failed to handle container failed: %w", err)
		}

	default:
		r.logger.Debug("ignoring unknown container state",
			"sessionId", update.SessionID,
			"state", update.State,
		)
	}

	return nil
}
