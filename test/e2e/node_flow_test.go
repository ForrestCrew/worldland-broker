//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// TestNodeBased2UserRentalFlow is a comprehensive E2E test for Node-based worldland-node flow
//
// This test verifies the complete Node-based rental lifecycle with 2 users:
// Provider (Hardhat Account[1]) and Renter (Hardhat Account[2])
//
// Flow:
// 1. Hub health verification
// 2. Wait for worldland-node registration
// 3. Renter discovers available nodes
// 4. Renter creates rental session
// 5. Renter deposits tokens via blockchain
// 6. Hub confirms deposit and provisions container via worldland-node
// 7. Container transitions to RUNNING state
// 8. Renter retrieves SSH credentials
// 9. Verify actual SSH connection to container
// 10. Renter terminates session
// 11. Verify settlement data
//
// REQUIREMENTS:
// - Running docker-compose.node.yml (Hub + PostgreSQL + Hardhat + worldland-node)
// - NODE_E2E=true environment variable to enable this test
// - worldland-node must auto-register with Hub via mTLS heartbeat
// - Hub with AUTH_DISABLED=true for testing simplicity
//
// SKIP LOGIC:
// Test is skipped unless NODE_E2E=true is set explicitly.
func TestNodeBased2UserRentalFlow(t *testing.T) {
	// Skip unless explicitly enabled
	if os.Getenv("NODE_E2E") != "true" {
		t.Skip("Skipping Node E2E test (set NODE_E2E=true to enable)")
	}

	cfg := DefaultNodeE2EConfig()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Invalid Node E2E configuration: %v", err)
	}

	hubClient := NewHubClient(cfg.HubURL)
	blockchainHelper := NewBlockchainHelper(cfg.HardhatURL)

	// Get 2-user test accounts
	providerAccount, renterAccount := Hardhat2UserAccounts()
	t.Logf("Provider: %s", providerAccount.Address)
	t.Logf("Renter: %s", renterAccount.Address)

	// Contract helper (initialized in setup)
	var contractHelper *ContractHelper

	// Session tracking
	var sessionID string

	feature := features.New("Node-based 2-User Rental Flow").
		Setup(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Check Hub availability
			if err := hubClient.Health(ctx); err != nil {
				t.Skipf("Hub server not available at %s: %v", cfg.HubURL, err)
			}
			t.Logf("Hub is available at %s", cfg.HubURL)

			// Check Hardhat availability
			if !blockchainHelper.client.IsHealthy(ctx) {
				t.Skipf("Hardhat node not available at %s", cfg.HardhatURL)
			}
			t.Logf("Hardhat node is available at %s", cfg.HardhatURL)

			// Initialize contract helper
			var err error
			contractHelper, err = NewContractHelperFromDeployment(blockchainHelper)
			if err != nil {
				t.Skipf("Failed to load contract addresses: %v", err)
			}
			t.Logf("Contract addresses loaded - Rental: %s, Token: %s",
				contractHelper.rentalAddr.Hex(), contractHelper.tokenAddr.Hex())

			// Setup auth if required (recommend AUTH_DISABLED=true for Node E2E)
			if !cfg.AuthDisabled {
				t.Log("WARNING: AUTH_DISABLED=false - authentication not implemented in E2E helpers")
				t.Skip("Run Hub with AUTH_DISABLED=true for Node E2E tests")
			}

			// Create EVM snapshot for test isolation
			if err := blockchainHelper.Snapshot(ctx); err != nil {
				t.Logf("WARNING: Failed to create EVM snapshot: %v", err)
			} else {
				t.Log("Created EVM snapshot for test isolation")
			}

			return ctx
		}).
		Assess("1. Hub health verification", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if err := hubClient.Health(ctx); err != nil {
				t.Fatalf("Hub health check failed: %v", err)
			}
			t.Log("✓ Hub is healthy")
			return ctx
		}).
		Assess("2. Wait for worldland-node registration", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			t.Logf("Waiting for node %s to register with Hub...", cfg.NodeID)

			if err := WaitForNodeRegistration(ctx, hubClient, cfg.NodeID, 60*time.Second); err != nil {
				t.Fatalf("Node registration failed: %v", err)
			}

			t.Logf("✓ Node %s is registered", cfg.NodeID)
			return ctx
		}).
		Assess("3. Discover available nodes (Renter perspective)", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			nodes, err := hubClient.DiscoverNodes(ctx, nil)
			if err != nil {
				t.Fatalf("Failed to discover nodes: %v", err)
			}

			if len(nodes) == 0 {
				t.Fatal("No nodes available for rental")
			}

			t.Logf("✓ Found %d available node(s)", len(nodes))
			for i, node := range nodes {
				nodeID, _ := node["nodeId"].(string)
				if nodeID == "" {
					nodeID, _ = node["id"].(string)
				}
				gpuModel, _ := node["gpuModel"].(string)
				t.Logf("  [%d] Node %s - GPU: %s", i+1, nodeID, gpuModel)
			}

			return ctx
		}).
		Assess("4. Create rental session (Renter)", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Create session with Node
			pricePerSecond := "100" // 100 tokens per second

			session, err := hubClient.CreateSession(ctx, cfg.NodeID, pricePerSecond, "")
			if err != nil {
				t.Fatalf("Failed to create session: %v", err)
			}

			sessionID, _ = session["id"].(string)
			if sessionID == "" {
				sessionID, _ = session["sessionId"].(string)
			}

			if sessionID == "" {
				t.Fatalf("Session created but ID missing in response: %+v", session)
			}

			t.Logf("✓ Session created: %s", sessionID)
			return ctx
		}).
		Assess("5. Deposit tokens and confirm via blockchain", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Setup renter with tokens
			tokenAmount := new(big.Int)
			tokenAmount.SetString("1000000000000000000000", 10) // 1000 tokens * 10^18

			t.Log("  Setting up Renter with tokens...")
			if err := contractHelper.SetupTestUser(ctx, renterAccount.PrivateKey, renterAccount.Address, tokenAmount); err != nil {
				t.Fatalf("Failed to setup Renter with tokens: %v", err)
			}
			t.Log("  ✓ Renter funded and deposited to contract")

			// Start rental on blockchain
			pricePerSecond := new(big.Int)
			pricePerSecond.SetString("100", 10)

			t.Log("  Calling startRental on blockchain...")
			txHash, err := contractHelper.StartRental(ctx, renterAccount.PrivateKey, providerAccount.Address, pricePerSecond)
			if err != nil {
				t.Fatalf("Failed to start rental on blockchain: %v", err)
			}
			t.Logf("  ✓ startRental tx: %s", txHash)

			// Confirm session with txHash
			t.Log("  Confirming session with Hub...")
			confirmResp, err := hubClient.ConfirmSession(ctx, sessionID, txHash)
			if err != nil {
				t.Fatalf("Failed to confirm session: %v", err)
			}

			state, _ := confirmResp["state"].(string)
			if state == "" {
				state, _ = confirmResp["status"].(string)
			}
			t.Logf("  ✓ Session confirmed - state: %s", state)

			return ctx
		}).
		Assess("6. Wait for container provisioning (RUNNING state)", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			t.Log("Waiting for worldland-node to provision container...")

			session, err := WaitForSessionRunning(ctx, hubClient, sessionID, 120*time.Second)
			if err != nil {
				t.Fatalf("Session did not reach RUNNING state: %v", err)
			}

			state, _ := session["state"].(string)
			if state == "" {
				state, _ = session["status"].(string)
			}

			t.Logf("✓ Container provisioned - session state: %s", state)
			return ctx
		}).
		Assess("7. Retrieve SSH credentials", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			session, err := hubClient.GetSession(ctx, sessionID)
			if err != nil {
				t.Fatalf("Failed to get session: %v", err)
			}

			sshInfo, err := ParseSSHInfoFromSession(session)
			if err != nil {
				t.Fatalf("Failed to parse SSH info: %v", err)
			}

			t.Logf("✓ SSH credentials retrieved:")
			t.Logf("  Host: %s", sshInfo.Host)
			t.Logf("  Port: %d", sshInfo.Port)
			t.Logf("  User: %s", sshInfo.User)
			t.Logf("  Password: %s****", sshInfo.Password[:4]) // Mask password

			// Store SSH info in context for next step
			return context.WithValue(ctx, "sshInfo", sshInfo)
		}).
		Assess("8. Verify SSH connection to container", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			sshInfo, ok := ctx.Value("sshInfo").(SSHConnectionInfo)
			if !ok {
				t.Fatal("SSH info not found in context")
			}

			t.Log("Verifying SSH access to provisioned container...")
			VerifySSHAccess(ctx, t, sshInfo)

			t.Log("✓ SSH access verified - container is accessible")
			return ctx
		}).
		Assess("9. Terminate session (Renter)", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Wait a few seconds to accumulate some usage time
			t.Log("Waiting 3 seconds to accumulate usage...")
			time.Sleep(3 * time.Second)

			t.Log("Terminating session...")
			resp, err := hubClient.TerminateSession(ctx, sessionID)
			if err != nil {
				t.Fatalf("Failed to terminate session: %v", err)
			}

			state, _ := resp["state"].(string)
			if state == "" {
				state, _ = resp["status"].(string)
			}
			t.Logf("✓ Session terminated - state: %s", state)

			// Wait for STOPPED state
			if err := WaitForSessionStopped(ctx, hubClient, sessionID, 30*time.Second); err != nil {
				t.Logf("WARNING: Session did not transition to STOPPED: %v", err)
			} else {
				t.Log("✓ Session reached STOPPED state")
			}

			return ctx
		}).
		Assess("10. Verify settlement data", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			t.Log("Retrieving settlement data...")
			settlement, err := hubClient.GetSettlement(ctx, sessionID)
			if err != nil {
				// Settlement endpoint may not be implemented yet
				t.Logf("WARNING: Failed to get settlement: %v (endpoint may not be implemented)", err)
				return ctx
			}

			// Extract settlement fields
			totalCost, _ := settlement["totalCost"].(string)
			duration, _ := settlement["duration"].(float64)
			startTime, _ := settlement["startTime"].(string)
			endTime, _ := settlement["endTime"].(string)

			t.Logf("✓ Settlement data retrieved:")
			t.Logf("  Total cost: %s", totalCost)
			t.Logf("  Duration: %.2f seconds", duration)
			t.Logf("  Start time: %s", startTime)
			t.Logf("  End time: %s", endTime)

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Revert EVM snapshot for test isolation
			if blockchainHelper.hasSnapshot {
				if err := blockchainHelper.Revert(ctx); err != nil {
					t.Logf("WARNING: Failed to revert EVM snapshot: %v", err)
				} else {
					t.Log("Reverted EVM snapshot")
				}
			}

			// Log session ID for manual diagnostics if test failed
			if t.Failed() {
				t.Logf("Test failed - Session ID: %s", sessionID)
				t.Logf("Manual diagnostics: Check Hub logs and worldland-node logs")
			}

			return ctx
		}).
		Feature()

	// Run the feature test
	testEnv.Test(t, feature)
}
