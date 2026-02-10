package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/worldland/worldland-hub/internal/domain"
)

// NodeSyncWorker periodically discovers K8s worker nodes and registers/updates
// them in the DB. This ensures newly joined workers are automatically visible.
type NodeSyncWorker struct {
	registry *ExternalClusterRegistry
	nodeRepo domain.NodeRepository
	logger   *slog.Logger
	interval time.Duration
}

// NewNodeSyncWorker creates a new K8s node sync worker
func NewNodeSyncWorker(
	registry *ExternalClusterRegistry,
	nodeRepo domain.NodeRepository,
	logger *slog.Logger,
	interval time.Duration,
) *NodeSyncWorker {
	return &NodeSyncWorker{
		registry: registry,
		nodeRepo: nodeRepo,
		logger:   logger,
		interval: interval,
	}
}

// Start begins the periodic node sync loop
func (w *NodeSyncWorker) Start(ctx context.Context) error {
	w.logger.Info("K8s node sync worker started", "interval", w.interval)

	// Run immediately on startup
	w.syncAll(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.syncAll(ctx)
		case <-ctx.Done():
			w.logger.Info("K8s node sync worker stopped")
			return ctx.Err()
		}
	}
}

// syncAll iterates all registered K8s providers and syncs their nodes
func (w *NodeSyncWorker) syncAll(ctx context.Context) {
	providerIDs := w.registry.ListProviderIDs()
	for _, pid := range providerIDs {
		client := w.registry.GetClient(pid)
		if client == nil {
			continue
		}
		w.syncProviderNodes(ctx, pid, client)
	}
}

// syncProviderNodes discovers K8s worker nodes and registers/updates them in DB
func (w *NodeSyncWorker) syncProviderNodes(ctx context.Context, providerID string, client *ClusterClient) {
	nodeList, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		w.logger.Warn("node sync: failed to list K8s nodes",
			"providerID", providerID, "error", err)
		return
	}

	// Track which K8s node names we see (for marking offline)
	seenNodes := make(map[string]bool)
	registered := 0
	updated := 0

	for i := range nodeList.Items {
		n := &nodeList.Items[i]

		// Skip control-plane/master nodes (uses shared helper from capacity.go)
		if isControlPlane(n) {
			continue
		}

		seenNodes[n.Name] = true

		// Only register Ready nodes
		if !isNodeReady(n) {
			continue
		}

		// Extract resource info
		gpuCount, gpuType, gpuModel, vramMB := nodeSyncDetectGPU(n)
		memGB := nodeSyncMemoryGB(n)
		cpuCores := nodeSyncCPUCores(n)
		externalIP := nodeSyncExternalIP(n)
		maxStorageGB := nodeSyncMaxStorageGB(n)

		// Deterministic node ID
		nodeID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(providerID+"/"+n.Name)).String()
		gpuUUID := fmt.Sprintf("k8s-%s-%s", providerID[:8], n.Name)

		existing, _ := w.nodeRepo.GetByID(ctx, nodeID)
		if existing != nil {
			// Update existing node
			changed := false
			if existing.Status != domain.NodeStatusActive {
				existing.Status = domain.NodeStatusActive
				changed = true
			}
			if existing.GPUType != gpuType {
				existing.GPUType = gpuType
				changed = true
			}
			if existing.TotalGPUs != int(gpuCount) {
				existing.TotalGPUs = int(gpuCount)
				existing.AvailableGPUs = int(gpuCount)
				changed = true
			}
			if existing.TotalCPUCores != cpuCores {
				existing.TotalCPUCores = cpuCores
				changed = true
			}
			if existing.TotalMemoryGB != memGB {
				existing.TotalMemoryGB = memGB
				existing.MemoryGB = memGB
				changed = true
			}
			if existing.K8sNodeName != n.Name {
				existing.K8sNodeName = n.Name
				changed = true
			}
			// Update GPU model/VRAM if better data available
			if gpuModel != "" && (existing.GPUModel == "" || strings.HasPrefix(existing.GPUModel, "GPU x")) {
				existing.GPUModel = gpuModel
				changed = true
			}
			if vramMB > 0 && existing.VramMB == 0 {
				existing.VramMB = vramMB
				changed = true
			}
			// Update external IP from K8s annotation
			if externalIP != "" && existing.ExternalIP != externalIP {
				existing.ExternalIP = externalIP
				changed = true
			}
			// Update max storage from K8s ephemeral-storage allocatable
			if maxStorageGB > 0 && existing.MaxStorageGB != maxStorageGB {
				existing.MaxStorageGB = maxStorageGB
				changed = true
			}

			if changed {
				existing.UpdatedAt = time.Now()
				w.nodeRepo.Update(ctx, existing)
				updated++
			}
			registered++
			continue
		}

		// Register new K8s node
		now := time.Now()
		node := &domain.Node{
			ID:             nodeID,
			ProviderID:     providerID,
			GPUUUID:        gpuUUID,
			GPUType:        gpuType,
			MemoryGB:       memGB,
			PricePerSecond: "277777777777777", // Default 1 WLC/hr
			Status:         domain.NodeStatusActive,
			TotalGPUs:      int(gpuCount),
			AvailableGPUs:  int(gpuCount),
			TotalCPUCores:  cpuCores,
			TotalMemoryGB:  memGB,
			K8sNodeName:    n.Name,
			GPUModel:       gpuModel,
			VramMB:         vramMB,
			ExternalIP:     externalIP,
			MaxStorageGB:   maxStorageGB,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		if err := w.nodeRepo.Create(ctx, node); err != nil {
			w.logger.Warn("node sync: failed to register node",
				"nodeName", n.Name, "error", err)
			continue
		}
		registered++
		w.logger.Info("node sync: new K8s node registered",
			"providerID", providerID,
			"nodeName", n.Name,
			"gpuType", gpuType,
			"gpuCount", gpuCount,
			"cpuCores", cpuCores,
			"memoryGB", memGB,
		)
	}

	// Mark nodes that disappeared from K8s as offline
	dbNodes, err := w.nodeRepo.GetByProvider(ctx, providerID)
	if err == nil {
		for _, dbNode := range dbNodes {
			// Only check K8s-managed nodes (no APIEndpoint)
			if dbNode.APIEndpoint != "" || dbNode.K8sNodeName == "" {
				continue
			}
			if !seenNodes[dbNode.K8sNodeName] && dbNode.Status == domain.NodeStatusActive {
				dbNode.Status = domain.NodeStatusOffline
				dbNode.UpdatedAt = time.Now()
				w.nodeRepo.Update(ctx, dbNode)
				w.logger.Info("node sync: K8s node marked offline",
					"providerID", providerID,
					"nodeName", dbNode.K8sNodeName,
					"nodeID", dbNode.ID,
				)
			}
		}
	}

	if registered > 0 || updated > 0 {
		w.logger.Debug("node sync complete",
			"providerID", providerID,
			"registered", registered,
			"updated", updated,
			"k8sNodes", len(nodeList.Items),
		)
	}
}

