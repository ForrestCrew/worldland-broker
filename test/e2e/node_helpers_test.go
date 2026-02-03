//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"time"
)

// NodeE2EConfig extends E2EConfig with Node-specific settings
type NodeE2EConfig struct {
	E2EConfig

	// Node-specific settings
	NodeURL     string // worldland-node mTLS API endpoint
	NodeID      string // Expected node ID for testing
	HardhatPort int    // Hardhat port for Node E2E (8546)
	HubPort     int    // Hub port for Node E2E (3002)
}

// DefaultNodeE2EConfig returns configuration for Node-based E2E tests
func DefaultNodeE2EConfig() NodeE2EConfig {
	return NodeE2EConfig{
		E2EConfig: E2EConfig{
			HubURL:             getEnv("NODE_E2E_HUB_URL", "http://localhost:3002"),
			HardhatURL:         getEnv("NODE_E2E_HARDHAT_URL", "http://localhost:8546"),
			AuthDisabled:       getEnv("AUTH_DISABLED", "true") == "true",
			CollectDiagnostics: getEnv("E2E_COLLECT_DIAGNOSTICS", "true") == "true",
			DiagnosticsDir:     getEnv("E2E_DIAGNOSTICS_DIR", "/tmp/e2e-node-diagnostics"),
			TestTimeout:        5 * time.Minute,
		},
		NodeURL:     getEnv("NODE_E2E_NODE_URL", "https://localhost:8446"),
		NodeID:      getEnv("NODE_E2E_NODE_ID", "test-provider-node-1"),
		HardhatPort: 8546,
		HubPort:     3002,
	}
}

// Validate validates the Node E2E configuration
func (c NodeE2EConfig) Validate() error {
	if c.HubURL == "" {
		return fmt.Errorf("NODE_E2E_HUB_URL is required")
	}
	if c.HardhatURL == "" {
		return fmt.Errorf("NODE_E2E_HARDHAT_URL is required")
	}
	if c.NodeID == "" {
		return fmt.Errorf("NODE_E2E_NODE_ID is required")
	}
	return nil
}

// WaitForNodeRegistration waits for worldland-node to register with Hub
func WaitForNodeRegistration(ctx context.Context, hubClient *HubClient, nodeID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		nodes, err := hubClient.ListNodes(ctx)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		for _, node := range nodes {
			if id, ok := node["nodeId"].(string); ok && id == nodeID {
				return nil
			}
			// Also check "id" field
			if id, ok := node["id"].(string); ok && id == nodeID {
				return nil
			}
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("node %s not registered after %v", nodeID, timeout)
}

// WaitForSessionRunning waits for session to transition to RUNNING state
func WaitForSessionRunning(ctx context.Context, hubClient *HubClient, sessionID string, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		session, err := hubClient.GetSession(ctx, sessionID)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		state, _ := session["state"].(string)
		if state == "" {
			state, _ = session["status"].(string) // Alternative field name
		}

		if state == "RUNNING" || state == "running" {
			return session, nil
		}

		if state == "FAILED" || state == "failed" {
			reason, _ := session["failureReason"].(string)
			return nil, fmt.Errorf("session failed: %s", reason)
		}

		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("session %s not RUNNING after %v", sessionID, timeout)
}

// WaitForSessionStopped waits for session to transition to STOPPED state
func WaitForSessionStopped(ctx context.Context, hubClient *HubClient, sessionID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		session, err := hubClient.GetSession(ctx, sessionID)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		state, _ := session["state"].(string)
		if state == "" {
			state, _ = session["status"].(string)
		}

		if state == "STOPPED" || state == "stopped" || state == "TERMINATED" || state == "terminated" {
			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("session %s not STOPPED after %v", sessionID, timeout)
}

// Hardhat2UserAccounts returns 2 accounts for Provider and Renter scenarios
// Account 0: Deployer (not used in tests)
// Account 1: Provider
// Account 2: Renter
func Hardhat2UserAccounts() (provider, renter HardhatAccount) {
	// Hardhat default accounts (deterministic mnemonic)
	// https://hardhat.org/hardhat-network/docs/reference#accounts
	provider = HardhatAccount{
		Address:    "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		PrivateKey: "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
	}
	renter = HardhatAccount{
		Address:    "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
		PrivateKey: "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
	}
	return
}

// HardhatAccount represents a Hardhat test account
type HardhatAccount struct {
	Address    string
	PrivateKey string
}

// CreateNodeE2EDiagnosticsDir creates diagnostics directory for Node E2E tests
func CreateNodeE2EDiagnosticsDir(testName string) (string, error) {
	timestamp := time.Now().Format("2006-01-02T15-04-05-999Z07:00")
	safeName := sanitizeTestName(testName)
	dir := fmt.Sprintf("%s/%s-%s",
		getEnv("E2E_DIAGNOSTICS_DIR", "/tmp/e2e-node-diagnostics"),
		safeName,
		timestamp,
	)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create diagnostics dir: %w", err)
	}

	return dir, nil
}

// sanitizeTestName makes test name safe for directory names
func sanitizeTestName(name string) string {
	result := make([]byte, 0, len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, byte(c))
		} else if c == ' ' || c == '/' {
			result = append(result, '_')
		}
	}
	return string(result)
}
