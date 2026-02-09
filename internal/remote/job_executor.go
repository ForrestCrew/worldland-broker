package remote

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/worldland/worldland-hub/internal/domain"
)

// RemoteJobExecutor implements domain.JobExecutor for Docker-based providers.
// It wraps the existing remote.JobManager for mTLS command-based operations.
type RemoteJobExecutor struct {
	jobManager   *JobManager
	nodeRepo     domain.NodeRepository
	providerRepo domain.ProviderRepository
	logger       *slog.Logger
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

// WithProviderRepo sets the provider repository for wallet address lookups
func (e *RemoteJobExecutor) WithProviderRepo(providerRepo domain.ProviderRepository) *RemoteJobExecutor {
	e.providerRepo = providerRepo
	return e
}

// Compile-time interface check
var _ domain.JobExecutor = (*RemoteJobExecutor)(nil)

// CreateGPUSession sends a start_rental command to the remote node via mTLS
func (e *RemoteJobExecutor) CreateGPUSession(ctx context.Context, spec domain.JobSpec) (string, error) {
	// Look up node to get provider info
	node, err := e.nodeRepo.GetByID(ctx, spec.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup node %s: %w", spec.NodeID, err)
	}

	// Resolve mTLS node ID: the mTLS client map uses wallet address (certificate CN),
	// not the node UUID. Look up provider's wallet address.
	mtlsNodeID := node.ID
	if e.providerRepo != nil {
		provider, err := e.providerRepo.GetByID(ctx, node.ProviderID)
		if err == nil && provider != nil {
			mtlsNodeID = provider.WalletAddress
			e.logger.Debug("resolved mTLS node ID from provider wallet",
				"nodeID", node.ID, "walletAddress", mtlsNodeID)
		}
	}

	// Extract host IP from node's API endpoint (e.g., "https://136.113.211.129:8444" → "136.113.211.129")
	nodeHost := ""
	if node.APIEndpoint != "" {
		if u, err := url.Parse(node.APIEndpoint); err == nil {
			nodeHost = u.Hostname()
		}
	}

	// Convert domain.JobSpec to remote.GPUJobSpec
	remoteSpec := GPUJobSpec{
		SessionID:     spec.SessionID,
		UserAddress:   spec.UserAddress,
		ProviderID:    spec.ProviderID,
		NodeID:        mtlsNodeID, // mTLS identifier = wallet address
		NodeHost:      nodeHost,
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

	// Resolve mTLS node ID (wallet address)
	mtlsNodeID := node.ID
	if e.providerRepo != nil {
		provider, err := e.providerRepo.GetByID(ctx, node.ProviderID)
		if err == nil && provider != nil {
			mtlsNodeID = provider.WalletAddress
		}
	}

	return e.jobManager.DeleteGPUSession(ctx, mtlsNodeID, session.ID)
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
