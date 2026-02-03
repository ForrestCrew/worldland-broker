//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// E2EConfig holds all configuration for E2E tests
type E2EConfig struct {
	// Hub connection
	HubURL       string
	AuthDisabled bool
	HubTimeout   time.Duration

	// Hardhat connection
	HardhatURL string

	// K8s cluster
	UseExistingCluster bool
	ClusterName        string
	KubeconfigPath     string

	// Test settings
	TestTimeout        time.Duration
	PodReadyTimeout    time.Duration
	DiagnosticsDir     string
	CollectDiagnostics bool
}

// LoadE2EConfig loads configuration from environment variables
func LoadE2EConfig() E2EConfig {
	cfg := E2EConfig{
		// Hub
		HubURL:       getEnv("HUB_URL", "http://localhost:3001"),
		AuthDisabled: getEnv("AUTH_DISABLED", "true") == "true",
		HubTimeout:   30 * time.Second,

		// Hardhat
		HardhatURL: getEnv("HARDHAT_URL", "http://localhost:8545"),

		// K8s
		UseExistingCluster: getEnv("USE_EXISTING_CLUSTER", "true") == "true",
		ClusterName:        getEnv("CLUSTER_NAME", "worldland-dev"),
		KubeconfigPath:     getKubeconfigPath(),

		// Test settings
		TestTimeout:        10 * time.Minute,
		PodReadyTimeout:    2 * time.Minute,
		DiagnosticsDir:     getEnv("E2E_DIAGNOSTICS_DIR", "/tmp/e2e-diagnostics"),
		CollectDiagnostics: getEnv("E2E_COLLECT_DIAGNOSTICS", "true") == "true",
	}

	return cfg
}

// getEnv returns environment variable value or default
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// getKubeconfigPath returns the kubeconfig path
func getKubeconfigPath() string {
	// Check explicit env var first
	if path := os.Getenv("KUBECONFIG"); path != "" {
		return path
	}

	// Default to ~/.kube/config
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// Validate checks if the configuration is valid
func (c *E2EConfig) Validate() error {
	if c.HubURL == "" {
		return fmt.Errorf("HUB_URL is required")
	}
	if c.UseExistingCluster && c.KubeconfigPath == "" {
		return fmt.Errorf("KUBECONFIG or ~/.kube/config must exist when USE_EXISTING_CLUSTER=true")
	}
	return nil
}

// TestAddresses returns Hardhat test wallet addresses
type TestAddresses struct {
	// Hardhat default accounts (deterministic from mnemonic)
	Provider string // Account[1] - used as provider
	Renter   string // Account[2] - used as renter
}

// HardhatTestAddresses returns well-known Hardhat test addresses
func HardhatTestAddresses() TestAddresses {
	return TestAddresses{
		Provider: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", // Account[1]
		Renter:   "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", // Account[2]
	}
}
