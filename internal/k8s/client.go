package k8s

import (
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClientsetManager manages the K8s clientset singleton
type ClientsetManager struct {
	clientset kubernetes.Interface
	config    *rest.Config
	logger    *slog.Logger
	mu        sync.RWMutex
}

var (
	instance *ClientsetManager
	once     sync.Once
)

// GetManager returns the singleton ClientsetManager instance
func GetManager() *ClientsetManager {
	once.Do(func() {
		instance = &ClientsetManager{}
	})
	return instance
}

// InitFromKubeconfig initializes the K8s clientset from a kubeconfig file
func (cm *ClientsetManager) InitFromKubeconfig(kubeconfigPath string, logger *slog.Logger) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.clientset != nil {
		return fmt.Errorf("clientset already initialized")
	}

	// Build config from kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	cm.clientset = clientset
	cm.config = config
	cm.logger = logger

	logger.Info("K8s clientset initialized from kubeconfig", "path", kubeconfigPath)

	return nil
}

// InitInCluster initializes the K8s clientset using in-cluster configuration
func (cm *ClientsetManager) InitInCluster(logger *slog.Logger) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.clientset != nil {
		return fmt.Errorf("clientset already initialized")
	}

	// Use in-cluster config (ServiceAccount token)
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	cm.clientset = clientset
	cm.config = config
	cm.logger = logger

	logger.Info("K8s clientset initialized in-cluster mode")

	return nil
}

// GetClientset returns the K8s clientset if initialized
func (cm *ClientsetManager) GetClientset() (kubernetes.Interface, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.clientset == nil {
		return nil, fmt.Errorf("clientset not initialized")
	}

	return cm.clientset, nil
}

// IsInitialized returns true if the clientset is initialized
func (cm *ClientsetManager) IsInitialized() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.clientset != nil
}

// GetConfig returns the stored K8s config (for advanced use)
func (cm *ClientsetManager) GetConfig() *rest.Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.config
}
