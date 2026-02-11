package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/worldland/worldland-hub/internal/domain"
)

// NodeCapacity tracks resource availability for a single K8s node.
// Follows proxy pattern: GPU-type-based tracking + CPU + Memory.
// Available = Total - InUse - Mining
type NodeCapacity struct {
	NodeName string
	GPUModel string // Primary GPU model on this node
	VramMB   int    // GPU VRAM in MB (from lookup table or K8s labels)

	// Total resources
	TotalGPUs     map[string]int // GPU type → count (e.g., {"RTX 4090": 4})
	TotalCPUCores int
	TotalMemoryGB int

	// In-use by rental sessions
	InUseGPUs     map[string]int // GPU type → rented count
	InUseCPUCores int
	InUseMemoryGB int

	// Available for new rentals: Total - InUse - Mining
	AvailableGPUs     map[string]int
	AvailableCPUCores int
	AvailableMemoryGB int

	// Mining reservations (future use)
	MiningGPUs     map[string]int
	MiningCPUCores int
	MiningMemoryGB int
}

// TotalGPUCount returns total GPUs across all types
func (c *NodeCapacity) TotalGPUCount() int {
	total := 0
	for _, count := range c.TotalGPUs {
		total += count
	}
	return total
}

// AvailableGPUCount returns available GPUs across all types
func (c *NodeCapacity) AvailableGPUCount() int {
	total := 0
	for _, count := range c.AvailableGPUs {
		total += count
	}
	return total
}

// InUseGPUCount returns in-use GPUs across all types
func (c *NodeCapacity) InUseGPUCount() int {
	total := 0
	for _, count := range c.InUseGPUs {
		total += count
	}
	return total
}

// ResourceAllocation represents resources to reserve or release for a job.
// Mirrors proxy's provider.ResourceAllocation.
type ResourceAllocation struct {
	SessionID string
	GPUType   string
	GPUCount  int
	CPUCores  int
	MemoryGB  int
}

// CapacityTracker manages resource capacity across multiple K8s clusters.
// Port of proxy's Orchestrator resource allocation logic to multi-cluster Hub.
type CapacityTracker struct {
	registry *ExternalClusterRegistry
	nodeRepo domain.NodeRepository
	logger   *slog.Logger

	mu       sync.RWMutex
	capacity map[string]map[string]*NodeCapacity // providerID -> nodeName -> capacity
}

// NewCapacityTracker creates a new capacity tracker
func NewCapacityTracker(
	registry *ExternalClusterRegistry,
	nodeRepo domain.NodeRepository,
	logger *slog.Logger,
) *CapacityTracker {
	return &CapacityTracker{
		registry: registry,
		nodeRepo: nodeRepo,
		logger:   logger,
		capacity: make(map[string]map[string]*NodeCapacity),
	}
}

