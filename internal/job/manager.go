// Package job provides GPU job creation and management.
package job

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/nubro999/worldland-gpu/internal/k8s"
)

// JobManager handles GPU job creation and management.
type JobManager struct {
	clientset             kubernetes.Interface
	tenantManager         *k8s.TenantManager
	enableTenantIsolation bool
}

// NewJobManager creates a new JobManager.
func NewJobManager(clientset kubernetes.Interface) *JobManager {
	return &JobManager{
		clientset:             clientset,
		tenantManager:         k8s.NewTenantManager(clientset),
		enableTenantIsolation: true, // 기본값: 테넌트 격리 활성화
	}
}

// SetTenantIsolation enables or disables tenant isolation.
func (jm *JobManager) SetTenantIsolation(enable bool) {
	jm.enableTenantIsolation = enable
}

// GPUJobRequest represents a request to create a GPU container.
type GPUJobRequest struct {
	UserID       string  `json:"user_id"`
	JobName      string  `json:"job_name"`
	ProviderID   string  `json:"provider_id"`    // 특정 Provider 선택 (선택적)
	GPUCount     int     `json:"gpu_count"`      // Number of GPUs (default: 1)
	Image        string  `json:"image"`          // Container image (default: nvidia/cuda with SSH)
	CPUCores     string  `json:"cpu_cores"`      // CPU cores (default: "2")
	MemoryGB     string  `json:"memory_gb"`      // Memory in GB (default: "8Gi")
	StorageGB    string  `json:"storage_gb"`     // Storage in GB (default: "10Gi")
	SSHPassword  string  `json:"ssh_password"`   // SSH root password
	Duration     int     `json:"duration_hours"` // How long to keep the container alive
	NodeHostname string  `json:"-"`              // 내부 사용: Provider의 호스트네임
	NodePublicIP string  `json:"-"`              // 내부 사용: Provider의 공인 IP
	GPUModel     string  `json:"-"`              // 내부 사용: GPU 모델명
	PricePerHour float64 `json:"-"`              // 내부 사용: 시간당 가격
}

