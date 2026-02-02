package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// StateChangeHandler handles session state changes from K8s Pod events
type StateChangeHandler interface {
	// OnPodRunning is called when a Pod becomes Ready (Running + readiness check passed)
	OnPodRunning(ctx context.Context, sessionID string) error
	// OnPodFailed is called when a Pod fails (phase=Failed)
	OnPodFailed(ctx context.Context, sessionID string, reason string) error
	// OnPodSucceeded is called when a Pod completes (phase=Succeeded)
	OnPodSucceeded(ctx context.Context, sessionID string) error
	// OnPodDeleted is called when a Pod is deleted
	OnPodDeleted(ctx context.Context, sessionID string) error
}

// PodWatcher watches K8s Pods and triggers state change handlers
type PodWatcher struct {
	factory      informers.SharedInformerFactory
	clientset    kubernetes.Interface
	stateHandler StateChangeHandler
	logger       *slog.Logger
	resyncPeriod time.Duration
}

// NewPodWatcher creates a new PodWatcher with Informer-based state synchronization
func NewPodWatcher(clientset kubernetes.Interface, stateHandler StateChangeHandler, logger *slog.Logger) *PodWatcher {
	// Create SharedInformerFactory with label selector for GPU rental Pods only
	tweakListOptions := func(opts *metav1.ListOptions) {
		opts.LabelSelector = fmt.Sprintf("%s=true", LabelGPURental)
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		30*time.Second, // Default resync interval
		informers.WithTweakListOptions(tweakListOptions),
	)

	return &PodWatcher{
		factory:      factory,
		clientset:    clientset,
		stateHandler: stateHandler,
		logger:       logger,
		resyncPeriod: 30 * time.Second,
	}
}

// WithResyncPeriod configures the resync period for the informer
func (pw *PodWatcher) WithResyncPeriod(period time.Duration) *PodWatcher {
	pw.resyncPeriod = period

	// Recreate factory with new resync period
	tweakListOptions := func(opts *metav1.ListOptions) {
		opts.LabelSelector = fmt.Sprintf("%s=true", LabelGPURental)
	}

	pw.factory = informers.NewSharedInformerFactoryWithOptions(
		pw.clientset,
		period,
		informers.WithTweakListOptions(tweakListOptions),
	)

	return pw
}

// Start starts the PodWatcher informer and blocks until context is cancelled
func (pw *PodWatcher) Start(ctx context.Context) error {
	informer := pw.factory.Core().V1().Pods().Informer()

	// Register event handlers
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			pw.handlePodEvent(ctx, pod, "add")
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			pod := newObj.(*corev1.Pod)
			pw.handlePodEvent(ctx, pod, "update")
		},
		DeleteFunc: func(obj interface{}) {
			// Handle delete tombstone
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if ok {
					pod, _ = tombstone.Obj.(*corev1.Pod)
				}
			}
			if pod != nil {
				pw.handlePodDelete(ctx, pod)
			}
		},
	})

	// Start informer (non-blocking)
	pw.factory.Start(ctx.Done())

	// Wait for cache sync
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	pw.logger.Info("pod watcher started with informer", "labelSelector", fmt.Sprintf("%s=true", LabelGPURental))

	// Block until context is cancelled
	<-ctx.Done()
	return nil
}