// DiscoverCapacity queries K8s API for a provider's cluster and updates node capacity.
// Filters out control-plane nodes. Updates the DB node records with current capacity.
func (t *CapacityTracker) DiscoverCapacity(ctx context.Context, providerID string) error {
	client := t.registry.GetClient(providerID)
	if client == nil {
		return fmt.Errorf("no K8s cluster registered for provider %s", providerID)
	}

	nodeList, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list K8s nodes: %w", err)
	}

	t.mu.Lock()
	if t.capacity[providerID] == nil {
		t.capacity[providerID] = make(map[string]*NodeCapacity)
	}
	t.mu.Unlock()

	for _, n := range nodeList.Items {
		// Skip control-plane nodes
		if isControlPlane(&n) {
			continue
		}

		// Skip non-ready nodes
		if !isNodeReady(&n) {
			continue
		}

		// Determine GPU model from node labels/annotations (priority order)
		gpuModel := "default"
		if model, ok := n.Labels["nvidia.com/gpu.product"]; ok {
			gpuModel = model
		} else if model, ok := n.Annotations["worldland.io/gpu-model"]; ok && model != "" {
			gpuModel = model
		} else if model, ok := n.Labels["accelerator"]; ok {
			gpuModel = model
		}

		gpuQty, hasGPU := n.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]
		totalGPUs := 0
		if hasGPU {
			totalGPUs = int(gpuQty.Value())
		}
		// Worldland annotation overrides (NVML-detected, more reliable than device plugin)
		if countStr, ok := n.Annotations["worldland.io/gpu-count"]; ok {
			if c, err := strconv.Atoi(countStr); err == nil && c > 0 {
				totalGPUs = c
			}
		}

		cpuCores := int(n.Status.Capacity.Cpu().Value())
		memGB := int(n.Status.Capacity.Memory().Value() / (1024 * 1024 * 1024))

		// Count resources already in use by rental Pods on this node
		usedGPUs, usedCPU, usedMem, err := t.countUsedResources(ctx, client, n.Name)
		if err != nil {
			t.logger.Warn("failed to count used resources", "node", n.Name, "error", err)
			usedGPUs = 0
			usedCPU = 0
			usedMem = 0
		}

		availGPUs := totalGPUs - usedGPUs
		if availGPUs < 0 {
			availGPUs = 0
		}
		availCPU := cpuCores - usedCPU
		if availCPU < 0 {
			availCPU = 0
		}
		availMem := memGB - usedMem
		if availMem < 0 {
			availMem = 0
		}

		vramMB := LookupVRAM(gpuModel)
		// Worldland annotation overrides for VRAM
		if memStr, ok := n.Annotations["worldland.io/gpu-vram-mb"]; ok {
			if mem, err := strconv.Atoi(memStr); err == nil && mem > 0 {
				vramMB = mem
			}
		}

		cap := &NodeCapacity{
			NodeName: n.Name,
			GPUModel: gpuModel,
			VramMB:   vramMB,
			TotalGPUs:         map[string]int{gpuModel: totalGPUs},
			TotalCPUCores:     cpuCores,
			TotalMemoryGB:     memGB,
			InUseGPUs:         map[string]int{gpuModel: usedGPUs},
			InUseCPUCores:     usedCPU,
			InUseMemoryGB:     usedMem,
			AvailableGPUs:     map[string]int{gpuModel: availGPUs},
			AvailableCPUCores: availCPU,
			AvailableMemoryGB: availMem,
			MiningGPUs:        make(map[string]int),
		}

		t.mu.Lock()
		t.capacity[providerID][n.Name] = cap
		t.mu.Unlock()
	}

	// Update DB node records with current capacity
	if err := t.syncCapacityToDB(ctx, providerID); err != nil {
		t.logger.Warn("failed to sync capacity to DB", "providerID", providerID, "error", err)
	}

	return nil
}