// --- Helper functions (node_sync-specific, avoid name collision with capacity.go) ---

func nodeSyncDetectGPU(n *corev1.Node) (count int64, gpuType, gpuModel string, vramMB int) {
	// GPU count: K8s capacity (nvidia device plugin)
	gpuQty, hasGPU := n.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]
	if hasGPU {
		count = gpuQty.Value()
	}

	// GPU count: worldland annotation overrides (NVML is more reliable)
	if countStr, ok := n.Annotations["worldland.io/gpu-count"]; ok {
		if c, err := strconv.ParseInt(countStr, 10, 64); err == nil && c > 0 {
			count = c
		}
	}

	// GPU model detection (priority order)
	gpuType = "CPU Node"
	if model, ok := n.Labels["nvidia.com/gpu.product"]; ok && model != "" {
		// 1. GFD label (most authoritative K8s source)
		gpuType = model
		gpuModel = model
	} else if model, ok := n.Annotations["worldland.io/gpu-model"]; ok && model != "" {
		// 2. Worldland NVML annotation (set by node worker bootstrap)
		gpuType = model
		gpuModel = model
	} else if model, ok := n.Labels["accelerator"]; ok && model != "" {
		// 3. Custom accelerator label
		gpuType = model
		gpuModel = model
	} else if count > 0 {
		// 4. GPU exists but model unknown
		gpuType = fmt.Sprintf("GPU x%d", count)
	}

	// VRAM: worldland annotation > GFD label > model lookup
	if memStr, ok := n.Annotations["worldland.io/gpu-vram-mb"]; ok {
		if mem, err := strconv.Atoi(memStr); err == nil && mem > 0 {
			vramMB = mem
		}
	}
	if vramMB == 0 && gpuModel != "" {
		vramMB = LookupVRAM(gpuModel)
	}
	if memStr, ok := n.Labels["nvidia.com/gpu.memory"]; ok {
		if mem, err := strconv.Atoi(memStr); err == nil && mem > 0 {
			vramMB = mem
		}
	}

	return count, gpuType, gpuModel, vramMB
}

func nodeSyncMemoryGB(n *corev1.Node) int {
	memQty := n.Status.Capacity.Memory()
	memGB := int(memQty.Value() / (1024 * 1024 * 1024))
	if memGB <= 0 {
		memGB = 1
	}
	return memGB
}

func nodeSyncCPUCores(n *corev1.Node) int {
	return int(n.Status.Capacity.Cpu().Value())
}

// nodeSyncMaxStorageGB returns the usable ephemeral-storage in GB for user containers.
// Subtracts realistic overhead: kubelet eviction threshold (15%) + container images (~15GB) + system.
func nodeSyncMaxStorageGB(n *corev1.Node) int {
	ephemeral := n.Status.Allocatable[corev1.ResourceEphemeralStorage]
	allocatableGB := int(ephemeral.Value() / (1024 * 1024 * 1024))
	if allocatableGB <= 0 {
		return 0
	}
	// Subtract realistic overhead:
	// - 15% eviction threshold (kubelet default imagefs.available < 15%)
	// - ~15GB for container images (pytorch ~10GB + system images ~3GB + buffer)
	evictionGB := allocatableGB * 15 / 100
	imageReserveGB := 15
	gb := allocatableGB - evictionGB - imageReserveGB
	if gb < 0 {
		gb = 0
	}
	return gb
}

// nodeSyncExternalIP extracts the external IP for SSH access.
// Priority: 1) annotation worldland.io/external-ip 2) K8s ExternalIP
func nodeSyncExternalIP(n *corev1.Node) string {
	if ip, ok := n.Annotations["worldland.io/external-ip"]; ok && ip != "" {
		return ip
	}
	for _, addr := range n.Status.Addresses {
		if addr.Type == corev1.NodeExternalIP {
			return addr.Address
		}
	}
	return ""
}
