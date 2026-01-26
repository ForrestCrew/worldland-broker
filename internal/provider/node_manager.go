// Package provider provides node management functionality for provider registration.
package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// NodeManager handles Kubernetes node operations for providers.
type NodeManager struct {
	clientset kubernetes.Interface
}

// NewNodeManager creates a new NodeManager.
func NewNodeManager(clientset kubernetes.Interface) *NodeManager {
	return &NodeManager{clientset: clientset}
}

// GetNode retrieves a node by name.
func (nm *NodeManager) GetNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	return nm.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
}

// NodeExists checks if a node exists.
func (nm *NodeManager) NodeExists(ctx context.Context, nodeName string) (bool, error) {
	_, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SetNodeLabels sets labels on a node.
func (nm *NodeManager) SetNodeLabels(ctx context.Context, nodeName string, labels map[string]string) error {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}

	for k, v := range labels {
		node.Labels[k] = v
	}

	_, err = nm.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node labels: %w", err)
	}

	return nil
}

// RemoveNodeLabels removes specific labels from a node.
func (nm *NodeManager) RemoveNodeLabels(ctx context.Context, nodeName string, labelKeys []string) error {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	for _, key := range labelKeys {
		delete(node.Labels, key)
	}

	_, err = nm.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node labels: %w", err)
	}

	return nil
}

// AddNodeTaint adds a taint to a node.
func (nm *NodeManager) AddNodeTaint(ctx context.Context, nodeName string, taint corev1.Taint) error {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	// Check if taint already exists
	for _, t := range node.Spec.Taints {
		if t.Key == taint.Key && t.Effect == taint.Effect {
			return nil // Taint already exists
		}
	}

	node.Spec.Taints = append(node.Spec.Taints, taint)

	_, err = nm.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to add taint: %w", err)
	}

	return nil
}

// RemoveNodeTaint removes a taint from a node.
func (nm *NodeManager) RemoveNodeTaint(ctx context.Context, nodeName, taintKey string, effect corev1.TaintEffect) error {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	var newTaints []corev1.Taint
	for _, t := range node.Spec.Taints {
		if t.Key == taintKey && t.Effect == effect {
			continue
		}
		newTaints = append(newTaints, t)
	}

	node.Spec.Taints = newTaints

	_, err = nm.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove taint: %w", err)
	}

	return nil
}

// SetNodeAnnotations sets annotations on a node.
func (nm *NodeManager) SetNodeAnnotations(ctx context.Context, nodeName string, annotations map[string]string) error {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}

	for k, v := range annotations {
		node.Annotations[k] = v
	}

	_, err = nm.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node annotations: %w", err)
	}

	return nil
}

// GetNodeAllocatableGPU returns the allocatable GPU count for a node.
func (nm *NodeManager) GetNodeAllocatableGPU(ctx context.Context, nodeName string) (int64, error) {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return 0, fmt.Errorf("failed to get node: %w", err)
	}

	gpuQuantity, ok := node.Status.Allocatable["nvidia.com/gpu"]
	if !ok {
		return 0, nil
	}

	return gpuQuantity.Value(), nil
}

// GetNodeAvailableGPU returns the actual available GPU count for a node.
// This calculates: allocatable GPUs - GPUs in use by running pods.
func (nm *NodeManager) GetNodeAvailableGPU(ctx context.Context, nodeName string) (int64, error) {
	// Get allocatable GPU count
	allocatable, err := nm.GetNodeAllocatableGPU(ctx, nodeName)
	if err != nil {
		return 0, err
	}

	// Get pods running on this node
	pods, err := nm.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase=Running", nodeName),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list pods on node: %w", err)
	}

	// Calculate used GPUs
	var usedGPUs int64 = 0
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			if gpuReq, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
				usedGPUs += gpuReq.Value()
			}
		}
	}

	available := allocatable - usedGPUs
	if available < 0 {
		available = 0
	}

	return available, nil
}