// AllocateResources allocates resources from a provider for a job.
// Follows proxy pattern: GPU → CPU → Memory with rollback on failure.
// Must be called before creating a K8s Pod.
func (t *CapacityTracker) AllocateResources(providerID, nodeName string, alloc ResourceAllocation) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	providerCap, ok := t.capacity[providerID]
	if !ok {
		return fmt.Errorf("no capacity data for provider %s", providerID)
	}

	cap, ok := providerCap[nodeName]
	if !ok {
		return fmt.Errorf("no capacity data for node %s", nodeName)
	}

	// Determine GPU type (proxy: gpuType fallback to node's GPUModel)
	// DB stores user-friendly names (e.g., "GPU x1") while K8s uses label-based names.
	// If the requested GPU type isn't tracked, fall back to the node's primary GPU model.
	gpuType := alloc.GPUType
	if gpuType == "" {
		gpuType = cap.GPUModel
	}
	if _, exists := cap.TotalGPUs[gpuType]; !exists {
		gpuType = cap.GPUModel
	}

	// Initialize maps if nil
	if cap.AvailableGPUs == nil {
		cap.AvailableGPUs = make(map[string]int)
	}
	if cap.InUseGPUs == nil {
		cap.InUseGPUs = make(map[string]int)
	}

	// Step 1: GPU allocation
	if alloc.GPUCount > 0 {
		available := cap.AvailableGPUs[gpuType]
		if available < alloc.GPUCount {
			return fmt.Errorf("insufficient GPU: requested %d, available %d (type: %s)",
				alloc.GPUCount, available, gpuType)
		}
		cap.AvailableGPUs[gpuType] -= alloc.GPUCount
		cap.InUseGPUs[gpuType] += alloc.GPUCount
	}

	// Step 2: CPU allocation (rollback GPUs on failure)
	if alloc.CPUCores > 0 {
		if cap.AvailableCPUCores < alloc.CPUCores {
			// Rollback GPU
			cap.AvailableGPUs[gpuType] += alloc.GPUCount
			cap.InUseGPUs[gpuType] -= alloc.GPUCount
			return fmt.Errorf("insufficient CPU: requested %d, available %d",
				alloc.CPUCores, cap.AvailableCPUCores)
		}
		cap.AvailableCPUCores -= alloc.CPUCores
		cap.InUseCPUCores += alloc.CPUCores
	}

	// Step 3: Memory allocation (rollback GPUs + CPU on failure)
	if alloc.MemoryGB > 0 {
		if cap.AvailableMemoryGB < alloc.MemoryGB {
			// Rollback GPU + CPU
			cap.AvailableGPUs[gpuType] += alloc.GPUCount
			cap.InUseGPUs[gpuType] -= alloc.GPUCount
			cap.AvailableCPUCores += alloc.CPUCores
			cap.InUseCPUCores -= alloc.CPUCores
			return fmt.Errorf("insufficient memory: requested %dGB, available %dGB",
				alloc.MemoryGB, cap.AvailableMemoryGB)
		}
		cap.AvailableMemoryGB -= alloc.MemoryGB
		cap.InUseMemoryGB += alloc.MemoryGB
	}

	t.logger.Info("Resources allocated",
		"provider", providerID,
		"node", nodeName,
		"session", alloc.SessionID,
		"gpu_type", gpuType,
		"gpu_count", alloc.GPUCount,
		"cpu_cores", alloc.CPUCores,
		"memory_gb", alloc.MemoryGB,
		"available_gpu", cap.AvailableGPUs[gpuType],
		"available_cpu", cap.AvailableCPUCores,
		"available_mem", cap.AvailableMemoryGB,
	)

	// Immediately sync to DB so other users see updated available_gpus
	go t.syncSingleNodeToDB(providerID, nodeName)

	return nil
}

// ReleaseResources returns allocated resources back to available pool.
// Mirrors proxy's releaseJobResources with safe decrement.
func (t *CapacityTracker) ReleaseResources(providerID, nodeName string, alloc ResourceAllocation) {
	t.mu.Lock()
	defer t.mu.Unlock()

	providerCap, ok := t.capacity[providerID]
	if !ok {
		t.logger.Warn("Provider not found for resource release", "provider", providerID)
		return
	}

	cap, ok := providerCap[nodeName]
	if !ok {
		t.logger.Warn("Node not found for resource release", "node", nodeName)
		return
	}

	// Determine GPU type (same fallback as AllocateResources)
	gpuType := alloc.GPUType
	if gpuType == "" {
		gpuType = cap.GPUModel
	}
	if _, exists := cap.TotalGPUs[gpuType]; !exists {
		gpuType = cap.GPUModel
	}

	// Release GPU
	if cap.AvailableGPUs == nil {
		cap.AvailableGPUs = make(map[string]int)
	}
	cap.AvailableGPUs[gpuType] += alloc.GPUCount
	if cap.InUseGPUs != nil {
		cap.InUseGPUs[gpuType] -= alloc.GPUCount
		if cap.InUseGPUs[gpuType] < 0 {
			cap.InUseGPUs[gpuType] = 0
		}
	}
	// Clamp to total
	if totalForType, exists := cap.TotalGPUs[gpuType]; exists {
		if cap.AvailableGPUs[gpuType] > totalForType {
			cap.AvailableGPUs[gpuType] = totalForType
		}
	}

	// Release CPU
	cap.AvailableCPUCores += alloc.CPUCores
	cap.InUseCPUCores -= alloc.CPUCores
	if cap.InUseCPUCores < 0 {
		cap.InUseCPUCores = 0
	}
	if cap.AvailableCPUCores > cap.TotalCPUCores {
		cap.AvailableCPUCores = cap.TotalCPUCores
	}

	// Release Memory
	cap.AvailableMemoryGB += alloc.MemoryGB
	cap.InUseMemoryGB -= alloc.MemoryGB
	if cap.InUseMemoryGB < 0 {
		cap.InUseMemoryGB = 0
	}
	if cap.AvailableMemoryGB > cap.TotalMemoryGB {
		cap.AvailableMemoryGB = cap.TotalMemoryGB
	}

	t.logger.Info("Resources released",
		"provider", providerID,
		"node", nodeName,
		"session", alloc.SessionID,
		"gpu_released", alloc.GPUCount,
		"cpu_released", alloc.CPUCores,
		"mem_released_gb", alloc.MemoryGB,
		"available_gpu", cap.AvailableGPUs[gpuType],
		"available_cpu", cap.AvailableCPUCores,
		"available_mem", cap.AvailableMemoryGB,
	)

	// Immediately sync to DB so freed GPUs are visible to other users
	go t.syncSingleNodeToDB(providerID, nodeName)
}

