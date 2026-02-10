package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExpirationMonitor checks all rental Pods across registered clusters
// and deletes Pods past their expires-at annotation plus cleans up Failed/Succeeded pods.
// Mirrors proxy's jobExpirationMonitor + cleanupExpiredAndFailedJobs.
// This is a safety net complementing the session ExpirationWorker.
type ExpirationMonitor struct {
	registry        *ExternalClusterRegistry
	capacityTracker *CapacityTracker
	logger          *slog.Logger
}

// NewExpirationMonitor creates a new expiration monitor
func NewExpirationMonitor(
	registry *ExternalClusterRegistry,
	capacityTracker *CapacityTracker,
	logger *slog.Logger,
) *ExpirationMonitor {
	return &ExpirationMonitor{
		registry:        registry,
		capacityTracker: capacityTracker,
		logger:          logger,
	}
}

// Start runs the expiration monitor loop, checking every interval.
// Blocks until ctx is cancelled.
func (m *ExpirationMonitor) Start(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run initial check (proxy pattern: cleanupExpiredAndFailedJobs on startup)
	m.checkAllClusters(ctx)

	for {
		select {
		case <-ticker.C:
			m.checkAllClusters(ctx)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// checkAllClusters iterates over all registered clusters and checks for expired/failed Pods
func (m *ExpirationMonitor) checkAllClusters(ctx context.Context) {
	for _, providerID := range m.registry.ListProviderIDs() {
		client := m.registry.GetClient(providerID)
		if client == nil {
			continue
		}

		if err := m.checkCluster(ctx, providerID, client); err != nil {
			m.logger.Warn("expiration check failed for cluster",
				"providerID", providerID, "error", err)
		}
	}
}

// checkCluster finds and deletes expired or failed rental Pods in a single cluster.
// Mirrors proxy's cleanupExpiredAndFailedJobs: release resources before deletion.
func (m *ExpirationMonitor) checkCluster(ctx context.Context, providerID string, client *ClusterClient) error {
	pods, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", LabelGPURental),
	})
	if err != nil {
		return err
	}

	now := time.Now()
	var expiredCount, failedCount int

	for _, pod := range pods.Items {
		shouldDelete := false
		reason := ""

		// Check for expired jobs (proxy pattern)
		if expiresStr, ok := pod.Annotations[AnnotationExpiresAt]; ok {
			expiresAt, parseErr := time.Parse(time.RFC3339, expiresStr)
			if parseErr != nil {
				m.logger.Warn("invalid expires-at annotation",
					"pod", pod.Name, "namespace", pod.Namespace, "value", expiresStr)
			} else if now.After(expiresAt) {
				shouldDelete = true
				reason = "expired"
				expiredCount++
			}
		}

		// Check for failed/succeeded pods (proxy pattern)
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			shouldDelete = true
			reason = string(pod.Status.Phase)
			failedCount++
		}

		if shouldDelete {
			m.logger.Info("Cleaning up job",
				"pod", pod.Name,
				"namespace", pod.Namespace,
				"reason", reason,
				"providerID", providerID,
			)

			// Release resources first (proxy pattern: release before delete)
			if m.capacityTracker != nil && pod.Spec.NodeName != "" {
				gpus, cpuCores, memGB := extractPodResources(&pod)
				if gpus > 0 || cpuCores > 0 || memGB > 0 {
					m.capacityTracker.ReleaseResources(providerID, pod.Spec.NodeName, ResourceAllocation{
						SessionID: pod.Labels[LabelSessionID],
						GPUCount:  gpus,
						CPUCores:  cpuCores,
						MemoryGB:  memGB,
					})
				}
			}

			// Delete Pod with grace period
			gracePeriod := int64(30)
			err := client.Clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			if err != nil {
				m.logger.Warn("failed to delete expired/failed Pod",
					"pod", pod.Name, "error", err)
			} else {
				m.logger.Info("Job cleaned up successfully",
					"pod", pod.Name, "reason", reason)
			}

			// Also delete associated Service (proxy pattern: cleanup service on deletion)
			sessionID := pod.Labels[LabelSessionID]
			if sessionID != "" {
				svcName := SSHServiceName(sessionID)
				_ = client.Clientset.CoreV1().Services(pod.Namespace).Delete(ctx, svcName, metav1.DeleteOptions{})
			}
		}
	}

	if expiredCount > 0 || failedCount > 0 {
		m.logger.Info("Job cleanup completed",
			"providerID", providerID,
			"expired", expiredCount,
			"failed", failedCount,
		)
	}

	return nil
}
