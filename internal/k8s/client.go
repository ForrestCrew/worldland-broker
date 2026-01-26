// Package k8s provides Kubernetes client utilities and tenant management.
package k8s

import (
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clientset     *kubernetes.Clientset
	clientsetOnce sync.Once
	clientsetErr  error
)

// Config holds K8s client configuration options.
type Config struct {
	// InCluster indicates whether to use in-cluster config (running inside a pod)
	InCluster bool
	// Kubeconfig is the path to kubeconfig file (used when InCluster is false)
	Kubeconfig string
}

// GetClientset returns a singleton Kubernetes clientset.
// It initializes the client on first call using the provided config.
func GetClientset(cfg *Config) (*kubernetes.Clientset, error) {
	clientsetOnce.Do(func() {
		var config *rest.Config

		if cfg.InCluster {
			// In-cluster config (running inside a pod)
			config, clientsetErr = rest.InClusterConfig()
			if clientsetErr != nil {
				clientsetErr = fmt.Errorf("failed to get in-cluster config: %w", clientsetErr)
				return
			}
		} else {
			// Out-of-cluster config (for local development)
			config, clientsetErr = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
			if clientsetErr != nil {
				clientsetErr = fmt.Errorf("failed to build config from kubeconfig: %w", clientsetErr)
				return
			}
		}

		clientset, clientsetErr = kubernetes.NewForConfig(config)
		if clientsetErr != nil {
			clientsetErr = fmt.Errorf("failed to create clientset: %w", clientsetErr)
			return
		}
	})

	return clientset, clientsetErr
}

// MustGetClientset returns the clientset or panics if not initialized.
// Use this only after GetClientset has been called successfully.
func MustGetClientset() *kubernetes.Clientset {
	if clientset == nil {
		panic("k8s clientset not initialized - call GetClientset first")
	}
	return clientset
}

// ResetClientset resets the singleton for testing purposes.
// This should only be used in tests.
func ResetClientset() {
	clientsetOnce = sync.Once{}
	clientset = nil
	clientsetErr = nil
}