// GetNodeCapacity returns the full capacity of a node.
func (nm *NodeManager) GetNodeCapacity(ctx context.Context, nodeName string) (*NodeCapacityInfo, error) {
	node, err := nm.GetNode(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	capacity := &NodeCapacityInfo{}

	// CPU
	if cpu, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
		capacity.CPUCores = cpu.Value()
	}

	// Memory
	if mem, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
		capacity.MemoryBytes = mem.Value()
	}

	// GPU
	if gpu, ok := node.Status.Allocatable["nvidia.com/gpu"]; ok {
		capacity.GPUCount = gpu.Value()
	}

	// Storage
	if storage, ok := node.Status.Allocatable[corev1.ResourceEphemeralStorage]; ok {
		capacity.StorageBytes = storage.Value()
	}

	return capacity, nil
}

// NodeCapacityInfo holds node capacity information.
type NodeCapacityInfo struct {
	CPUCores     int64
	MemoryBytes  int64
	GPUCount     int64
	StorageBytes int64
}

// MarkNodeAsRentalAvailable marks a node as available for rental.
func (nm *NodeManager) MarkNodeAsRentalAvailable(ctx context.Context, nodeName string, providerID string, gpuModel string) error {
	// Set labels
	labels := map[string]string{
		NodeLabels.ProviderID:        providerID,
		NodeLabels.RentalType:        "gpu",
		NodeLabels.GPUModel:          strings.ToLower(strings.ReplaceAll(gpuModel, " ", "-")),
		NodeLabels.BlockchainNode:    "worldland",
		"worldland.io/rental-status": "available",
	}

	if err := nm.SetNodeLabels(ctx, nodeName, labels); err != nil {
		return err
	}

	// Add taint to prevent non-rental pods from scheduling
	taint := corev1.Taint{
		Key:    NodeTaints.DedicatedRental,
		Value:  "true",
		Effect: corev1.TaintEffectNoSchedule,
	}

	return nm.AddNodeTaint(ctx, nodeName, taint)
}

// MarkNodeAsRentalBusy marks a node as busy (fully utilized).
func (nm *NodeManager) MarkNodeAsRentalBusy(ctx context.Context, nodeName string) error {
	labels := map[string]string{
		"worldland.io/rental-status": "busy",
	}
	return nm.SetNodeLabels(ctx, nodeName, labels)
}

// MarkNodeGPUFull marks a node as having no available GPU.
func (nm *NodeManager) MarkNodeGPUFull(ctx context.Context, nodeName string) error {
	taint := corev1.Taint{
		Key:    NodeTaints.GPUFull,
		Value:  "true",
		Effect: corev1.TaintEffectNoSchedule,
	}
	return nm.AddNodeTaint(ctx, nodeName, taint)
}

// UnmarkNodeGPUFull removes the GPU full taint from a node.
func (nm *NodeManager) UnmarkNodeGPUFull(ctx context.Context, nodeName string) error {
	return nm.RemoveNodeTaint(ctx, nodeName, NodeTaints.GPUFull, corev1.TaintEffectNoSchedule)
}

// CheckAndUpdateGPUTaint checks GPU availability and updates taints accordingly.
func (nm *NodeManager) CheckAndUpdateGPUTaint(ctx context.Context, nodeName string) error {
	gpuCount, err := nm.GetNodeAllocatableGPU(ctx, nodeName)
	if err != nil {
		return err
	}

	if gpuCount == 0 {
		return nm.MarkNodeGPUFull(ctx, nodeName)
	}
	return nm.UnmarkNodeGPUFull(ctx, nodeName)
}

// ListProviderNodes lists all nodes that are registered as providers.
func (nm *NodeManager) ListProviderNodes(ctx context.Context) ([]corev1.Node, error) {
	nodes, err := nm.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s", NodeLabels.ProviderID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list provider nodes: %w", err)
	}

	return nodes.Items, nil
}

// GPUJobPodInfo contains extracted information from a GPU Job Pod.
type GPUJobPodInfo struct {
	JobID      string
	UserID     string
	ProviderID string
	Namespace  string
	NodeName   string
	Status     string
	GPUCount   int
	CPUCores   int
	MemoryMB   int64
	GPUModel   string
	ExpiresAt  time.Time // Job 만료 시간
}

