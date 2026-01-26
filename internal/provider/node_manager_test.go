package provider

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ================== Pod 파싱 테스트 ==================

func createTestPod(name string, annotations map[string]string, labels map[string]string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "tenant-user1",
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "gpu-container",
					Image: "nvidia/cuda:12.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"nvidia.com/gpu":      resource.MustParse("2"),
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}
}

func TestExtractPodResourceInfo(t *testing.T) {
	pod := createTestPod("job-123", nil, nil, corev1.PodRunning)

	gpuCount, cpuCores, memoryMB := extractPodResourceInfo(pod)

	if gpuCount != 2 {
		t.Errorf("GPUCount = %d; want 2", gpuCount)
	}
	if cpuCores != 4 {
		t.Errorf("CPUCores = %d; want 4", cpuCores)
	}
	// 16Gi = 16384 MB
	if memoryMB != 16384 {
		t.Errorf("MemoryMB = %d; want 16384", memoryMB)
	}
}

func TestExtractPodResourceInfo_NoResources(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "empty-pod",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container",
					// No resources specified
				},
			},
		},
	}

	gpuCount, cpuCores, memoryMB := extractPodResourceInfo(pod)

	if gpuCount != 0 {
		t.Errorf("GPUCount = %d; want 0", gpuCount)
	}
	if cpuCores != 0 {
		t.Errorf("CPUCores = %d; want 0", cpuCores)
	}
	if memoryMB != 0 {
		t.Errorf("MemoryMB = %d; want 0", memoryMB)
	}
}

func TestExtractPodResourceInfo_NoContainers(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "no-container-pod",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{},
		},
	}

	gpuCount, cpuCores, memoryMB := extractPodResourceInfo(pod)

	if gpuCount != 0 || cpuCores != 0 || memoryMB != 0 {
		t.Error("Empty container slice should return zero values")
	}
}

func TestParsePodToGPUJobInfo(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	labels := map[string]string{
		"job-id":                   "job-abc123",
		"user-id":                  "user42",
		"worldland.io/provider-id": "provider-xyz",
	}
	annotations := map[string]string{
		"worldland.io/expires-at": expiresAt,
		"worldland.io/gpu-model":  "Tesla T4",
	}

	pod := createTestPod("job-abc123", annotations, labels, corev1.PodRunning)

	info := parsePodToGPUJobInfo(pod)

	if info.JobID != "job-abc123" {
		t.Errorf("JobID = %s; want job-abc123", info.JobID)
	}
	if info.UserID != "user42" {
		t.Errorf("UserID = %s; want user42", info.UserID)
	}
	if info.ProviderID != "provider-xyz" {
		t.Errorf("ProviderID = %s; want provider-xyz", info.ProviderID)
	}
	if info.GPUModel != "Tesla T4" {
		t.Errorf("GPUModel = %s; want Tesla T4", info.GPUModel)
	}
	if info.GPUCount != 2 {
		t.Errorf("GPUCount = %d; want 2", info.GPUCount)
	}
	if info.Status != "Running" {
		t.Errorf("Status = %s; want Running", info.Status)
	}
	if info.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be parsed")
	}
}

func TestParsePodToGPUJobInfo_InvalidExpiresAt(t *testing.T) {
	labels := map[string]string{
		"job-id": "job-123",
	}
	annotations := map[string]string{
		"worldland.io/expires-at": "invalid-time-format",
	}

	pod := createTestPod("job-123", annotations, labels, corev1.PodRunning)

	info := parsePodToGPUJobInfo(pod)

	// Invalid time should result in zero value
	if !info.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should be zero for invalid format, got %v", info.ExpiresAt)
	}
}

func TestParsePodToGPUJobInfo_MissingLabels(t *testing.T) {
	pod := createTestPod("orphan-pod", nil, nil, corev1.PodRunning)

	info := parsePodToGPUJobInfo(pod)

	// JobID should fall back to pod name
	if info.JobID != "orphan-pod" {
		t.Errorf("JobID = %s; want orphan-pod", info.JobID)
	}
}

