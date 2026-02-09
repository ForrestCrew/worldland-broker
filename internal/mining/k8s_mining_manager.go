package mining

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
)

const (
	// MiningNamespace is the namespace for mining pods
	MiningNamespace = "worldland-mining"
	// MiningImage is the default mining container image
	MiningImage = "mingeyom/worldland-mio:latest"
	// LabelMining identifies mining pods
	LabelMining = "worldland.io/mining"
	// LabelMiningProvider identifies the provider for a mining pod
	LabelMiningProvider = "worldland.io/mining-provider"
)

// MiningConfig holds configuration for a mining pod
type MiningConfig struct {
	GPUCount int    `json:"gpuCount"`
	Image    string `json:"image,omitempty"` // Defaults to MiningImage
}

// MiningStatus represents the current state of mining for a provider
type MiningStatus struct {
	Active   bool   `json:"active"`
	PodName  string `json:"podName,omitempty"`
	Phase    string `json:"phase,omitempty"`
	GPUCount int    `json:"gpuCount"`
}

// K8sMiningManager manages mining pod lifecycle on provider K8s clusters
type K8sMiningManager struct {
	clusterRegistry *k8s.ExternalClusterRegistry
	providerRepo    domain.ProviderRepository
	gpuPool         *GPUPool
	logger          *slog.Logger
}

// NewK8sMiningManager creates a new mining manager
func NewK8sMiningManager(
	clusterRegistry *k8s.ExternalClusterRegistry,
	providerRepo domain.ProviderRepository,
	logger *slog.Logger,
) *K8sMiningManager {
	return &K8sMiningManager{
		clusterRegistry: clusterRegistry,
		providerRepo:    providerRepo,
		logger:          logger,
	}
}

// WithGPUPool sets the GPU pool for auto-discovery
func (m *K8sMiningManager) WithGPUPool(pool *GPUPool) *K8sMiningManager {
	m.gpuPool = pool
	return m
}

// DiscoverGPUs queries K8s nodes for nvidia.com/gpu capacity and populates the GPUPool.
// Called on startup for all registered K8s providers.
func (m *K8sMiningManager) DiscoverGPUs(ctx context.Context) {
	if m.gpuPool == nil {
		return
	}

	for _, providerID := range m.clusterRegistry.ListProviderIDs() {
		gpuCount, err := m.discoverProviderGPUs(ctx, providerID)
		if err != nil {
			m.logger.Warn("failed to discover GPUs for provider",
				"providerID", providerID,
				"error", err,
			)
			continue
		}
		if gpuCount > 0 {
			m.gpuPool.SetTotal(providerID, gpuCount)
			m.logger.Info("discovered GPUs for provider",
				"providerID", providerID,
				"gpuCount", gpuCount,
			)
		}
	}
}

// discoverProviderGPUs queries K8s nodes and counts total nvidia.com/gpu capacity
func (m *K8sMiningManager) discoverProviderGPUs(ctx context.Context, providerID string) (int, error) {
	client := m.clusterRegistry.GetClient(providerID)
	if client == nil {
		return 0, fmt.Errorf("no cluster registered")
	}

	nodes, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list nodes: %w", err)
	}

	totalGPUs := 0
	for _, node := range nodes.Items {
		if gpuRes, ok := node.Status.Capacity["nvidia.com/gpu"]; ok {
			totalGPUs += int(gpuRes.Value())
		}
	}

	return totalGPUs, nil
}

// DeployMiningPod deploys a mining pod on the provider's K8s cluster
func (m *K8sMiningManager) DeployMiningPod(ctx context.Context, providerID string, config MiningConfig) error {
	client := m.clusterRegistry.GetClient(providerID)
	if client == nil {
		return fmt.Errorf("no K8s cluster registered for provider %s", providerID)
	}

	image := config.Image
	if image == "" {
		image = MiningImage
	}

	gpuCount := config.GPUCount
	if gpuCount <= 0 {
		gpuCount = 1
	}

	podName := fmt.Sprintf("mining-%s", providerID[:8])

	// Ensure mining namespace exists
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: MiningNamespace,
		},
	}
	_, err := client.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create mining namespace: %w", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: MiningNamespace,
			Labels: map[string]string{
				LabelMining:         "true",
				LabelMiningProvider: providerID,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			HostNetwork:   true,
			Tolerations: []corev1.Toleration{{
				Key:      "nvidia.com/gpu",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			Containers: []corev1.Container{{
				Name:  "worldland-mio",
				Image: image,
				Args: []string{
					"--mio",
					"--datadir", "/worldland/data",
					"--syncmode", "full",
					"--http",
				},
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"nvidia.com/gpu": resource.MustParse(fmt.Sprintf("%d", gpuCount)),
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "data",
					MountPath: "/worldland/data",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}},
		},
	}

	_, err = client.Clientset.CoreV1().Pods(MiningNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			m.logger.Info("mining pod already exists", "providerID", providerID)
			return nil
		}
		return fmt.Errorf("failed to create mining pod: %w", err)
	}

	m.logger.Info("deployed mining pod",
		"providerID", providerID,
		"podName", podName,
		"gpuCount", gpuCount,
		"image", image,
	)

	return nil
}

// DeleteMiningPod deletes the mining pod from the provider's K8s cluster
func (m *K8sMiningManager) DeleteMiningPod(ctx context.Context, providerID string) error {
	client := m.clusterRegistry.GetClient(providerID)
	if client == nil {
		return fmt.Errorf("no K8s cluster registered for provider %s", providerID)
	}

	podName := fmt.Sprintf("mining-%s", providerID[:8])

	err := client.Clientset.CoreV1().Pods(MiningNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete mining pod: %w", err)
	}

	m.logger.Info("deleted mining pod",
		"providerID", providerID,
		"podName", podName,
	)

	return nil
}

// GetMiningStatus returns the current mining status for a provider
func (m *K8sMiningManager) GetMiningStatus(ctx context.Context, providerID string) (*MiningStatus, error) {
	client := m.clusterRegistry.GetClient(providerID)
	if client == nil {
		return &MiningStatus{Active: false}, nil
	}

	podName := fmt.Sprintf("mining-%s", providerID[:8])

	pod, err := client.Clientset.CoreV1().Pods(MiningNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &MiningStatus{Active: false}, nil
		}
		return nil, fmt.Errorf("failed to get mining pod: %w", err)
	}

	gpuCount := 0
	if len(pod.Spec.Containers) > 0 {
		if gpuRes, ok := pod.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"]; ok {
			gpuCount = int(gpuRes.Value())
		}
	}

	return &MiningStatus{
		Active:   pod.Status.Phase == corev1.PodRunning,
		PodName:  podName,
		Phase:    string(pod.Status.Phase),
		GPUCount: gpuCount,
	}, nil
}