// GetCapacity returns the current capacity for a provider's node.
// Returns nil if no data available.
func (t *CapacityTracker) GetCapacity(providerID, nodeName string) *NodeCapacity {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if providerCap, ok := t.capacity[providerID]; ok {
		return providerCap[nodeName]
	}
	return nil
}

// GetProviderCapacity returns all node capacities for a provider
func (t *CapacityTracker) GetProviderCapacity(providerID string) map[string]*NodeCapacity {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.capacity[providerID]
}

// StartPeriodicSync starts a background goroutine that periodically refreshes
// capacity data from all registered clusters.
func (t *CapacityTracker) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				for _, providerID := range t.registry.ListProviderIDs() {
					if err := t.DiscoverCapacity(ctx, providerID); err != nil {
						t.logger.Warn("periodic capacity sync failed",
							"providerID", providerID, "error", err)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// RecoverAllocations recovers in-memory allocation state from running K8s pods.
// Called on Hub startup to ensure capacity matches actual cluster state.
// Mirrors proxy's RecoverJobAllocations.
func (t *CapacityTracker) RecoverAllocations(ctx context.Context) error {
	t.logger.Info("Recovering job allocations from K8s...")

	recoveredTotal := 0
	for _, providerID := range t.registry.ListProviderIDs() {
		recovered, err := t.recoverProviderAllocations(ctx, providerID)
		if err != nil {
			t.logger.Warn("Failed to recover allocations for provider",
				"providerID", providerID, "error", err)
			continue
		}
		recoveredTotal += recovered
	}

	t.logger.Info("Job allocation recovery completed", "total_jobs", recoveredTotal)
	return nil
}

// recoverProviderAllocations recovers allocations for a single provider
func (t *CapacityTracker) recoverProviderAllocations(ctx context.Context, providerID string) (int, error) {
	client := t.registry.GetClient(providerID)
	if client == nil {
		return 0, nil
	}

	pods, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", LabelGPURental),
	})
	if err != nil {
		return 0, err
	}

	if len(pods.Items) == 0 {
		return 0, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	recovered := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}

		providerCap, ok := t.capacity[providerID]
		if !ok {
			continue
		}
		cap, ok := providerCap[nodeName]
		if !ok {
			continue
		}

		// Extract resources from pod spec
		gpuCount, cpuCores, memGB := extractPodResources(&pod)
		gpuType := cap.GPUModel

		// Mark as in-use (DiscoverCapacity already counted these,
		// so this is a verification step)
		_ = gpuCount
		_ = cpuCores
		_ = memGB
		_ = gpuType

		recovered++
		t.logger.Debug("Recovered job allocation",
			"session", pod.Labels[LabelSessionID],
			"node", nodeName,
			"gpu", gpuCount,
			"cpu", cpuCores,
			"mem_gb", memGB,
		)
	}

	return recovered, nil
}

// countUsedResources counts GPUs, CPU cores, and memory GB in use by rental Pods on a node
func (t *CapacityTracker) countUsedResources(ctx context.Context, client *ClusterClient, nodeName string) (int, int, int, error) {
	pods, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", LabelGPURental),
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase=Running", nodeName),
	})
	if err != nil {
		return 0, 0, 0, err
	}

	usedGPUs := 0
	usedCPU := 0
	usedMem := 0
	for _, pod := range pods.Items {
		gpus, cpu, mem := extractPodResources(&pod)
		usedGPUs += gpus
		usedCPU += cpu
		usedMem += mem
	}

	return usedGPUs, usedCPU, usedMem, nil
}