func TestParsePodToGPUJobInfo_AllPhases(t *testing.T) {
	phases := []struct {
		phase    corev1.PodPhase
		expected string
	}{
		{corev1.PodPending, "Pending"},
		{corev1.PodRunning, "Running"},
		{corev1.PodSucceeded, "Succeeded"},
		{corev1.PodFailed, "Failed"},
		{corev1.PodUnknown, "Unknown"},
	}

	for _, p := range phases {
		pod := createTestPod("test-pod", nil, nil, p.phase)
		info := parsePodToGPUJobInfo(pod)

		if info.Status != p.expected {
			t.Errorf("Phase %s: Status = %s; want %s", p.phase, info.Status, p.expected)
		}
	}
}

// ================== GPUJobPodInfo 만료 체크 테스트 ==================

func TestGPUJobPodInfo_IsExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt time.Time
		expected  bool
	}{
		{
			name:      "Expired 1 hour ago",
			expiresAt: now.Add(-1 * time.Hour),
			expected:  true,
		},
		{
			name:      "Expires in 1 hour",
			expiresAt: now.Add(1 * time.Hour),
			expected:  false,
		},
		{
			name:      "Expired just now",
			expiresAt: now.Add(-1 * time.Second),
			expected:  true,
		},
		{
			name:      "Zero time (no expiration)",
			expiresAt: time.Time{},
			expected:  false, // Zero time means no expiration set
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &GPUJobPodInfo{
				ExpiresAt: tt.expiresAt,
			}

			isExpired := !info.ExpiresAt.IsZero() && now.After(info.ExpiresAt)

			if isExpired != tt.expected {
				t.Errorf("isExpired = %v; want %v", isExpired, tt.expected)
			}
		})
	}
}

// ================== Memory 단위 변환 테스트 ==================

func TestMemoryConversion(t *testing.T) {
	tests := []struct {
		quantity   string
		expectedMB int
	}{
		{"16Gi", 16384},        // 16 GiB = 16384 MiB
		{"8Gi", 8192},          // 8 GiB = 8192 MiB
		{"32Gi", 32768},        // 32 GiB
		{"1Gi", 1024},          // 1 GiB
		{"512Mi", 512},         // 512 MiB
		{"1024Mi", 1024},       // 1024 MiB
		{"17179869184", 16384}, // 16 GiB in bytes
	}

	for _, tt := range tests {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse(tt.quantity),
							},
						},
					},
				},
			},
		}

		_, _, memoryMB := extractPodResourceInfo(pod)

		if memoryMB != int64(tt.expectedMB) {
			t.Errorf("Memory %s: got %d MB; want %d MB", tt.quantity, memoryMB, tt.expectedMB)
		}
	}
}

// ================== 다중 컨테이너 테스트 ==================

func TestExtractPodResourceInfo_MultipleContainers(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"nvidia.com/gpu":      resource.MustParse("2"),
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
				{
					Name: "sidecar", // This container should be ignored
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}

	// extractPodResourceInfo only looks at the first container
	gpuCount, cpuCores, memoryMB := extractPodResourceInfo(pod)

	if gpuCount != 2 {
		t.Errorf("GPUCount = %d; want 2 (from first container)", gpuCount)
	}
	if cpuCores != 4 {
		t.Errorf("CPUCores = %d; want 4 (from first container)", cpuCores)
	}
	if memoryMB != 8192 {
		t.Errorf("MemoryMB = %d; want 8192 (from first container)", memoryMB)
	}
}

// ================== 분수 CPU 테스트 ==================

func TestExtractPodResourceInfo_FractionalCPU(t *testing.T) {
	// Note: resource.Quantity.Value() rounds UP for fractional values
	// This is K8s's behavior, not ours
	tests := []struct {
		cpuStr   string
		expected int
	}{
		{"500m", 1},  // 0.5 cores -> 1 (rounded up by K8s)
		{"1500m", 2}, // 1.5 cores -> 2 (rounded up)
		{"2000m", 2}, // 2 cores
		{"100m", 1},  // 0.1 cores -> 1 (rounded up)
		{"4", 4},     // 4 cores
		{"0.5", 1},   // 0.5 cores -> 1 (rounded up)
		{"1", 1},     // 1 core
		{"3000m", 3}, // 3 cores
	}

	for _, tt := range tests {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse(tt.cpuStr),
							},
						},
					},
				},
			},
		}

		_, cpuCores, _ := extractPodResourceInfo(pod)

		if cpuCores != tt.expected {
			t.Errorf("CPU %s: got %d cores; want %d cores", tt.cpuStr, cpuCores, tt.expected)
		}
	}
}
