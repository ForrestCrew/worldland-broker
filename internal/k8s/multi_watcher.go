package k8s

import (
	"context"
	"log/slog"
	"sync"
)

// MultiClusterWatcher manages PodWatchers across multiple K8s clusters.
// Each registered provider gets its own PodWatcher that monitors GPU rental Pods.
type MultiClusterWatcher struct {
	registry     *ExternalClusterRegistry
	stateHandler StateChangeHandler
	logger       *slog.Logger

	mu       sync.Mutex
	watchers map[string]context.CancelFunc // providerID → cancel func
}

// NewMultiClusterWatcher creates a multi-cluster watcher
func NewMultiClusterWatcher(
	registry *ExternalClusterRegistry,
	stateHandler StateChangeHandler,
	logger *slog.Logger,
) *MultiClusterWatcher {
	return &MultiClusterWatcher{
		registry:     registry,
		stateHandler: stateHandler,
		logger:       logger,
		watchers:     make(map[string]context.CancelFunc),
	}
}

// StartWatcher starts a PodWatcher for a specific provider's cluster.
// Idempotent — if a watcher already exists for this provider, it's a no-op.
func (w *MultiClusterWatcher) StartWatcher(ctx context.Context, providerID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.watchers[providerID]; exists {
		return
	}

	client := w.registry.GetClient(providerID)
	if client == nil {
		w.logger.Warn("cannot start watcher: no cluster client",
			"providerID", providerID)
		return
	}

	watchCtx, cancel := context.WithCancel(ctx)
	w.watchers[providerID] = cancel

	podWatcher := NewPodWatcher(client.Clientset, w.stateHandler, w.logger)

	go func() {
		w.logger.Info("starting pod watcher for provider",
			"providerID", providerID)
		if err := podWatcher.Start(watchCtx); err != nil && err != context.Canceled {
			w.logger.Error("pod watcher error",
				"providerID", providerID, "error", err)
		}
		w.logger.Info("pod watcher stopped",
			"providerID", providerID)

		w.mu.Lock()
		delete(w.watchers, providerID)
		w.mu.Unlock()
	}()
}

// StopWatcher stops the PodWatcher for a specific provider
func (w *MultiClusterWatcher) StopWatcher(providerID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cancel, exists := w.watchers[providerID]; exists {
		cancel()
		delete(w.watchers, providerID)
		w.logger.Info("stopped pod watcher for provider",
			"providerID", providerID)
	}
}

// StartAll starts watchers for all currently registered providers.
// Called on Hub startup to recover watchers for existing clusters.
func (w *MultiClusterWatcher) StartAll(ctx context.Context) {
	for _, providerID := range w.registry.ListProviderIDs() {
		w.StartWatcher(ctx, providerID)
	}
}

// StopAll stops all running watchers
func (w *MultiClusterWatcher) StopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for providerID, cancel := range w.watchers {
		cancel()
		delete(w.watchers, providerID)
	}
}

// WatcherCount returns the number of active watchers
func (w *MultiClusterWatcher) WatcherCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watchers)
}
