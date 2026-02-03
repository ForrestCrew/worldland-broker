package k8s

import (
	"context"
	"fmt"
	"log/slog"

	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// PodUsageMetrics represents real-time CPU/Memory usage for a pod
type PodUsageMetrics struct {
	PodName       string
	Namespace     string
	CPUMilliCores int64 // CPU usage in milliCPU (1 core = 1000)
	MemoryBytes   int64 // Memory usage in bytes
}

// NodeUsageMetrics represents real-time CPU/Memory usage for a node
type NodeUsageMetrics struct {
	NodeName      string
	CPUMilliCores int64
	MemoryBytes   int64
}

// MetricsCollector queries Kubernetes Metrics Server for real-time usage
type MetricsCollector struct {
	metricsClient metricsclientset.Interface
	logger        *slog.Logger
}

// NewMetricsCollector creates a new MetricsCollector from rest.Config
// Returns error if Metrics Server client cannot be created
func NewMetricsCollector(config *rest.Config, logger *slog.Logger) (*MetricsCollector, error) {
	metricsClient, err := metricsclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}
	return &MetricsCollector{
		metricsClient: metricsClient,
		logger:        logger,
	}, nil
}

// NewMetricsCollectorWithClient creates a MetricsCollector with a provided metrics client
// Useful for testing with mock clients
func NewMetricsCollectorWithClient(metricsClient metricsclientset.Interface, logger *slog.Logger) *MetricsCollector {
	return &MetricsCollector{
		metricsClient: metricsClient,
		logger:        logger,
	}
}

// GetPodMetrics retrieves real-time CPU/Memory for all pods in a namespace
// Returns empty slice if Metrics Server unavailable (graceful degradation)
func (mc *MetricsCollector) GetPodMetrics(ctx context.Context, namespace string) ([]PodUsageMetrics, error) {
	podMetricsList, err := mc.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		mc.logger.Warn("failed to get pod metrics", "namespace", namespace, "error", err)
		return nil, fmt.Errorf("failed to list pod metrics: %w", err)
	}

	return mc.parsePodMetrics(podMetricsList), nil
}

// GetPodMetricsByLabel retrieves metrics for pods matching label selector
func (mc *MetricsCollector) GetPodMetricsByLabel(ctx context.Context, namespace, labelSelector string) ([]PodUsageMetrics, error) {
	podMetricsList, err := mc.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		mc.logger.Warn("failed to get pod metrics by label",
			"namespace", namespace,
			"labelSelector", labelSelector,
			"error", err,
		)
		return nil, fmt.Errorf("failed to list pod metrics: %w", err)
	}

	return mc.parsePodMetrics(podMetricsList), nil
}

// GetNodeMetrics retrieves real-time CPU/Memory for all nodes
func (mc *MetricsCollector) GetNodeMetrics(ctx context.Context) ([]NodeUsageMetrics, error) {
	nodeMetricsList, err := mc.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		mc.logger.Warn("failed to get node metrics", "error", err)
		return nil, fmt.Errorf("failed to list node metrics: %w", err)
	}

	result := make([]NodeUsageMetrics, 0, len(nodeMetricsList.Items))
	for _, nm := range nodeMetricsList.Items {
		result = append(result, NodeUsageMetrics{
			NodeName:      nm.Name,
			CPUMilliCores: nm.Usage.Cpu().MilliValue(),
			MemoryBytes:   nm.Usage.Memory().Value(),
		})
	}

	return result, nil
}

// GetSinglePodMetrics retrieves metrics for a specific pod
func (mc *MetricsCollector) GetSinglePodMetrics(ctx context.Context, namespace, podName string) (*PodUsageMetrics, error) {
	podMetrics, err := mc.metricsClient.MetricsV1beta1().PodMetricses(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		mc.logger.Warn("failed to get single pod metrics",
			"namespace", namespace,
			"podName", podName,
			"error", err,
		)
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	// Aggregate CPU/Memory across all containers
	var cpuMilliCores, memoryBytes int64
	for _, container := range podMetrics.Containers {
		cpuMilliCores += container.Usage.Cpu().MilliValue()
		memoryBytes += container.Usage.Memory().Value()
	}

	return &PodUsageMetrics{
		PodName:       podMetrics.Name,
		Namespace:     podMetrics.Namespace,
		CPUMilliCores: cpuMilliCores,
		MemoryBytes:   memoryBytes,
	}, nil
}

// parsePodMetrics converts K8s PodMetricsList to our PodUsageMetrics slice
func (mc *MetricsCollector) parsePodMetrics(podMetricsList *metricsv1beta1.PodMetricsList) []PodUsageMetrics {
	result := make([]PodUsageMetrics, 0, len(podMetricsList.Items))

	for _, pm := range podMetricsList.Items {
		// Aggregate CPU/Memory across all containers in the pod
		var cpuMilliCores, memoryBytes int64
		for _, container := range pm.Containers {
			cpuMilliCores += container.Usage.Cpu().MilliValue()
			memoryBytes += container.Usage.Memory().Value()
		}

		result = append(result, PodUsageMetrics{
			PodName:       pm.Name,
			Namespace:     pm.Namespace,
			CPUMilliCores: cpuMilliCores,
			MemoryBytes:   memoryBytes,
		})
	}

	return result
}
