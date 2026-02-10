package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// sanitizeLabel converts a string to a valid K8s label value.
// K8s labels: max 63 chars, alphanumeric or -_.
func sanitizeLabel(s string) string {
	if s == "" {
		return "unknown"
	}
	var b []byte
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b = append(b, c)
		} else {
			b = append(b, '-')
		}
	}
	if len(b) > 63 {
		b = b[:63]
	}
	return string(b)
}

// buildNodeSelector creates a NodeSelector based on job spec.
// Mirrors proxy's buildNodeSelector: specific hostname or any GPU rental node.
func buildNodeSelector(spec GPUJobSpec) map[string]string {
	if spec.NodeHostname != "" {
		return map[string]string{
			"kubernetes.io/hostname": spec.NodeHostname,
		}
	}
	// Default: schedule on any node labeled for GPU rental
	return map[string]string{
		LabelRentalType: "gpu",
	}
}

// extractHostIP extracts a plain IP from a URL like "https://34.64.255.101:6443"
func extractHostIP(hostOrURL string) string {
	if u, err := url.Parse(hostOrURL); err == nil && u.Host != "" {
		host, _, err := net.SplitHostPort(u.Host)
		if err == nil {
			return host
		}
		return u.Host
	}
	return hostOrURL
}

// JobManager manages GPU session Pod lifecycle
type JobManager struct {
	clientset    kubernetes.Interface
	logger       *slog.Logger
	externalHost string // External host/IP for SSH access (overrides node IP if set)
}

// NewJobManager creates a new JobManager
func NewJobManager(clientset kubernetes.Interface, logger *slog.Logger) *JobManager {
	return &JobManager{
		clientset: clientset,
		logger:    logger,
	}
}

// WithExternalHost sets the external host for SSH connections
func (m *JobManager) WithExternalHost(host string) *JobManager {
	m.externalHost = host
	return m
}

// buildResourceRequirements creates K8s ResourceRequirements from GPUJobSpec.
// CPU/Memory: requests < limits (Burstable QoS) to allow scheduling on nodes
// where system pods (kube-proxy, flannel, nvidia-plugin) consume some resources.
// GPU: requests == limits (nvidia device plugin requirement).
func buildResourceRequirements(spec GPUJobSpec) corev1.ResourceRequirements {
	cpuStr := strconv.Itoa(spec.CPUCores)
	memStr := strconv.Itoa(spec.MemoryGB) + "Gi"

	// Reserve 500m CPU for system pods (kube-proxy, flannel, nvidia-plugin)
	cpuRequestMillis := spec.CPUCores*1000 - 500
	if cpuRequestMillis < 500 {
		cpuRequestMillis = 500
	}
	cpuRequestStr := strconv.Itoa(cpuRequestMillis) + "m"

	// Reserve 512Mi memory for system pods
	memRequestMB := spec.MemoryGB*1024 - 512
	if memRequestMB < 512 {
		memRequestMB = 512
	}
	memRequestStr := strconv.Itoa(memRequestMB) + "Mi"

	reqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuRequestStr),
			corev1.ResourceMemory: resource.MustParse(memRequestStr),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuStr),
			corev1.ResourceMemory: resource.MustParse(memStr),
		},
	}

	if spec.StorageGB > 0 {
		storageStr := strconv.Itoa(spec.StorageGB) + "Gi"
		reqs.Requests[corev1.ResourceEphemeralStorage] = resource.MustParse(storageStr)
		reqs.Limits[corev1.ResourceEphemeralStorage] = resource.MustParse(storageStr)
	}

	return reqs
}

