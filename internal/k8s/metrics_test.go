package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func TestNewMetricsCollectorWithClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	fakeClient := metricsfake.NewSimpleClientset()

	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	if mc == nil {
		t.Fatal("expected non-nil MetricsCollector")
	}
	if mc.metricsClient != fakeClient {
		t.Fatal("expected metricsClient to be set")
	}
	if mc.logger != logger {
		t.Fatal("expected logger to be set")
	}
}

func TestParsePodMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	// Test the parsing logic directly with a PodMetricsList
	podMetricsList := &metricsv1beta1.PodMetricsList{
		Items: []metricsv1beta1.PodMetrics{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "session-abc123",
					Namespace: "tenant-1234567890abcdef",
				},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "gpu-container",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"), // 2 cores
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
					{
						Name: "sidecar",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"), // 0.5 cores
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	}

	metrics := mc.parsePodMetrics(podMetricsList)

	if len(metrics) != 1 {
		t.Fatalf("expected 1 pod metric, got %d", len(metrics))
	}

	pm := metrics[0]
	if pm.PodName != "session-abc123" {
		t.Errorf("expected pod name 'session-abc123', got '%s'", pm.PodName)
	}
	if pm.Namespace != "tenant-1234567890abcdef" {
		t.Errorf("expected namespace 'tenant-1234567890abcdef', got '%s'", pm.Namespace)
	}
	// 2000m + 500m = 2500 milliCPU
	if pm.CPUMilliCores != 2500 {
		t.Errorf("expected 2500 milliCPU, got %d", pm.CPUMilliCores)
	}
	// 4Gi + 512Mi bytes
	expectedMemory := int64(4*1024*1024*1024 + 512*1024*1024)
	if pm.MemoryBytes != expectedMemory {
		t.Errorf("expected %d bytes, got %d", expectedMemory, pm.MemoryBytes)
	}
}

func TestParsePodMetrics_MultipleContainers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	podMetricsList := &metricsv1beta1.PodMetricsList{
		Items: []metricsv1beta1.PodMetrics{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-container-pod",
					Namespace: "test-ns",
				},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "container-1",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					{
						Name: "container-2",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					{
						Name: "container-3",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	}

	metrics := mc.parsePodMetrics(podMetricsList)

	if len(metrics) != 1 {
		t.Fatalf("expected 1 pod metric, got %d", len(metrics))
	}

	pm := metrics[0]
	// 1000m + 2000m + 500m = 3500 milliCPU
	if pm.CPUMilliCores != 3500 {
		t.Errorf("expected 3500 milliCPU, got %d", pm.CPUMilliCores)
	}
	// 1Gi + 2Gi + 512Mi bytes
	expectedMemory := int64(1024*1024*1024 + 2*1024*1024*1024 + 512*1024*1024)
	if pm.MemoryBytes != expectedMemory {
		t.Errorf("expected %d bytes, got %d", expectedMemory, pm.MemoryBytes)
	}
}

func TestParsePodMetrics_Empty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	podMetricsList := &metricsv1beta1.PodMetricsList{
		Items: []metricsv1beta1.PodMetrics{},
	}

	metrics := mc.parsePodMetrics(podMetricsList)

	if len(metrics) != 0 {
		t.Fatalf("expected 0 pod metrics, got %d", len(metrics))
	}
}

func TestParsePodMetrics_MultiplePods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	podMetricsList := &metricsv1beta1.PodMetricsList{
		Items: []metricsv1beta1.PodMetrics{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "ns-1",
				},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "main",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-2",
					Namespace: "ns-1",
				},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "main",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}

	metrics := mc.parsePodMetrics(podMetricsList)

	if len(metrics) != 2 {
		t.Fatalf("expected 2 pod metrics, got %d", len(metrics))
	}

	// Verify first pod
	if metrics[0].PodName != "pod-1" {
		t.Errorf("expected pod name 'pod-1', got '%s'", metrics[0].PodName)
	}
	if metrics[0].CPUMilliCores != 1000 {
		t.Errorf("expected 1000 milliCPU, got %d", metrics[0].CPUMilliCores)
	}

	// Verify second pod
	if metrics[1].PodName != "pod-2" {
		t.Errorf("expected pod name 'pod-2', got '%s'", metrics[1].PodName)
	}
	if metrics[1].CPUMilliCores != 2000 {
		t.Errorf("expected 2000 milliCPU, got %d", metrics[1].CPUMilliCores)
	}
}

func TestGetPodMetrics_EmptyNamespace(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Empty fake client - no pod metrics
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	ctx := context.Background()
	metrics, err := mc.GetPodMetrics(ctx, "empty-namespace")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected 0 pod metrics, got %d", len(metrics))
	}
}

func TestGetNodeMetrics_Empty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	ctx := context.Background()
	metrics, err := mc.GetNodeMetrics(ctx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected 0 node metrics, got %d", len(metrics))
	}
}

func TestGetSinglePodMetrics_NotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Empty fake client - no pod metrics
	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	ctx := context.Background()
	_, err := mc.GetSinglePodMetrics(ctx, "tenant-test", "nonexistent-pod")

	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestGetPodMetricsByLabel_NoResults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	fakeClient := metricsfake.NewSimpleClientset()
	mc := NewMetricsCollectorWithClient(fakeClient, logger)

	ctx := context.Background()
	metrics, err := mc.GetPodMetricsByLabel(ctx, "tenant-abc", "worldland.io/gpu-rental=true")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected 0 results, got %d", len(metrics))
	}
}
