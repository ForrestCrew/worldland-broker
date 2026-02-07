package remote

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/worldland/worldland-hub/internal/domain"
)

// RemoteJobExecutor implements domain.JobExecutor for Docker-based providers.
// It wraps the existing remote.JobManager for mTLS command-based operations.
type RemoteJobExecutor struct {
	jobManager *JobManager
	nodeRepo   domain.NodeRepository
	logger     *slog.Logger
}

// NewRemoteJobExecutor creates a new Docker/remote job executor
func NewRemoteJobExecutor(
	jobManager *JobManager,
	nodeRepo domain.NodeRepository,
	logger *slog.Logger,
) *RemoteJobExecutor {
	return &RemoteJobExecutor{
		jobManager: jobManager,
		nodeRepo:   nodeRepo,
		logger:     logger,
	}
}

// Compile-time interface check
var _ domain.JobExecutor = (*RemoteJobExecutor)(nil)

// CreateGPUSession sends a start_rental command to the remote node via mTLS
func (e *RemoteJobExecutor) CreateGPUSession(ctx context.Context, spec domain.JobSpec) (string, error) {
	// Look up node to get mTLS node ID
	node, err := e.nodeRepo.GetByID(ctx, spec.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup node %s: %w", spec.NodeID, err)
	}

	// Convert domain.JobSpec to remote.GPUJobSpec
	remoteSpec := GPUJobSpec{
		SessionID:     spec.SessionID,
		UserAddress:   spec.UserAddress,
		ProviderID:    spec.ProviderID,
		NodeID:        node.ID, // mTLS identifier
		GPUCount:      spec.GPUCount,
		GPUModel:      spec.GPUModel,
		GPUDeviceID:   spec.GPUDeviceID,
		Image:         spec.Image,
		CPURequest:    spec.CPURequest,
		MemoryRequest: spec.MemoryRequest,
	}

	password, err := e.jobManager.CreateGPUSession(ctx, remoteSpec)
	if err != nil {
		return "", fmt.Errorf("remote CreateGPUSession failed: %w", err)
	}

	return password, nil
}

// DeleteGPUSession sends a stop_rental command to the remote node via mTLS
func (e *RemoteJobExecutor) DeleteGPUSession(ctx context.Context, session *domain.RentalSession) error {
	// Look up node to get mTLS node ID
	node, err := e.nodeRepo.GetByID(ctx, session.NodeID)
	if err != nil {
		e.logger.Warn("failed to lookup node for delete, skipping",
			"sessionID", session.ID,
			"nodeID", session.NodeID,
			"error", err,
		)
		return nil
	}

	return e.jobManager.DeleteGPUSession(ctx, node.ID, session.ID)
}

// GetSSHConnectionInfo retrieves SSH connection details from in-memory store
func (e *RemoteJobExecutor) GetSSHConnectionInfo(ctx context.Context, session *domain.RentalSession) (*domain.SSHConnectionInfo, error) {
	info, err := e.jobManager.GetSSHConnectionInfo(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	return &domain.SSHConnectionInfo{
		Host:     info.Host,
		Port:     info.Port,
		Password: info.Password,
		User:     info.User,
	}, nil
}