// extractPodResourceInfo extracts resource information from a pod's container spec.
// This is a helper function to avoid code duplication.
func extractPodResourceInfo(pod *corev1.Pod) (gpuCount, cpuCores int, memoryMB int64) {
	if len(pod.Spec.Containers) == 0 {
		return 0, 0, 0
	}

	container := pod.Spec.Containers[0]

	// GPU count
	if gpuReq, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
		gpuCount = int(gpuReq.Value())
	}

	// CPU cores
	if cpuReq := container.Resources.Requests.Cpu(); cpuReq != nil && !cpuReq.IsZero() {
		cpuCores = int(cpuReq.Value())
	}

	// Memory in MB
	if memReq := container.Resources.Requests.Memory(); memReq != nil && !memReq.IsZero() {
		memoryMB = memReq.Value() / (1024 * 1024)
	}

	return
}

// parsePodToGPUJobInfo parses a Pod to GPUJobPodInfo struct.
func parsePodToGPUJobInfo(pod *corev1.Pod) GPUJobPodInfo {
	info := GPUJobPodInfo{
		JobID:      pod.Name,
		UserID:     pod.Labels["user-id"],
		ProviderID: pod.Labels["worldland.io/provider-id"],
		Namespace:  pod.Namespace,
		NodeName:   pod.Spec.NodeName,
		Status:     string(pod.Status.Phase),
		GPUModel:   pod.Annotations["worldland.io/gpu-model"],
	}

	// Parse expires-at annotation
	if expiresAtStr := pod.Annotations["worldland.io/expires-at"]; expiresAtStr != "" {
		if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
			info.ExpiresAt = t
		}
	}

	info.GPUCount, info.CPUCores, info.MemoryMB = extractPodResourceInfo(pod)
	return info
}

// ListGPUJobPods lists all GPU rental job pods across all namespaces.
func (nm *NodeManager) ListGPUJobPods(ctx context.Context) ([]GPUJobPodInfo, error) {
	// Query all pods with gpu-rental label
	pods, err := nm.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: "worldland.io/gpu-rental=true",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list GPU job pods: %w", err)
	}

	var result []GPUJobPodInfo
	for _, pod := range pods.Items {
		// Skip terminated pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		result = append(result, parsePodToGPUJobInfo(&pod))
	}

	return result, nil
}

// PodWatchEvent represents a pod watch event with parsed info.
type PodWatchEvent struct {
	Type    watch.EventType
	PodInfo GPUJobPodInfo
	RawPod  *corev1.Pod
}

// WatchGPUJobPods starts watching GPU job pods and returns events through a channel.
func (nm *NodeManager) WatchGPUJobPods(ctx context.Context) (<-chan PodWatchEvent, error) {
	watcher, err := nm.clientset.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{
		LabelSelector: "worldland.io/gpu-rental=true",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start pod watcher: %w", err)
	}

	eventCh := make(chan PodWatchEvent, 100)

	go func() {
		defer close(eventCh)
		defer watcher.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					// Watcher closed, need to restart
					return
				}

				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					continue
				}

				// Parse pod info using helper
				info := parsePodToGPUJobInfo(pod)

				eventCh <- PodWatchEvent{
					Type:    event.Type,
					PodInfo: info,
					RawPod:  pod,
				}
			}
		}
	}()

	return eventCh, nil
}

// DeleteGPUJob deletes a GPU job pod and its associated service.
func (nm *NodeManager) DeleteGPUJob(ctx context.Context, jobID, namespace string) error {
	// Delete the service first (ignore errors - it may not exist)
	serviceName := jobID + "-ssh"
	err := nm.clientset.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		// Log but don't fail - continue to delete pod
		// slog.Warn("Failed to delete service", "service", serviceName, "error", err)
	}

	// Delete the pod
	err = nm.clientset.CoreV1().Pods(namespace).Delete(ctx, jobID, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete pod %s: %w", jobID, err)
	}

	return nil
}

// ListAllGPUJobPods lists all GPU job pods including terminated ones.
// Used for expiration checking and cleanup.
func (nm *NodeManager) ListAllGPUJobPods(ctx context.Context) ([]GPUJobPodInfo, error) {
	pods, err := nm.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: "worldland.io/gpu-rental=true",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list GPU job pods: %w", err)
	}

	var result []GPUJobPodInfo
	for _, pod := range pods.Items {
		result = append(result, parsePodToGPUJobInfo(&pod))
	}

	return result, nil
}
