package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/worldland/worldland-hub/internal/domain"
)

// K8sJobExecutor implements domain.JobExecutor for K8s-based providers.
// It wraps the existing JobManager and routes to the correct cluster
// based on providerID via ExternalClusterRegistry.
type K8sJobExecutor struct {
	clusterRegistry  *ExternalClusterRegistry
	nodeRepo         domain.NodeRepository
	capacityTracker  *CapacityTracker
	logger           *slog.Logger
	defaultImage     string
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

// WithCapacityTracker sets the capacity tracker for resource reservation
func (e *K8sJobExecutor) WithCapacityTracker(ct *CapacityTracker) *K8sJobExecutor {
	e.capacityTracker = ct
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

	// Apply defaults for resource specs
	cpuCores := spec.CPUCores
	if cpuCores <= 0 {
		cpuCores = 4
	}
	memoryGB := spec.MemoryGB
	if memoryGB <= 0 {
		memoryGB = 16
	}
	storageGB := spec.StorageGB
	if storageGB <= 0 {
		storageGB = 20
	}

	expiresAt := spec.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	// Resolve K8s node name and hostname for scheduling (use session's specific node)
	nodeName, nodeHostname := e.resolveK8sNode(ctx, spec.ProviderID, spec.NodeID)

	// Cap storage to node's actual ephemeral-storage capacity (dynamic per node)
	if e.nodeRepo != nil && spec.NodeID != "" {
		node, err := e.nodeRepo.GetByID(ctx, spec.NodeID)
		if err == nil && node.MaxStorageGB > 0 && storageGB > node.MaxStorageGB {
			e.logger.Warn("storage request capped to node max",
				"requested", storageGB, "nodeMax", node.MaxStorageGB, "sessionID", spec.SessionID)
			storageGB = node.MaxStorageGB
		}
	}

	// Reserve capacity before creating the Pod (proxy pattern: GPU → CPU → Memory with rollback)
	alloc := ResourceAllocation{
		SessionID: spec.SessionID,
		GPUType:   spec.GPUModel,
		GPUCount:  spec.GPUCount,
		CPUCores:  cpuCores,
		MemoryGB:  memoryGB,
	}
	if e.capacityTracker != nil && nodeName != "" {
		if err := e.capacityTracker.AllocateResources(spec.ProviderID, nodeName, alloc); err != nil {
			return "", fmt.Errorf("insufficient capacity: %w", err)
		}
	}

	k8sSpec := GPUJobSpec{
		SessionID:    spec.SessionID,
		UserAddress:  spec.UserAddress,
		ProviderID:   spec.ProviderID,
		GPUCount:     spec.GPUCount,
		GPUModel:     spec.GPUModel,
		Image:        image,
		CPUCores:     cpuCores,
		MemoryGB:     memoryGB,
		StorageGB:    storageGB,
		ExpiresAt:    expiresAt,
		NodeHostname: nodeHostname,
	}

	password, err := jm.CreateGPUSession(ctx, k8sSpec)
	if err != nil {
		// Release reserved capacity on failure
		if e.capacityTracker != nil && nodeName != "" {
			e.capacityTracker.ReleaseResources(spec.ProviderID, nodeName, alloc)
		}
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
	if err := jm.DeleteGPUSession(ctx, session.UserAddress, session.ID); err != nil {
		return err
	}

	// Release capacity after successful deletion (proxy pattern: releaseJobResources)
	if e.capacityTracker != nil {
		nodeName, _ := e.resolveK8sNode(ctx, providerID, session.NodeID)
		if nodeName != "" {
			gpuCount := session.GPUCount
			if gpuCount <= 0 {
				gpuCount = 1
			}
			cpuCores := session.CPUCores
			if cpuCores <= 0 {
				cpuCores = 4
			}
			memoryGB := session.MemoryGB
			if memoryGB <= 0 {
				memoryGB = 16
			}
			e.capacityTracker.ReleaseResources(providerID, nodeName, ResourceAllocation{
				SessionID: session.ID,
				GPUCount:  gpuCount,
				CPUCores:  cpuCores,
				MemoryGB:  memoryGB,
			})
		}
	}

	return nil
}

// GetSSHConnectionInfo retrieves SSH connection details from the K8s cluster.
// If K8s returns a private IP, falls back to DB-stored external_ip for the node.
// Caches SSH info to DB for resilience against K8s unavailability.
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

	host := k8sInfo.Host

	// If K8s returned a private IP, fallback to DB-stored external IP for the node
	if isPrivateIP(host) && e.nodeRepo != nil {
		node, err := e.nodeRepo.GetByID(ctx, session.NodeID)
		if err == nil && node.ExternalIP != "" {
			e.logger.Info("SSH host: private IP replaced with node external IP",
				"sessionID", session.ID,
				"privateIP", host,
				"externalIP", node.ExternalIP,
			)
			host = node.ExternalIP
		}
	}

	return &domain.SSHConnectionInfo{
		Host:     host,
		Port:     k8sInfo.Port,
		Password: k8sInfo.Password,
		User:     "root",
	}, nil
}

// isPrivateIP checks if an IP address is in a private range (10.x, 172.16-31.x, 192.168.x)
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	privateRanges := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// GetPodStatus returns a human-readable container status for frontend display
func (e *K8sJobExecutor) GetPodStatus(ctx context.Context, session *domain.RentalSession) (string, error) {
	providerID := e.getProviderID(ctx, session)
	client := e.clusterRegistry.GetClient(providerID)
	if client == nil {
		return "Pending", nil
	}

	jm := NewJobManager(client.Clientset, e.logger)
	phase, ready, err := jm.GetPodStatus(ctx, session.UserAddress, session.ID)
	if err != nil {
		return "Pending", nil // Pod not created yet
	}

	switch {
	case *phase == corev1.PodPending:
		return "Creating", nil
	case *phase == corev1.PodRunning && !ready:
		return "Starting", nil
	case *phase == corev1.PodRunning && ready:
		return "Running", nil
	case *phase == corev1.PodFailed:
		return "Failed", nil
	default:
		return "Pending", nil
	}
}

// DeleteSessionContainer implements domain.SessionCleanup for EventProcessor
func (e *K8sJobExecutor) DeleteSessionContainer(ctx context.Context, session *domain.RentalSession) error {
	return e.DeleteGPUSession(ctx, session)
}

// resolveK8sNode finds the K8s node name for scheduling.
// If nodeID is provided, looks up that specific node first (session-targeted scheduling).
// Falls back to first GPU-capable node with a K8sNodeName for the provider.
func (e *K8sJobExecutor) resolveK8sNode(ctx context.Context, providerID, nodeID string) (string, string) {
	if e.nodeRepo == nil {
		return "", ""
	}

	// Primary: use the session's specific node
	if nodeID != "" {
		node, err := e.nodeRepo.GetByID(ctx, nodeID)
		if err == nil && node.K8sNodeName != "" {
			return node.K8sNodeName, node.K8sNodeName
		}
	}

	// Fallback: first GPU-capable node with K8s name for this provider
	nodes, err := e.nodeRepo.GetByProvider(ctx, providerID)
	if err != nil || len(nodes) == 0 {
		return "", ""
	}
	for _, n := range nodes {
		if n.K8sNodeName != "" && n.TotalGPUs > 0 {
			return n.K8sNodeName, n.K8sNodeName
		}
	}
	return "", ""
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