// CreateGPUSession creates a GPU Pod with SSH password injection
// Returns the generated SSH password for the caller to store
// Idempotent: if Pod already exists, returns existing SSH password
func (m *JobManager) CreateGPUSession(ctx context.Context, spec GPUJobSpec) (password string, err error) {
	namespace := TenantNamespace(spec.UserAddress)
	podName := PodName(spec.SessionID)

	// Idempotency: check if Pod already exists (e.g. worker restart)
	existingPod, _ := m.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if existingPod != nil && existingPod.Name != "" && existingPod.DeletionTimestamp == nil {
		m.logger.Info("pod already exists, returning existing SSH password",
			"sessionId", spec.SessionID,
			"podName", podName,
		)
		existingPwd, _ := GetSSHPassword(ctx, m.clientset, namespace, spec.SessionID)
		return existingPwd, nil
	}

	// Generate SSH password
	password = GenerateSSHPassword()

	// Create SSH Secret first
	if err := createSSHSecret(ctx, m.clientset, namespace, spec.SessionID, password); err != nil {
		return "", fmt.Errorf("failed to create SSH secret: %w", err)
	}

	// Create Pod with SSH password injected from Secret
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				LabelSessionID:  spec.SessionID,
				LabelProviderID: spec.ProviderID,
				LabelGPURental:  "true",
				LabelGPUModel:   sanitizeLabel(spec.GPUModel),
			},
			Annotations: map[string]string{
				AnnotationExpiresAt:   spec.ExpiresAt.Format(time.RFC3339),
				AnnotationUserAddress: spec.UserAddress,
				AnnotationGPUModel:    spec.GPUModel,
				AnnotationPricePerHr:  fmt.Sprintf("%.2f", spec.PricePerHour),
				AnnotationStorageGB:   fmt.Sprintf("%d", spec.StorageGB),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			// NodeSelector: schedule on specific node or any GPU rental node
			// Mirrors proxy's buildNodeSelector pattern
			NodeSelector: buildNodeSelector(spec),
			Tolerations: []corev1.Toleration{{
				Key:      TaintDedicatedRental,
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			Containers: []corev1.Container{{
				Name:  "gpu-workload",
				Image: spec.Image,
				Command: []string{"/bin/bash", "-c"},
				Args: []string{`
export DEBIAN_FRONTEND=noninteractive
apt-get update && apt-get install -y --no-install-recommends openssh-server
mkdir -p /run/sshd
echo "root:$SSH_PASSWORD" | chpasswd
sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config
sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/' /etc/ssh/sshd_config

# Add conda/python to PATH for PyTorch/TensorFlow images
if [ -d "/opt/conda/bin" ]; then
  echo 'export PATH="/opt/conda/bin:$PATH"' >> /root/.bashrc
  echo 'source /opt/conda/etc/profile.d/conda.sh 2>/dev/null || true' >> /root/.bashrc
fi

echo 'export CUDA_HOME="/usr/local/cuda"' >> /root/.bashrc
echo "SSH server starting..."
exec /usr/sbin/sshd -D -e
`},
				Env: []corev1.EnvVar{{
					Name: "SSH_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: SSHSecretName(spec.SessionID),
							},
							Key: "password",
						},
					},
				}},
				Resources: buildResourceRequirements(spec),
				// SecurityContext: SYS_ADMIN for CUDA/GPU driver access
				// Mirrors proxy: job/manager.go:281-285
				SecurityContext: &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{"SYS_ADMIN"},
					},
				},
				Ports: []corev1.ContainerPort{{
					Name:          "ssh",
					ContainerPort: 22,
					Protocol:      corev1.ProtocolTCP,
				}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(22),
						},
					},
					InitialDelaySeconds: 5,
					PeriodSeconds:       10,
					TimeoutSeconds:      1,
					FailureThreshold:    3,
				},
			}},
		},
	}

	// Add GPU resource request if spec.GPUCount > 0
	if spec.GPUCount > 0 {
		pod.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"] = resource.MustParse(strconv.Itoa(spec.GPUCount))
		pod.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"] = resource.MustParse(strconv.Itoa(spec.GPUCount))
	}

	// Create Pod
	_, err = m.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create pod: %w", err)
	}

	m.logger.Info("created GPU session pod",
		"sessionId", spec.SessionID,
		"namespace", namespace,
		"podName", podName,
		"providerId", spec.ProviderID,
		"gpuCount", spec.GPUCount,
	)

	// Create SSH NodePort Service
	if err := m.createSSHService(ctx, namespace, spec.SessionID); err != nil {
		return "", fmt.Errorf("failed to create SSH service: %w", err)
	}

	return password, nil
}

// createSSHService creates a NodePort Service for SSH access
func (m *JobManager) createSSHService(ctx context.Context, namespace, sessionID string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SSHServiceName(sessionID),
			Namespace: namespace,
			Labels: map[string]string{
				LabelSessionID: sessionID,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{
				LabelSessionID: sessionID,
			},
			Ports: []corev1.ServicePort{{
				Name:       "ssh",
				Protocol:   corev1.ProtocolTCP,
				Port:       22,
				TargetPort: intstr.FromInt(22),
			}},
			ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster,
		},
	}

	_, err := m.clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	m.logger.Info("created SSH service",
		"sessionId", sessionID,
		"namespace", namespace,
		"serviceName", SSHServiceName(sessionID),
	)

	return nil
}

// DeleteGPUSession deletes a GPU session (Pod, Service, and Secret) with graceful termination
// Idempotent - returns nil if resources don't exist
func (m *JobManager) DeleteGPUSession(ctx context.Context, userAddress, sessionID string) error {
	namespace := TenantNamespace(userAddress)
	podName := PodName(sessionID)

	// Delete Pod with grace period
	gracePeriod := int64(30)
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOpts := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	}

	err := m.clientset.CoreV1().Pods(namespace).Delete(ctx, podName, deleteOpts)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	// Delete SSH Service (ignore NotFound)
	err = m.clientset.CoreV1().Services(namespace).Delete(ctx, SSHServiceName(sessionID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		m.logger.Warn("failed to delete SSH service", "sessionId", sessionID, "error", err)
		// Continue - not fatal
	}

	// Delete SSH Secret (ignore NotFound)
	if err := deleteSSHSecret(ctx, m.clientset, namespace, sessionID); err != nil {
		m.logger.Warn("failed to delete SSH secret", "sessionId", sessionID, "error", err)
		// Continue - not fatal
	}

	m.logger.Info("deleted GPU session",
		"sessionId", sessionID,
		"namespace", namespace,
		"podName", podName,
	)

	return nil
}