// handlePodEvent processes Pod events and triggers state change handlers
func (pw *PodWatcher) handlePodEvent(ctx context.Context, pod *corev1.Pod, eventType string) {
	// Extract session ID from labels
	sessionID, ok := pod.Labels[LabelSessionID]
	if !ok {
		// Skip Pods without session-id label (graceful handling)
		pw.logger.Debug("skipping pod without session-id label",
			"podName", pod.Name,
			"namespace", pod.Namespace,
		)
		return
	}

	// Log pod event
	pw.logger.Debug("handling pod event",
		"eventType", eventType,
		"podName", pod.Name,
		"namespace", pod.Namespace,
		"sessionID", sessionID,
		"phase", pod.Status.Phase,
	)

	// Level-driven state handling based on current Pod phase
	switch pod.Status.Phase {
	case corev1.PodPending:
		// No state change for Pending - wait for Running
		pw.logger.Debug("pod pending",
			"sessionID", sessionID,
			"podName", pod.Name,
		)

	case corev1.PodRunning:
		// Check if Pod is Ready (readiness probe passed)
		if isPodReady(pod) {
			pw.logger.Info("pod running and ready",
				"sessionID", sessionID,
				"podName", pod.Name,
				"namespace", pod.Namespace,
			)
			if err := pw.stateHandler.OnPodRunning(ctx, sessionID); err != nil {
				pw.logger.Error("failed to handle pod running",
					"sessionID", sessionID,
					"error", err,
				)
			}
		} else {
			// Pod is Running but not Ready yet
			pw.logger.Debug("pod running but not ready",
				"sessionID", sessionID,
				"podName", pod.Name,
			)
		}

	case corev1.PodFailed:
		reason := extractPodFailureReason(pod)
		pw.logger.Info("pod failed",
			"sessionID", sessionID,
			"podName", pod.Name,
			"namespace", pod.Namespace,
			"reason", reason,
		)
		if err := pw.stateHandler.OnPodFailed(ctx, sessionID, reason); err != nil {
			pw.logger.Error("failed to handle pod failed",
				"sessionID", sessionID,
				"error", err,
			)
		}

	case corev1.PodSucceeded:
		pw.logger.Info("pod succeeded",
			"sessionID", sessionID,
			"podName", pod.Name,
			"namespace", pod.Namespace,
		)
		if err := pw.stateHandler.OnPodSucceeded(ctx, sessionID); err != nil {
			pw.logger.Error("failed to handle pod succeeded",
				"sessionID", sessionID,
				"error", err,
			)
		}

	default:
		pw.logger.Debug("pod in unknown phase",
			"sessionID", sessionID,
			"podName", pod.Name,
			"phase", pod.Status.Phase,
		)
	}
}

// handlePodDelete processes Pod deletion events
func (pw *PodWatcher) handlePodDelete(ctx context.Context, pod *corev1.Pod) {
	// Extract session ID from labels
	sessionID, ok := pod.Labels[LabelSessionID]
	if !ok {
		// Skip Pods without session-id label
		return
	}

	pw.logger.Info("pod deleted",
		"sessionID", sessionID,
		"podName", pod.Name,
		"namespace", pod.Namespace,
	)

	if err := pw.stateHandler.OnPodDeleted(ctx, sessionID); err != nil {
		pw.logger.Error("failed to handle pod deleted",
			"sessionID", sessionID,
			"error", err,
		)
	}
}

// isPodReady checks if the Pod has passed its readiness probe
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// extractPodFailureReason extracts the failure reason from a failed Pod
func extractPodFailureReason(pod *corev1.Pod) string {
	// Check pod status message
	if pod.Status.Message != "" {
		return pod.Status.Message
	}

	// Check pod status reason
	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}

	// Check container statuses for terminated reason
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.State.Terminated != nil {
			if containerStatus.State.Terminated.Reason != "" {
				return containerStatus.State.Terminated.Reason
			}
			if containerStatus.State.Terminated.Message != "" {
				return containerStatus.State.Terminated.Message
			}
		}
	}

	// Check init container statuses
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		if containerStatus.State.Terminated != nil {
			if containerStatus.State.Terminated.Reason != "" {
				return containerStatus.State.Terminated.Reason
			}
			if containerStatus.State.Terminated.Message != "" {
				return containerStatus.State.Terminated.Message
			}
		}
	}

	return "Unknown"
}

// IsCacheSynced returns true if the informer cache is synced
func (pw *PodWatcher) IsCacheSynced() bool {
	informer := pw.factory.Core().V1().Pods().Informer()
	return informer.HasSynced()
}

// GetPodFromCache retrieves a Pod from the informer cache (no API call)
func (pw *PodWatcher) GetPodFromCache(namespace, name string) (*corev1.Pod, bool) {
	lister := pw.factory.Core().V1().Pods().Lister()
	pod, err := lister.Pods(namespace).Get(name)
	if err != nil {
		return nil, false
	}
	return pod, true
}

// ListPodsFromCache lists all Pods from the informer cache
func (pw *PodWatcher) ListPodsFromCache() []*corev1.Pod {
	lister := pw.factory.Core().V1().Pods().Lister()
	pods, err := lister.List(labels.Everything()) // All pods (already filtered by informer's label selector)
	if err != nil {
		pw.logger.Error("failed to list pods from cache", "error", err)
		return nil
	}
	return pods
}
