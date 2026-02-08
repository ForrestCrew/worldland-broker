package sessions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/worldland/worldland-hub/internal/domain"
)

// ExecutorRouter routes GPU session operations to the correct executor
// based on the provider type (Docker or K8s).
// Implements domain.SessionCleanup for EventProcessor usage.
type ExecutorRouter struct {
	providerRepo   domain.ProviderRepository
	nodeRepo       domain.NodeRepository
	dockerExecutor domain.JobExecutor // For Docker (mTLS) providers
	k8sExecutor    domain.JobExecutor // For K8s providers (can be nil if no K8s clusters)
	logger         *slog.Logger
}

// Compile-time interface check
var _ domain.SessionCleanup = (*ExecutorRouter)(nil)

// NewExecutorRouter creates a new executor router
func NewExecutorRouter(
	providerRepo domain.ProviderRepository,
	nodeRepo domain.NodeRepository,
	dockerExecutor domain.JobExecutor,
	k8sExecutor domain.JobExecutor,
	logger *slog.Logger,
) *ExecutorRouter {
	return &ExecutorRouter{
		providerRepo:   providerRepo,
		nodeRepo:       nodeRepo,
		dockerExecutor: dockerExecutor,
		k8sExecutor:    k8sExecutor,
		logger:         logger,
	}
}

// GetExecutorForSession determines the correct executor based on the session's provider type
func (r *ExecutorRouter) GetExecutorForSession(ctx context.Context, session *domain.RentalSession) (domain.JobExecutor, error) {
	providerType, err := r.resolveProviderType(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve provider type: %w", err)
	}

	switch providerType {
	case domain.ProviderTypeDocker:
		if r.dockerExecutor == nil {
			return nil, fmt.Errorf("docker executor not configured")
		}
		return r.dockerExecutor, nil

	case domain.ProviderTypeK8s:
		if r.k8sExecutor == nil {
			return nil, fmt.Errorf("K8s executor not configured")
		}
		return r.k8sExecutor, nil

	default:
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}
}

// DeleteSessionContainer implements domain.SessionCleanup.
// Routes container deletion to the correct executor based on provider type.
func (r *ExecutorRouter) DeleteSessionContainer(ctx context.Context, session *domain.RentalSession) error {
	executor, err := r.GetExecutorForSession(ctx, session)
	if err != nil {
		r.logger.Warn("failed to get executor for session cleanup",
			"sessionID", session.ID,
			"error", err,
		)
		return nil // Best-effort cleanup
	}

	return executor.DeleteGPUSession(ctx, session)
}

// GetSSHConnectionInfo retrieves SSH info through the correct executor
func (r *ExecutorRouter) GetSSHConnectionInfo(ctx context.Context, session *domain.RentalSession) (*domain.SSHConnectionInfo, error) {
	executor, err := r.GetExecutorForSession(ctx, session)
	if err != nil {
		return nil, err
	}

	return executor.GetSSHConnectionInfo(ctx, session)
}

// resolveProviderType looks up the provider type for a session's node.
// Uses node-level heuristic: nodes with APIEndpoint are Docker/mTLS nodes,
// nodes without APIEndpoint are K8s cluster nodes.
// This allows mixed provider types (Docker + K8s) under one provider.
func (r *ExecutorRouter) resolveProviderType(ctx context.Context, session *domain.RentalSession) (domain.ProviderType, error) {
	// Look up node to get provider ID and check node-level attributes
	node, err := r.nodeRepo.GetByID(ctx, session.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %w", session.NodeID, err)
	}

	// Node-level routing: Docker/mTLS nodes have APIEndpoint set,
	// K8s cluster nodes don't (they're managed by the K8s cluster)
	if node.APIEndpoint != "" {
		r.logger.Debug("routing to Docker executor (node has APIEndpoint)",
			"nodeID", node.ID, "apiEndpoint", node.APIEndpoint)
		return domain.ProviderTypeDocker, nil
	}

	// Check provider type as fallback
	provider, err := r.providerRepo.GetByID(ctx, node.ProviderID)
	if err != nil {
		return "", fmt.Errorf("failed to get provider %s: %w", node.ProviderID, err)
	}

	if provider.ProviderType == domain.ProviderTypeK8s {
		r.logger.Debug("routing to K8s executor (K8s provider, no APIEndpoint)",
			"nodeID", node.ID, "providerID", provider.ID)
		return domain.ProviderTypeK8s, nil
	}

	// Default to Docker for backward compatibility
	return domain.ProviderTypeDocker, nil
}
