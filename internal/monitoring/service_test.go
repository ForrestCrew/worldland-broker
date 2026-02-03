package monitoring

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestParseQuotaStatus(t *testing.T) {
	status := &corev1.ResourceQuotaStatus{
		Hard: corev1.ResourceList{
			"requests.nvidia.com/gpu": resource.MustParse("4"),
			"requests.cpu":            resource.MustParse("32"),
			"requests.memory":         resource.MustParse("128Gi"),
			corev1.ResourcePods:       resource.MustParse("10"),
		},
		Used: corev1.ResourceList{
			"requests.nvidia.com/gpu": resource.MustParse("2"),
			"requests.cpu":            resource.MustParse("16"),
			"requests.memory":         resource.MustParse("64Gi"),
			corev1.ResourcePods:       resource.MustParse("5"),
		},
	}

	quota := parseQuotaStatus(status)

	if quota.GPUHard != 4 {
		t.Errorf("expected GPUHard=4, got %d", quota.GPUHard)
	}
	if quota.GPUUsed != 2 {
		t.Errorf("expected GPUUsed=2, got %d", quota.GPUUsed)
	}
	if quota.CPUHard != "32" {
		t.Errorf("expected CPUHard='32', got '%s'", quota.CPUHard)
	}
	if quota.CPUUsed != "16" {
		t.Errorf("expected CPUUsed='16', got '%s'", quota.CPUUsed)
	}
	if quota.PodsHard != 10 {
		t.Errorf("expected PodsHard=10, got %d", quota.PodsHard)
	}
	if quota.PodsUsed != 5 {
		t.Errorf("expected PodsUsed=5, got %d", quota.PodsUsed)
	}
}

func TestParseQuotaStatus_Nil(t *testing.T) {
	quota := parseQuotaStatus(nil)

	if quota.GPUHard != 0 {
		t.Errorf("expected GPUHard=0 for nil status, got %d", quota.GPUHard)
	}
	// When status is nil, parseQuotaStatus returns empty QuotaStatus with empty strings
	if quota.CPUHard != "" {
		t.Errorf("expected CPUHard='' for nil status, got '%s'", quota.CPUHard)
	}
}

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "pod is ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			},
			expected: true,
		},
		{
			name: "pod is not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			},
			expected: false,
		},
		{
			name: "no ready condition",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
					},
				},
			},
			expected: false,
		},
		{
			name:     "no conditions",
			pod:      &corev1.Pod{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPodReady(tt.pod)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestExtractGPUCount(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected int
	}{
		{
			name: "pod with 2 GPUs",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "gpu-workload",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"nvidia.com/gpu": resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			expected: 2,
		},
		{
			name: "pod with no GPU",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "cpu-workload",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "pod with empty containers",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractGPUCount(tt.pod)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestNewMonitoringService(t *testing.T) {
	// Verify that NewMonitoringService creates a service even with nil dependencies
	// This tests graceful degradation
	service := NewMonitoringService(nil, nil, nil, nil)

	if service == nil {
		t.Fatal("expected non-nil MonitoringService")
	}
}

func TestParseQuantityInt(t *testing.T) {
	tests := []struct {
		name     string
		list     corev1.ResourceList
		key      string
		expected int
	}{
		{
			name: "existing key",
			list: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("4"),
			},
			key:      "nvidia.com/gpu",
			expected: 4,
		},
		{
			name: "missing key",
			list: corev1.ResourceList{
				"cpu": resource.MustParse("8"),
			},
			key:      "nvidia.com/gpu",
			expected: 0,
		},
		{
			name:     "empty list",
			list:     corev1.ResourceList{},
			key:      "any-key",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseQuantityInt(tt.list, tt.key)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestParseQuantityString(t *testing.T) {
	tests := []struct {
		name     string
		list     corev1.ResourceList
		key      string
		expected string
	}{
		{
			name: "cpu cores",
			list: corev1.ResourceList{
				"requests.cpu": resource.MustParse("16"),
			},
			key:      "requests.cpu",
			expected: "16",
		},
		{
			name: "memory gi",
			list: corev1.ResourceList{
				"requests.memory": resource.MustParse("64Gi"),
			},
			key:      "requests.memory",
			expected: "64Gi",
		},
		{
			name:     "missing key",
			list:     corev1.ResourceList{},
			key:      "missing",
			expected: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseQuantityString(tt.list, tt.key)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
