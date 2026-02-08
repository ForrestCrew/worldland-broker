package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// K8sJobExecutor implements domain.JobExecutor for K8s-based providers.
// It wraps the existing JobManager and routes to the correct cluster
// based on providerID via ExternalClusterRegistry.
type K8sJobExecutor struct {
	clusterRegistry *ExternalClusterRegistry
	nodeRepo        domain.NodeRepository
	logger          *slog.Logger
	defaultImage    string
}

// NewK8sJobExecutor creates a new K8s job executor
func NewK8sJobExecutor(
	clusterRegistry *ExternalClusterRegistry,
	logger *slog.Logger,
	defaultImage string,
) *K8sJobExecutor {
	return &K8sJobExecutor{
		clusterRegistry: clusterRegistry,
		logger:          logger,
		defaultImage:    defaultImage,
	}
}

// WithNodeRepo sets the node repository for provider ID lookups
func (e *K8sJobExecutor) WithNodeRepo(nodeRepo domain.NodeRepository) *K8sJobExecutor {
	e.nodeRepo = nodeRepo
	return e
}

// Compile-time interface check
var _ domain.JobExecutor = (*K8sJobExecutor)(nil)

// CreateGPUSession creates a GPU Pod on the provider's K8s cluster
func (e *K8sJobExecutor) CreateGPUSession(ctx context.Context, spec domain.JobSpec) (string, error) {
	client := e.clusterRegistry.GetClient(spec.ProviderID)
	if client == nil {
		return "", fmt.Errorf("no K8s cluster registered for provider %s", spec.ProviderID)
	}

	// Create a JobManager for this provider's cluster
	jm := NewJobManager(client.Clientset, e.logger).WithExternalHost(client.ExternalHost)

	// Ensure tenant namespace exists
	tenantOrch := NewTenantOrchestrator(client.Clientset, e.logger)
	if _, err := tenantOrch.EnsureTenant(ctx, spec.UserAddress, spec.GPUCount); err != nil {
		return "", fmt.Errorf("failed to ensure tenant namespace: %w", err)
	}

	// Convert domain.JobSpec to k8s.GPUJobSpec
	image := spec.Image
	if image == "" {
		image = e.defaultImage
	}

	cpuRequest := spec.CPURequest
	if cpuRequest == "" {
		cpuRequest = "2"
	}
	memoryRequest := spec.MemoryRequest
	if memoryRequest == "" {
		memoryRequest = "4Gi"
	}
	cpuLimit := spec.CPULimit
	if cpuLimit == "" {
		cpuLimit = "4"
	}
	memoryLimit := spec.MemoryLimit
	if memoryLimit == "" {
		memoryLimit = "8Gi"
	}

	k8sSpec := GPUJobSpec{
		SessionID:     spec.SessionID,
		UserAddress:   spec.UserAddress,
		ProviderID:    spec.ProviderID,
		GPUCount:      spec.GPUCount,
		GPUModel:      spec.GPUModel,
		Image:         image,
		CPURequest:    cpuRequest,
		MemoryRequest: memoryRequest,
		CPULimit:      cpuLimit,
		MemoryLimit:   memoryLimit,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
	}

	password, err := jm.CreateGPUSession(ctx, k8sSpec)
	if err != nil {
		return "", fmt.Errorf("K8s CreateGPUSession failed: %w", err)
	}

	return password, nil
}

// DeleteGPUSession deletes a GPU Pod from the provider's K8s cluster
func (e *K8sJobExecutor) DeleteGPUSession(ctx context.Context, session *domain.RentalSession) error {
	providerID := e.getProviderID(ctx, session)
	client := e.clusterRegistry.GetClient(providerID)
	if client == nil {
		e.logger.Warn("no K8s cluster for provider, skipping delete",
			"providerID", providerID,
			"sessionID", session.ID,
		)
		return nil
	}

	jm := NewJobManager(client.Clientset, e.logger)
	return jm.DeleteGPUSession(ctx, session.UserAddress, session.ID)
}

// GetSSHConnectionInfo retrieves SSH connection details from the K8s cluster
func (e *K8sJobExecutor) GetSSHConnectionInfo(ctx context.Context, session *domain.RentalSession) (*domain.SSHConnectionInfo, error) {
	providerID := e.getProviderID(ctx, session)
	client := e.clusterRegistry.GetClient(providerID)
	if client == nil {
		return nil, fmt.Errorf("no K8s cluster registered for provider %s", providerID)
	}

	jm := NewJobManager(client.Clientset, e.logger).WithExternalHost(client.ExternalHost)
	k8sInfo, err := jm.GetSSHConnectionInfo(ctx, session.UserAddress, session.ID)
	if err != nil {
		return nil, err
	}

	return &domain.SSHConnectionInfo{
		Host:     k8sInfo.Host,
		Port:     k8sInfo.Port,
		Password: k8sInfo.Password,
		User:     "user",
	}, nil
}

// getProviderID looks up the provider ID from the node repository.
// The session stores NodeID, but the cluster registry uses provider ID as key.
func (e *K8sJobExecutor) getProviderID(ctx context.Context, session *domain.RentalSession) string {
	if e.nodeRepo != nil {
		node, err := e.nodeRepo.GetByID(ctx, session.NodeID)
		if err == nil && node.ProviderID != "" {
			return node.ProviderID
		}
		e.logger.Warn("failed to look up provider ID from node",
			"nodeID", session.NodeID,
			"error", err,
		)
	}
	// Fallback: return NodeID (will likely fail cluster lookup)
	return session.NodeID
}
