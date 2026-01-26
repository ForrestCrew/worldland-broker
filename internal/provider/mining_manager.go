// Package provider provides mining pod management for Worldland blockchain nodes.
package provider

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// MiningNamespace is the namespace for all mining pods
	MiningNamespace = "worldland-mining"

	// Labels
	LabelApp        = "app"
	LabelProviderID = "provider-id"
	LabelMining     = "worldland.io/mining"
)

// MiningPodManager handles Worldland mining pod lifecycle.
type MiningPodManager struct {
	clientset kubernetes.Interface
}

// NewMiningPodManager creates a new MiningPodManager.
func NewMiningPodManager(clientset kubernetes.Interface) *MiningPodManager {
	return &MiningPodManager{clientset: clientset}
}

// EnsureMiningNamespace creates the mining namespace if it doesn't exist.
func (m *MiningPodManager) EnsureMiningNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: MiningNamespace,
			Labels: map[string]string{
				"name":       MiningNamespace,
				"managed-by": "k8s-proxy-server",
			},
		},
	}

	_, err := m.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create mining namespace: %w", err)
	}
	return nil
}

// DeployMiningPod deploys a Worldland mining pod for a provider.
func (m *MiningPodManager) DeployMiningPod(ctx context.Context, providerID string, config *MiningConfig, nodeName string) (string, error) {
	podName := fmt.Sprintf("mining-%s", providerID)

	// Ensure namespace exists
	if err := m.EnsureMiningNamespace(ctx); err != nil {
		return "", err
	}

	// Build pod spec
	pod := m.buildMiningPod(podName, providerID, config, nodeName)

	// Create pod
	_, err := m.clientset.CoreV1().Pods(MiningNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return podName, nil // Already exists, return success
		}
		return "", fmt.Errorf("failed to create mining pod: %w", err)
	}

	return podName, nil
}

// UpdateMiningPodGPU updates the GPU allocation of an existing mining pod.
// Note: This requires deleting and recreating the pod since GPU limits can't be changed in-place.
func (m *MiningPodManager) UpdateMiningPodGPU(ctx context.Context, providerID string, config *MiningConfig, nodeName string) error {
	podName := fmt.Sprintf("mining-%s", providerID)

	// Delete existing pod
	err := m.clientset.CoreV1().Pods(MiningNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete existing mining pod: %w", err)
	}

	// Wait for pod deletion (simple approach - in production use watch)
	// For now, we'll just try to create and let K8s handle it

	// Create new pod with updated GPU count
	pod := m.buildMiningPod(podName, providerID, config, nodeName)
	_, err = m.clientset.CoreV1().Pods(MiningNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create updated mining pod: %w", err)
	}

	return nil
}

// DeleteMiningPod deletes the mining pod for a provider.
func (m *MiningPodManager) DeleteMiningPod(ctx context.Context, providerID string) error {
	podName := fmt.Sprintf("mining-%s", providerID)

	err := m.clientset.CoreV1().Pods(MiningNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete mining pod: %w", err)
	}
	return nil
}

// GetMiningPodStatus returns the status of a mining pod.
func (m *MiningPodManager) GetMiningPodStatus(ctx context.Context, providerID string) (string, error) {
	podName := fmt.Sprintf("mining-%s", providerID)

	pod, err := m.clientset.CoreV1().Pods(MiningNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return "stopped", nil
		}
		return "", fmt.Errorf("failed to get mining pod: %w", err)
	}

	switch pod.Status.Phase {
	case corev1.PodRunning:
		return "running", nil
	case corev1.PodPending:
		return "pending", nil
	case corev1.PodFailed:
		return "failed", nil
	case corev1.PodSucceeded:
		return "stopped", nil
	default:
		return "unknown", nil
	}
}

// buildMiningPod creates a pod spec for mining.
func (m *MiningPodManager) buildMiningPod(podName, providerID string, config *MiningConfig, nodeName string) *corev1.Pod {
	// Default image if not provided
	image := config.Image
	if image == "" {
		image = "mingeyom/worldland-mio:latest"
	}

	// Resource limits
	resources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", config.CPUCores)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", config.MemoryMB)),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", config.CPUCores/2+1)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", config.MemoryMB/2)),
		},
	}

	// Add GPU resources if requested
	if config.GPUCount > 0 {
		gpuQuantity := resource.MustParse(fmt.Sprintf("%d", config.GPUCount))
		resources.Limits["nvidia.com/gpu"] = gpuQuantity
		resources.Requests["nvidia.com/gpu"] = gpuQuantity
	}

	// Build container args for Worldland node
	// Equivalent to: --mio --datadir /worldland/data --syncmode full --http ...
	nodeArgs := []string{
		"--mio",
		"--datadir", "/worldland/data",
		"--syncmode", "full",
		"--http",
		"--http.addr", "0.0.0.0",
		"--http.api", "eth,net,web3,personal,admin,miner",
		"--http.corsdomain", "*",
	}

	// Add NAT configuration if public IP is available
	if config.PublicIP != "" {
		nodeArgs = append(nodeArgs, "--nat", fmt.Sprintf("extip:%s", config.PublicIP))
	}

	// Append any extra args from config
	nodeArgs = append(nodeArgs, config.ExtraArgs...)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: MiningNamespace,
			Labels: map[string]string{
				LabelApp:        "worldland-mining",
				LabelProviderID: providerID,
				LabelMining:     "true",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			HostNetwork:   true, // Use host network for P2P connectivity
			Containers: []corev1.Container{
				{
					Name:      "worldland-node",
					Image:     image,
					Resources: resources,
					Args:      nodeArgs,
					Ports: []corev1.ContainerPort{
						{Name: "p2p-tcp", ContainerPort: 30303, Protocol: corev1.ProtocolTCP},
						{Name: "p2p-udp", ContainerPort: 30303, Protocol: corev1.ProtocolUDP},
						{Name: "http-rpc", ContainerPort: 8545, Protocol: corev1.ProtocolTCP},
						{Name: "ws-rpc", ContainerPort: 8546, Protocol: corev1.ProtocolTCP},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "blockchain-data",
							MountPath: "/worldland/data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "blockchain-data",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: fmt.Sprintf("/data/worldland/%s", providerID),
							Type: func() *corev1.HostPathType {
								t := corev1.HostPathDirectoryOrCreate
								return &t
							}(),
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "nvidia.com/gpu",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			},
		},
	}

	// Add node selector if node name is specified
	if nodeName != "" {
		pod.Spec.NodeSelector = map[string]string{
			"kubernetes.io/hostname": nodeName,
		}
	}

	return pod
}

// MiningPodExists checks if a mining pod exists for a provider.
func (m *MiningPodManager) MiningPodExists(ctx context.Context, providerID string) (bool, error) {
	podName := fmt.Sprintf("mining-%s", providerID)

	_, err := m.clientset.CoreV1().Pods(MiningNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