// GPUJobResponse contains connection info for the created container.
type GPUJobResponse struct {
	JobID      string `json:"job_id"`
	ProviderID string `json:"provider_id,omitempty"`
	Status     string `json:"status"`

	// 할당된 리소스
	GPUCount  int    `json:"gpu_count,omitempty"`
	GPUModel  string `json:"gpu_model,omitempty"`
	CPUCores  string `json:"cpu_cores,omitempty"`
	MemoryGB  string `json:"memory_gb,omitempty"`
	StorageGB string `json:"storage_gb,omitempty"`

	// SSH 접속 정보
	SSHHost     string `json:"ssh_host"`     // Node IP
	SSHPort     int32  `json:"ssh_port"`     // NodePort
	SSHUser     string `json:"ssh_user"`     // root
	SSHPassword string `json:"ssh_password"` // Password set by user

	// 가격 및 만료
	PricePerHour float64   `json:"price_per_hour,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Message      string    `json:"message,omitempty"`

	// 실패 정보 (OOMKilled 등)
	FailureReason  string              `json:"failure_reason,omitempty"`  // "OOMKilled", "Error", etc.
	FailureMessage string              `json:"failure_message,omitempty"` // 상세 메시지
	Suggestion     *ResourceSuggestion `json:"suggestion,omitempty"`      // 개선 제안
}

// ResourceSuggestion provides recommendations for resource adjustment.
type ResourceSuggestion struct {
	Action            string `json:"action"`                       // "increase_memory", "increase_cpu"
	RecommendedMemory string `json:"recommended_memory,omitempty"` // "32Gi"
	RecommendedCPU    string `json:"recommended_cpu,omitempty"`    // "8"
	Message           string `json:"message"`                      // 사용자 친화적 메시지
}

// DefaultGPUJobRequest returns default values for a GPU job request.
func DefaultGPUJobRequest(userID string) *GPUJobRequest {
	return &GPUJobRequest{
		UserID:   userID,
		GPUCount: 1,
		Image:    "nvidia/cuda:12.0.0-devel-ubuntu22.04",
		CPUCores: "2",
		MemoryGB: "8Gi",
		Duration: 24, // 24 hours default
	}
}

// CreateGPUJob creates a GPU container with SSH access.
func (jm *JobManager) CreateGPUJob(ctx context.Context, req *GPUJobRequest) (*GPUJobResponse, error) {
	// Generate unique job ID
	jobID := fmt.Sprintf("gpu-%s-%d", req.UserID, time.Now().Unix())

	// Namespace 결정: 테넌트 격리 활성화 시 사용자별 namespace 사용
	var namespace string
	if jm.enableTenantIsolation && req.UserID != "" && req.UserID != "anonymous" {
		namespace = k8s.GetNamespaceName(req.UserID) // "tenant-{userID}"

		// Tenant namespace가 없으면 생성
		exists, err := jm.tenantManager.TenantExists(ctx, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to check tenant: %w", err)
		}
		if !exists {
			cfg := k8s.DefaultTenantConfig(req.UserID, req.GPUCount)
			cfg.CPURequest = req.CPUCores
			cfg.MemoryRequest = req.MemoryGB
			if err := jm.tenantManager.CreateTenantEnvironment(ctx, cfg); err != nil {
				return nil, fmt.Errorf("failed to create tenant environment: %w", err)
			}
		}
	} else {
		namespace = "default"
	}

	if req.JobName == "" {
		req.JobName = jobID
	}

	// Set defaults
	if req.GPUCount <= 0 {
		req.GPUCount = 1
	}
	if req.Image == "" {
		req.Image = "nvidia/cuda:12.0.0-devel-ubuntu22.04"
	}
	if req.CPUCores == "" {
		req.CPUCores = "2"
	}
	if req.MemoryGB == "" {
		req.MemoryGB = "8Gi"
	} else if !strings.HasSuffix(req.MemoryGB, "Gi") && !strings.HasSuffix(req.MemoryGB, "Mi") {
		req.MemoryGB = req.MemoryGB + "Gi"
	}
	if req.StorageGB == "" {
		req.StorageGB = "20Gi"
	} else if !strings.HasSuffix(req.StorageGB, "Gi") && !strings.HasSuffix(req.StorageGB, "Mi") {
		req.StorageGB = req.StorageGB + "Gi"
	}
	if req.SSHPassword == "" {
		req.SSHPassword = "gpuaccess123"
	}

	// Create Pod
	pod := jm.buildGPUPod(jobID, namespace, req)
	createdPod, err := jm.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	// Create Service (NodePort)
	svc := jm.buildNodePortService(jobID, namespace)
	createdSvc, err := jm.clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		// Cleanup pod if service creation fails
		_ = jm.clientset.CoreV1().Pods(namespace).Delete(ctx, createdPod.Name, metav1.DeleteOptions{})
		return nil, fmt.Errorf("failed to create service: %w", err)
	}

	// Get NodePort
	var sshPort int32
	for _, port := range createdSvc.Spec.Ports {
		if port.Name == "ssh" {
			sshPort = port.NodePort
			break
		}
	}

	// Get node IP (we'll update this when pod is scheduled)
	expiresAt := time.Now().Add(time.Duration(req.Duration) * time.Hour)

	return &GPUJobResponse{
		JobID:       jobID,
		Status:      "creating",
		GPUCount:    req.GPUCount,
		CPUCores:    req.CPUCores,
		MemoryGB:    req.MemoryGB,
		StorageGB:   req.StorageGB,
		SSHHost:     "", // Will be updated when pod is scheduled
		SSHPort:     sshPort,
		SSHUser:     "root",
		SSHPassword: req.SSHPassword,
		ExpiresAt:   expiresAt,
		Message:     "GPU container is being created. Check status in a few seconds.",
	}, nil
}

// buildGPUPod creates a Pod spec with SSH server and GPU access.
func (jm *JobManager) buildGPUPod(jobID, namespace string, req *GPUJobRequest) *corev1.Pod {
	// SSH setup script - improved for container environment
	sshSetupScript := fmt.Sprintf(`
export DEBIAN_FRONTEND=noninteractive
apt-get update && apt-get install -y --no-install-recommends openssh-server
mkdir -p /run/sshd
echo 'root:%s' | chpasswd
sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config
sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/' /etc/ssh/sshd_config

# Add conda/python to PATH for PyTorch/TensorFlow images
if [ -d "/opt/conda/bin" ]; then
  echo 'export PATH="/opt/conda/bin:$PATH"' >> /root/.bashrc
  echo 'source /opt/conda/etc/profile.d/conda.sh 2>/dev/null || true' >> /root/.bashrc
fi

echo "SSH server starting..."
exec /usr/sbin/sshd -D -e
`, req.SSHPassword)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobID,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                      "gpu-job",
				"job-id":                   jobID,
				"user-id":                  req.UserID,
				"worldland.io/gpu-rental":  "true",
				"worldland.io/provider-id": req.ProviderID,
			},
			Annotations: map[string]string{
				"worldland.io/expires-at":     time.Now().Add(time.Duration(req.Duration) * time.Hour).Format(time.RFC3339),
				"worldland.io/storage-gb":     req.StorageGB,
				"worldland.io/gpu-model":      req.GPUModel,
				"worldland.io/price-per-hour": fmt.Sprintf("%.2f", req.PricePerHour),
				"worldland.io/public-ip":      req.NodePublicIP,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "gpu-container",
					Image:   req.Image,
					Command: []string{"/bin/bash", "-c"},
					Args:    []string{sshSetupScript},
					Ports: []corev1.ContainerPort{
						{
							Name:          "ssh",
							ContainerPort: 22,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					Resources: corev1.ResourceRequirements{
						// Guaranteed QoS: Request = Limit (EC2처럼 정확한 리소스 할당)
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:              resource.MustParse(req.CPUCores),
							corev1.ResourceMemory:           resource.MustParse(req.MemoryGB),
							"nvidia.com/gpu":                resource.MustParse(fmt.Sprintf("%d", req.GPUCount)),
							corev1.ResourceEphemeralStorage: resource.MustParse(req.StorageGB),
						},
						Requests: corev1.ResourceList{
							// Request = Limit: 정확히 할당된 리소스만 사용 가능
							corev1.ResourceCPU:              resource.MustParse(req.CPUCores),
							corev1.ResourceMemory:           resource.MustParse(req.MemoryGB),
							corev1.ResourceEphemeralStorage: resource.MustParse(req.StorageGB),
							"nvidia.com/gpu":                resource.MustParse(fmt.Sprintf("%d", req.GPUCount)),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Add: []corev1.Capability{"SYS_ADMIN"},
						},
					},
				},
			},
			// Schedule on specific node if provider specified, otherwise any GPU node
			NodeSelector: jm.buildNodeSelector(req),
			// Tolerate the dedicated rental taint
			Tolerations: []corev1.Toleration{
				{
					Key:      "worldland.io/dedicated-rental",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			},
		},
	}
}

// buildNodeSelector creates a NodeSelector based on provider info.
func (jm *JobManager) buildNodeSelector(req *GPUJobRequest) map[string]string {
	// Provider가 지정된 경우 해당 노드에만 스케줄링
	if req.NodeHostname != "" {
		return map[string]string{
			"kubernetes.io/hostname": req.NodeHostname,
		}
	}

	// 기본: 모든 GPU 노드에 스케줄링 가능
	return map[string]string{
		"worldland.io/rental-type": "gpu",
	}
}

// buildNodePortService creates a NodePort service for SSH access.
func (jm *JobManager) buildNodePortService(jobID, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobID + "-ssh",
			Namespace: namespace,
			Labels: map[string]string{
				"job-id": jobID,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{
				"job-id": jobID,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "ssh",
					Port:       22,
					TargetPort: intstr.FromInt(22),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// GetJobStatus returns the current status of a GPU job.
func (jm *JobManager) GetJobStatus(ctx context.Context, jobID, userID string) (*GPUJobResponse, error) {
	// Namespace 결정
	var namespace string
	if jm.enableTenantIsolation && userID != "" && userID != "anonymous" {
		namespace = k8s.GetNamespaceName(userID)
	} else {
		namespace = "default"
	}

	pod, err := jm.clientset.CoreV1().Pods(namespace).Get(ctx, jobID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}

	svc, err := jm.clientset.CoreV1().Services(namespace).Get(ctx, jobID+"-ssh", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("service not found: %w", err)
	}

	// Get SSH port
	var sshPort int32
	for _, port := range svc.Spec.Ports {
		if port.Name == "ssh" {
			sshPort = port.NodePort
		}
	}

	// Get node IP - prefer public-ip annotation, then ExternalIP, then InternalIP
	var sshHost string

	// First, check for public-ip annotation (set during job creation from Provider data)
	if publicIP := pod.Annotations["worldland.io/public-ip"]; publicIP != "" {
		sshHost = publicIP
	} else if pod.Spec.NodeName != "" {
		// Fallback to node addresses
		node, err := jm.clientset.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if err == nil {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeExternalIP {
					sshHost = addr.Address
					break
				}
				if addr.Type == corev1.NodeInternalIP && sshHost == "" {
					sshHost = addr.Address
				}
			}
		}
	}

	status := string(pod.Status.Phase)

	// Get provider info from labels/annotations
	providerID := pod.Labels["worldland.io/provider-id"]
	expiresAtStr := pod.Annotations["worldland.io/expires-at"]
	expiresAt, _ := time.Parse(time.RFC3339, expiresAtStr)
	gpuModel := pod.Annotations["worldland.io/gpu-model"]
	priceStr := pod.Annotations["worldland.io/price-per-hour"]
	pricePerHour, _ := strconv.ParseFloat(priceStr, 64)
	storageGB := pod.Annotations["worldland.io/storage-gb"]

	// Get resource info from container spec
	var cpuCores, memoryGB string
	var gpuCount int
	if len(pod.Spec.Containers) > 0 {
		container := pod.Spec.Containers[0]
		if cpu := container.Resources.Requests.Cpu(); cpu != nil && !cpu.IsZero() {
			cpuCores = cpu.String()
		}
		if mem := container.Resources.Requests.Memory(); mem != nil && !mem.IsZero() {
			memoryGB = mem.String()
		}
		if gpu, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			gpuCount = int(gpu.Value())
		}
	}

	// Build response
	resp := &GPUJobResponse{
		JobID:        jobID,
		ProviderID:   providerID,
		Status:       status,
		GPUCount:     gpuCount,
		GPUModel:     gpuModel,
		CPUCores:     cpuCores,
		MemoryGB:     memoryGB,
		StorageGB:    storageGB,
		SSHHost:      sshHost,
		SSHPort:      sshPort,
		SSHUser:      "root",
		PricePerHour: pricePerHour,
		ExpiresAt:    expiresAt,
		Message:      fmt.Sprintf("Pod is %s", status),
	}

	// Check for OOMKilled or other failure reasons
	if pod.Status.Phase == corev1.PodFailed {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Terminated != nil {
				terminated := containerStatus.State.Terminated

				if terminated.Reason == "OOMKilled" {
					resp.FailureReason = "OOMKilled"
					resp.FailureMessage = fmt.Sprintf(
						"Container was killed due to memory limit exceeded (Exit Code: %d)",
						terminated.ExitCode,
					)

					// 현재 메모리 설정에서 권장 메모리 계산
					currentMemory := pod.Annotations["worldland.io/memory-gb"]
					if currentMemory == "" {
						// 컨테이너 리소스에서 가져오기
						if len(pod.Spec.Containers) > 0 {
							memLimit := pod.Spec.Containers[0].Resources.Limits.Memory()
							if memLimit != nil {
								currentMemory = memLimit.String()
							}
						}
					}

					// 2배 메모리 권장
					recommendedMemory := jm.calculateRecommendedMemory(currentMemory)

					resp.Suggestion = &ResourceSuggestion{
						Action:            "increase_memory",
						RecommendedMemory: recommendedMemory,
						Message:           fmt.Sprintf("메모리가 부족하여 컨테이너가 종료되었습니다. %s 이상의 메모리로 새 Job을 생성해주세요.", recommendedMemory),
					}
					resp.Message = "Container killed due to out of memory"

				} else if terminated.ExitCode != 0 {
					resp.FailureReason = terminated.Reason
					resp.FailureMessage = fmt.Sprintf(
						"Container exited with code %d: %s",
						terminated.ExitCode,
						terminated.Message,
					)
				}
				break
			}
		}
	}

	return resp, nil
}

// DeleteJob deletes a GPU job and its service.
func (jm *JobManager) DeleteJob(ctx context.Context, jobID, userID string) error {
	// Namespace 결정
	var namespace string
	if jm.enableTenantIsolation && userID != "" && userID != "anonymous" {
		namespace = k8s.GetNamespaceName(userID)
	} else {
		namespace = "default"
	}

	// Delete service
	_ = jm.clientset.CoreV1().Services(namespace).Delete(ctx, jobID+"-ssh", metav1.DeleteOptions{})

	// Delete pod
	err := jm.clientset.CoreV1().Pods(namespace).Delete(ctx, jobID, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	return nil
}

// ListUserJobs lists all GPU jobs for a user.
func (jm *JobManager) ListUserJobs(ctx context.Context, userID string) ([]GPUJobResponse, error) {
	// Namespace 결정
	var namespace string
	if jm.enableTenantIsolation && userID != "" && userID != "anonymous" {
		namespace = k8s.GetNamespaceName(userID)
	} else {
		namespace = "default"
	}

	pods, err := jm.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("user-id=%s,worldland.io/gpu-rental=true", userID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	var jobs []GPUJobResponse
	for _, pod := range pods.Items {
		jobResp, err := jm.GetJobStatus(ctx, pod.Name, userID)
		if err == nil {
			jobs = append(jobs, *jobResp)
		}
	}

	return jobs, nil
}

// calculateRecommendedMemory calculates 2x the current memory as recommendation.
func (jm *JobManager) calculateRecommendedMemory(currentMemory string) string {
	if currentMemory == "" {
		return "32Gi"
	}

	// Parse memory string (e.g., "16Gi", "8G", "16384Mi")
	currentMemory = strings.TrimSpace(currentMemory)

	var value int64
	var unit string

	// Try to parse with different formats
	if strings.HasSuffix(currentMemory, "Gi") {
		fmt.Sscanf(currentMemory, "%dGi", &value)
		unit = "Gi"
	} else if strings.HasSuffix(currentMemory, "G") {
		fmt.Sscanf(currentMemory, "%dG", &value)
		unit = "Gi"
	} else if strings.HasSuffix(currentMemory, "Mi") {
		fmt.Sscanf(currentMemory, "%dMi", &value)
		value = value / 1024 // Convert to Gi
		unit = "Gi"
	} else {
		// Default: assume it's in bytes or unknown format
		return "32Gi"
	}

	// Double the memory
	recommendedValue := value * 2
	if recommendedValue < 8 {
		recommendedValue = 8 // Minimum 8Gi
	}
	if recommendedValue > 512 {
		recommendedValue = 512 // Maximum 512Gi (practical limit)
	}

	return fmt.Sprintf("%d%s", recommendedValue, unit)
}
