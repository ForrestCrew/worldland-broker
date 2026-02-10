package k8s

import (
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ClusterClient holds K8s client resources for an external provider cluster
type ClusterClient struct {
	Clientset    kubernetes.Interface
	ExternalHost string // External host/IP for SSH access
	CACertData   []byte // CA certificate PEM from kubeconfig (for join token hash)
}

// ExternalClusterRegistry manages K8s clientsets for external provider clusters.
// Each K8s provider registers their kubeconfig, and the registry maintains
// a providerID -> ClusterClient map for routing operations.
type ExternalClusterRegistry struct {
	clusters map[string]*ClusterClient // providerID -> ClusterClient
	mu       sync.RWMutex
	logger   *slog.Logger
}

// NewExternalClusterRegistry creates a new registry for external K8s clusters
func NewExternalClusterRegistry(logger *slog.Logger) *ExternalClusterRegistry {
	return &ExternalClusterRegistry{
		clusters: make(map[string]*ClusterClient),
		logger:   logger,
	}
}

// RegisterCluster parses kubeconfig bytes, creates a clientset, and registers it for the provider.
// Returns error if kubeconfig is invalid or clientset creation fails.
func (r *ExternalClusterRegistry) RegisterCluster(providerID string, kubeconfigBytes []byte) error {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	externalHost := config.Host

	r.mu.Lock()
	r.clusters[providerID] = &ClusterClient{
		Clientset:    clientset,
		ExternalHost: externalHost,
		CACertData:   config.TLSClientConfig.CAData,
	}
	r.mu.Unlock()

	r.logger.Info("registered external K8s cluster",
		"providerID", providerID,
		"host", externalHost,
	)

	return nil
}

// GetClient returns the ClusterClient for a provider, or nil if not registered
func (r *ExternalClusterRegistry) GetClient(providerID string) *ClusterClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clusters[providerID]
}

// RemoveCluster removes a cluster registration
func (r *ExternalClusterRegistry) RemoveCluster(providerID string) {
	r.mu.Lock()
	delete(r.clusters, providerID)
	r.mu.Unlock()

	r.logger.Info("removed external K8s cluster", "providerID", providerID)
}

// ListProviderIDs returns all registered provider IDs
func (r *ExternalClusterRegistry) ListProviderIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.clusters))
	for id := range r.clusters {
		ids = append(ids, id)
	}
	return ids
}
