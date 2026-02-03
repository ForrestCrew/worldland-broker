package monitoring

import (
	"context"
	"log/slog"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"github.com/worldland/worldland-hub/internal/k8s"
)

// MonitoringService aggregates K8s metrics for tenant and provider dashboards
type MonitoringService struct {
	orchestrator     *k8s.TenantOrchestrator
	metricsCollector *k8s.MetricsCollector
	podWatcher       *k8s.PodWatcher
	logger           *slog.Logger
}

// NewMonitoringService creates a new MonitoringService
// metricsCollector can be nil (graceful degradation if Metrics Server unavailable)
func NewMonitoringService(
	orchestrator *k8s.TenantOrchestrator,
	metricsCollector *k8s.MetricsCollector,
	podWatcher *k8s.PodWatcher,
	logger *slog.Logger,
) *MonitoringService {
	return &MonitoringService{
		orchestrator:     orchestrator,
		metricsCollector: metricsCollector,
		podWatcher:       podWatcher,
		logger:           logger,
	}
}

// GetTenantUsage returns quota status and real-time usage for a tenant
func (s *MonitoringService) GetTenantUsage(ctx context.Context, userAddress string) (*TenantUsage, error) {
	namespace := k8s.TenantNamespace(userAddress)

	// 1. Get ResourceQuota status (allocation limits)
	quotaStatus, err := s.orchestrator.GetTenantQuotaUsage(ctx, userAddress)
	if err != nil {
		s.logger.Warn("failed to get tenant quota", "userAddress", userAddress, "error", err)
		// Return partial data if quota fetch fails
		quotaStatus = nil
	}

	// Parse quota into QuotaStatus
	quota := parseQuotaStatus(quotaStatus)

	// 2. Get real-time metrics (actual usage)
	var realTimeUsage ResourceMetrics
	if s.metricsCollector != nil {
		podMetrics, err := s.metricsCollector.GetPodMetrics(ctx, namespace)
		if err != nil {
			s.logger.Warn("failed to get pod metrics", "namespace", namespace, "error", err)
		} else {
			// Aggregate CPU/Memory across all pods
			for _, pm := range podMetrics {
				realTimeUsage.CPUMilliCores += pm.CPUMilliCores
				realTimeUsage.MemoryMiB += pm.MemoryBytes / (1024 * 1024)
			}
		}
	}

	// 3. Count active pods from PodWatcher cache
	activePods := 0
	if s.podWatcher != nil {
		pods := s.podWatcher.ListPodsFromCache()
		for _, pod := range pods {
			if pod.Namespace == namespace && pod.Status.Phase == corev1.PodRunning {
				activePods++
			}
		}
	}

	return &TenantUsage{
		Namespace:     namespace,
		UserAddress:   userAddress,
		Quota:         quota,
		RealTimeUsage: realTimeUsage,
		ActivePods:    activePods,
	}, nil
}

// GetProviderStats returns dashboard data for a provider
func (s *MonitoringService) GetProviderStats(ctx context.Context, providerID string) (*ProviderStats, error) {
	stats := &ProviderStats{
		ProviderID: providerID,
		Sessions:   []SessionMetrics{},
	}

	// Get all pods from PodWatcher cache
	if s.podWatcher == nil {
		return stats, nil
	}

	pods := s.podWatcher.ListPodsFromCache()

	// Filter by provider ID and build session metrics
	for _, pod := range pods {
		podProviderID, ok := pod.Labels[k8s.LabelProviderID]
		if !ok || podProviderID != providerID {
			continue
		}

		sessionID := pod.Labels[k8s.LabelSessionID]
		if sessionID == "" {
			continue
		}

		// Build session metrics
		session := SessionMetrics{
			SessionID:  sessionID,
			PodName:    pod.Name,
			Namespace:  pod.Namespace,
			ProviderID: providerID,
			PodPhase:   string(pod.Status.Phase),
			IsReady:    isPodReady(pod),
			GPUCount:   extractGPUCount(pod),
			ExpiresAt:  pod.Annotations[k8s.AnnotationExpiresAt],
		}

		// Get real-time metrics if available
		if s.metricsCollector != nil {
			podMetrics, err := s.metricsCollector.GetSinglePodMetrics(ctx, pod.Namespace, pod.Name)
			if err == nil && podMetrics != nil {
				session.CPUMilliCores = podMetrics.CPUMilliCores
				session.MemoryMiB = podMetrics.MemoryBytes / (1024 * 1024)
			}
		}

		stats.Sessions = append(stats.Sessions, session)
		stats.TotalSessions++

		// Count as active if Running and Ready
		if pod.Status.Phase == corev1.PodRunning && isPodReady(pod) {
			stats.ActiveSessions++
		}

		// Aggregate resource usage
		stats.TotalCPUUsage += session.CPUMilliCores
		stats.TotalMemoryUsage += session.MemoryMiB
	}

	return stats, nil
}