// DeleteGPUSessionImmediate deletes a GPU session immediately (grace period = 0)
// For force termination scenarios
func (m *JobManager) DeleteGPUSessionImmediate(ctx context.Context, userAddress, sessionID string) error {
	namespace := TenantNamespace(userAddress)
	podName := PodName(sessionID)

	// Delete Pod with zero grace period
	gracePeriod := int64(0)
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOpts := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	}

	err := m.clientset.CoreV1().Pods(namespace).Delete(ctx, podName, deleteOpts)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	// Delete SSH Service (ignore NotFound)
	err = m.clientset.CoreV1().Services(namespace).Delete(ctx, SSHServiceName(sessionID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		m.logger.Warn("failed to delete SSH service", "sessionId", sessionID, "error", err)
		// Continue - not fatal
	}

	// Delete SSH Secret (ignore NotFound)
	if err := deleteSSHSecret(ctx, m.clientset, namespace, sessionID); err != nil {
		m.logger.Warn("failed to delete SSH secret", "sessionId", sessionID, "error", err)
		// Continue - not fatal
	}

	m.logger.Info("deleted GPU session immediately",
		"sessionId", sessionID,
		"namespace", namespace,
		"podName", podName,
	)

	return nil
}

// GetSSHConnectionInfo retrieves SSH connection details for a session
// Returns error if Pod is not ready yet
func (m *JobManager) GetSSHConnectionInfo(ctx context.Context, userAddress, sessionID string) (*SSHConnectionInfo, error) {
	namespace := TenantNamespace(userAddress)

	// Get Service to find NodePort
	svc, err := m.clientset.CoreV1().Services(namespace).Get(ctx, SSHServiceName(sessionID), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH service: %w", err)
	}

	var nodePort int32
	for _, port := range svc.Spec.Ports {
		if port.Name == "ssh" {
			nodePort = port.NodePort
			break
		}
	}

	if nodePort == 0 {
		return nil, fmt.Errorf("SSH NodePort not found in service")
	}

	// Get Pod to find which Node it's running on
	pod, err := m.clientset.CoreV1().Pods(namespace).Get(ctx, PodName(sessionID), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Check Pod readiness
	if !IsPodReady(pod) {
		return nil, fmt.Errorf("pod not ready yet")
	}

	// Determine host IP for SSH connection
	// Always query the K8s node where the Pod is running for the actual IP
	var nodeIP string

	node, err := m.clientset.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		// Fallback to externalHost if node lookup fails (but strip protocol/port)
		if m.externalHost != "" {
			nodeIP = extractHostIP(m.externalHost)
		}
		if nodeIP == "" {
			return nil, fmt.Errorf("failed to get node: %w", err)
		}
	} else {
		// Check annotation first (for GCP nodes without cloud-provider ExternalIP)
		if ip, ok := node.Annotations["worldland.io/external-ip"]; ok && ip != "" {
			nodeIP = ip
		} else {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeExternalIP {
					nodeIP = addr.Address
					break
				}
				if addr.Type == corev1.NodeInternalIP && nodeIP == "" {
					nodeIP = addr.Address
				}
			}
		}
	}

	// Final fallback: use externalHost with protocol/port stripped
	if nodeIP == "" && m.externalHost != "" {
		nodeIP = extractHostIP(m.externalHost)
	}
	if nodeIP == "" {
		return nil, fmt.Errorf("node IP not found")
	}

	// Retrieve SSH password from K8s Secret
	password, err := GetSSHPassword(ctx, m.clientset, namespace, sessionID)
	if err != nil {
		m.logger.Warn("failed to get SSH password", "sessionId", sessionID, "error", err)
		// Return connection info without password - caller may have it stored elsewhere
		password = ""
	}

	return &SSHConnectionInfo{
		Host:     nodeIP,
		Port:     nodePort,
		Password: password,
	}, nil
}

// GetPodStatus returns the Pod phase and readiness status
func (m *JobManager) GetPodStatus(ctx context.Context, userAddress, sessionID string) (*corev1.PodPhase, bool, error) {
	namespace := TenantNamespace(userAddress)

	pod, err := m.clientset.CoreV1().Pods(namespace).Get(ctx, PodName(sessionID), metav1.GetOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("failed to get pod: %w", err)
	}

	phase := pod.Status.Phase
	isReady := IsPodReady(pod)

	return &phase, isReady, nil
}

// IsPodReady checks if a Pod has the Ready condition set to True
func IsPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// ListSessionPods lists all GPU rental session Pods across all namespaces
// For orphan cleanup and monitoring
func (m *JobManager) ListSessionPods(ctx context.Context) ([]corev1.Pod, error) {
	pods, err := m.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", LabelGPURental),
	})
	if err != nil {
		return nil, err
	}

	return pods.Items, nil
}