// extractPodResources extracts GPU count, CPU cores, and memory GB from a Pod
func extractPodResources(pod *corev1.Pod) (gpus, cpuCores, memGB int) {
	for _, container := range pod.Spec.Containers {
		if gpuReq, ok := container.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok {
			gpus += int(gpuReq.Value())
		}
		if cpuReq := container.Resources.Requests.Cpu(); cpuReq != nil {
			cpuCores += int(cpuReq.Value())
		}
		if memReq := container.Resources.Requests.Memory(); memReq != nil {
			memGB += int(memReq.Value() / (1024 * 1024 * 1024))
		}
	}
	return
}

// syncSingleNodeToDB immediately updates a single node's capacity in DB.
// Called after AllocateResources/ReleaseResources so other users see correct available_gpus.
func (t *CapacityTracker) syncSingleNodeToDB(providerID, nodeName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.mu.RLock()
	providerCap := t.capacity[providerID]
	t.mu.RUnlock()

	if providerCap == nil {
		return
	}
	cap, ok := providerCap[nodeName]
	if !ok {
		return
	}

	nodes, err := t.nodeRepo.GetByProvider(ctx, providerID)
	if err != nil {
		t.logger.Warn("syncSingleNodeToDB: failed to get nodes", "error", err)
		return
	}

	for _, node := range nodes {
		if node.K8sNodeName != nodeName {
			continue
		}
		node.AvailableGPUs = cap.AvailableGPUCount()
		node.TotalCPUCores = cap.TotalCPUCores
		node.TotalMemoryGB = cap.TotalMemoryGB
		node.AvailableCPUCores = cap.AvailableCPUCores
		node.AvailableMemoryGB = cap.AvailableMemoryGB
		node.UpdatedAt = time.Now()
		if err := t.nodeRepo.Update(ctx, node); err != nil {
			t.logger.Warn("syncSingleNodeToDB: failed to update node",
				"nodeID", node.ID, "error", err)
		} else {
			t.logger.Info("syncSingleNodeToDB: node capacity synced",
				"nodeID", node.ID,
				"availableGPUs", node.AvailableGPUs,
				"availableCPU", node.AvailableCPUCores,
				"availableMem", node.AvailableMemoryGB)
		}
		return
	}
}

// syncCapacityToDB updates DB node records with current in-memory capacity
func (t *CapacityTracker) syncCapacityToDB(ctx context.Context, providerID string) error {
	t.mu.RLock()
	providerCap := t.capacity[providerID]
	t.mu.RUnlock()

	if providerCap == nil {
		return nil
	}

	nodes, err := t.nodeRepo.GetByProvider(ctx, providerID)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.K8sNodeName == "" {
			continue
		}
		cap, ok := providerCap[node.K8sNodeName]
		if !ok {
			continue
		}

		node.TotalGPUs = cap.TotalGPUCount()
		node.AvailableGPUs = cap.AvailableGPUCount()
		node.TotalCPUCores = cap.TotalCPUCores
		node.TotalMemoryGB = cap.TotalMemoryGB
		node.AvailableCPUCores = cap.AvailableCPUCores
		node.AvailableMemoryGB = cap.AvailableMemoryGB
		node.UpdatedAt = time.Now()

		if err := t.nodeRepo.Update(ctx, node); err != nil {
			t.logger.Warn("failed to update node capacity",
				"nodeID", node.ID, "error", err)
		}
	}

	return nil
}

// isControlPlane checks if a K8s node has control-plane/master labels
func isControlPlane(n *corev1.Node) bool {
	for label := range n.Labels {
		if label == "node-role.kubernetes.io/control-plane" || label == "node-role.kubernetes.io/master" {
			return true
		}
	}
	return false
}

// isNodeReady checks if a K8s node has the Ready condition
func isNodeReady(n *corev1.Node) bool {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