// GetAllActiveSessions returns metrics for all active GPU rental sessions
func (s *MonitoringService) GetAllActiveSessions(ctx context.Context) ([]SessionMetrics, error) {
	sessions := []SessionMetrics{}

	if s.podWatcher == nil {
		return sessions, nil
	}

	pods := s.podWatcher.ListPodsFromCache()

	for _, pod := range pods {
		sessionID := pod.Labels[k8s.LabelSessionID]
		if sessionID == "" {
			continue
		}

		session := SessionMetrics{
			SessionID:  sessionID,
			PodName:    pod.Name,
			Namespace:  pod.Namespace,
			ProviderID: pod.Labels[k8s.LabelProviderID],
			PodPhase:   string(pod.Status.Phase),
			IsReady:    isPodReady(pod),
			GPUCount:   extractGPUCount(pod),
			ExpiresAt:  pod.Annotations[k8s.AnnotationExpiresAt],
		}

		// Get real-time metrics if available
		if s.metricsCollector != nil {
			podMetrics, err := s.metricsCollector.GetSinglePodMetrics(ctx, pod.Namespace, pod.Name)
			if err == nil && podMetrics != nil {
				session.CPUMilliCores = podMetrics.CPUMilliCores
				session.MemoryMiB = podMetrics.MemoryBytes / (1024 * 1024)
			}
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// GetSessionMetrics returns metrics for a specific session
func (s *MonitoringService) GetSessionMetrics(ctx context.Context, sessionID string) (*SessionMetrics, error) {
	if s.podWatcher == nil {
		return nil, nil
	}

	pods := s.podWatcher.ListPodsFromCache()

	for _, pod := range pods {
		if pod.Labels[k8s.LabelSessionID] != sessionID {
			continue
		}

		session := &SessionMetrics{
			SessionID:  sessionID,
			PodName:    pod.Name,
			Namespace:  pod.Namespace,
			ProviderID: pod.Labels[k8s.LabelProviderID],
			PodPhase:   string(pod.Status.Phase),
			IsReady:    isPodReady(pod),
			GPUCount:   extractGPUCount(pod),
			ExpiresAt:  pod.Annotations[k8s.AnnotationExpiresAt],
		}

		// Get real-time metrics if available
		if s.metricsCollector != nil {
			podMetrics, err := s.metricsCollector.GetSinglePodMetrics(ctx, pod.Namespace, pod.Name)
			if err == nil && podMetrics != nil {
				session.CPUMilliCores = podMetrics.CPUMilliCores
				session.MemoryMiB = podMetrics.MemoryBytes / (1024 * 1024)
			}
		}

		return session, nil
	}

	return nil, nil
}

// parseQuotaStatus converts K8s ResourceQuotaStatus to our QuotaStatus DTO
func parseQuotaStatus(status *corev1.ResourceQuotaStatus) QuotaStatus {
	if status == nil {
		return QuotaStatus{}
	}

	return QuotaStatus{
		GPUHard:    parseQuantityInt(status.Hard, "requests.nvidia.com/gpu"),
		GPUUsed:    parseQuantityInt(status.Used, "requests.nvidia.com/gpu"),
		CPUHard:    parseQuantityString(status.Hard, "requests.cpu"),
		CPUUsed:    parseQuantityString(status.Used, "requests.cpu"),
		MemoryHard: parseQuantityString(status.Hard, "requests.memory"),
		MemoryUsed: parseQuantityString(status.Used, "requests.memory"),
		PodsHard:   parseQuantityInt(status.Hard, string(corev1.ResourcePods)),
		PodsUsed:   parseQuantityInt(status.Used, string(corev1.ResourcePods)),
	}
}

// parseQuantityInt extracts an integer value from ResourceList
func parseQuantityInt(list corev1.ResourceList, key string) int {
	if q, ok := list[corev1.ResourceName(key)]; ok {
		return int(q.Value())
	}
	return 0
}

// parseQuantityString extracts a string value from ResourceList
func parseQuantityString(list corev1.ResourceList, key string) string {
	if q, ok := list[corev1.ResourceName(key)]; ok {
		return q.String()
	}
	return "0"
}

// isPodReady checks if a pod has passed its readiness probe
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// extractGPUCount extracts GPU count from pod spec
func extractGPUCount(pod *corev1.Pod) int {
	for _, container := range pod.Spec.Containers {
		if q, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			val, err := strconv.Atoi(q.String())
			if err == nil {
				return val
			}
		}
	}
	return 0
}
