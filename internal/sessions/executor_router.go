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

// resolveProviderType looks up the provider type for a session's node
func (r *ExecutorRouter) resolveProviderType(ctx context.Context, session *domain.RentalSession) (domain.ProviderType, error) {
	// Look up node to get provider ID
	node, err := r.nodeRepo.GetByID(ctx, session.NodeID)
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %w", session.NodeID, err)
	}

	// Look up provider to get type
	provider, err := r.providerRepo.GetByID(ctx, node.ProviderID)
	if err != nil {
		return "", fmt.Errorf("failed to get provider %s: %w", node.ProviderID, err)
	}

	// Default to Docker if provider_type is empty (backward compatibility)
	if provider.ProviderType == "" {
		return domain.ProviderTypeDocker, nil
	}

	return provider.ProviderType, nil
}
