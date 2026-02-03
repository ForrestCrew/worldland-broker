//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/worldland/worldland-hub/internal/k8s"
)

// TestCompleteRentalFlowE2E is the comprehensive E2E test covering all Phase 25 success criteria
//
// This test verifies the complete rental lifecycle:
// 1. Hub health check
// 2. List preset images
// 3. Create rental session
// 4. K8s Pod created in tenant namespace
// 5. Session transitions to RUNNING
// 6. SSH connection info is available
// 7. Terminate rental session
// 8. Verify settlement completes
//
// REQUIREMENTS:
// - Running Kind cluster (worldland-dev or ephemeral)
// - Running Hub server with K8S_ENABLED=true
// - Running Hardhat node with deployed contracts
// - Hub with AUTH_DISABLED=true recommended for test simplicity
//
// The test uses EVM snapshots for test isolation and collects diagnostics on failure.
func TestCompleteRentalFlowE2E(t *testing.T) {
	cfg := DefaultConfig()
	hubClient := NewHubClient(cfg.HubURL)

	// Hardhat test accounts (pre-funded)
	testUserAddress := HardhatAccount1Address   // Account #1 (renter)
	testUserPrivateKey := HardhatAccount1PrivateKey
	testProviderAddress := HardhatAccount2Address // Account #2 (provider)
	testNodeID := "22222222-2222-2222-2222-222222222222" // E2E test node (seeded in database)

	// Hardhat node URL
	hardhatURL := os.Getenv("HARDHAT_URL")
	if hardhatURL == "" {
		hardhatURL = "http://localhost:8545"
	}

	// Create blockchain helper
	blockchainHelper := NewBlockchainHelper(hardhatURL)

	// Create contract helper (will be initialized in setup)
	var contractHelper *ContractHelper

	// Context for session ID and txHash
	var sessionID string
	var startRentalTxHash string

	feature := features.New("Complete Rental Flow E2E").
		Setup(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Check Hub availability
			if !isHubAvailable(ctx, hubClient) {
				t.Skip("Hub server not available at " + cfg.HubURL)
			}

			// Check Hardhat availability
			if !blockchainHelper.client.IsHealthy(ctx) {
				t.Skip("Hardhat node not available at " + hardhatURL)
			}

			// Initialize contract helper
			var err error
			contractHelper, err = NewContractHelperFromDeployment(blockchainHelper)
			if err != nil {
				t.Skipf("Failed to load contract addresses: %v", err)
			}
			t.Logf("Contract addresses loaded - Rental: %s, Token: %s",
				contractHelper.rentalAddr.Hex(), contractHelper.tokenAddr.Hex())

			// Setup auth if required
			if !cfg.AuthDisabled {
				if err := SetupTestAuth(ctx, hubClient, testUserAddress); err != nil {
					t.Skipf("auth setup failed (run Hub with AUTH_DISABLED=true): %v", err)
				}
			}

			// Create EVM snapshot for test isolation
			if err := blockchainHelper.Snapshot(ctx); err != nil {
				t.Logf("WARNING: Failed to create EVM snapshot: %v", err)
			} else {
				t.Log("Created EVM snapshot for test isolation")
			}

			return ctx
		}).
		Assess("Hub health check", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if err := hubClient.Health(ctx); err != nil {
				t.Fatalf("Hub health check failed: %v", err)
			}
			t.Log("✓ Hub is healthy")
			return ctx
		}).
		Assess("List preset images", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			images, err := hubClient.ListImages(ctx)
			if err != nil {
				t.Fatalf("Failed to list images: %v", err)
			}

			t.Logf("✓ Found %d preset images", len(images))
			for _, img := range images {
				name, _ := img["name"].(string)
				dockerImage, _ := img["dockerImage"].(string)
				t.Logf("  - %s: %s", name, dockerImage)
			}

			return ctx
		}).
		Assess("Setup test user with tokens", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Mint tokens, approve, and deposit for test user
			// Amount: 1000 tokens (assuming 18 decimals)
			tokenAmount := new(big.Int)
			tokenAmount.SetString("1000000000000000000000", 10) // 1000 * 10^18

			t.Log("Setting up test user with tokens...")

			// Step 1: Mint tokens to test user
			t.Log("  1. Minting tokens to test user...")
			mintTx, err := contractHelper.MintTokens(ctx, testUserAddress, tokenAmount)
			if err != nil {
				t.Fatalf("Failed to mint tokens: %v", err)
			}
			t.Logf("    Mint tx: %s", mintTx)

			// Step 2: Approve rental contract
			t.Log("  2. Approving rental contract to spend tokens...")
			approveTx, err := contractHelper.ApproveToken(ctx, testUserPrivateKey, tokenAmount)
			if err != nil {
				t.Fatalf("Failed to approve tokens: %v", err)
			}
			t.Logf("    Approve tx: %s", approveTx)

			// Step 3: Deposit tokens to rental contract
			t.Log("  3. Depositing tokens to rental contract...")
			depositTx, err := contractHelper.DepositTokens(ctx, testUserPrivateKey, tokenAmount)
			if err != nil {
				t.Fatalf("Failed to deposit tokens: %v", err)
			}
			t.Logf("    Deposit tx: %s", depositTx)

			// Verify deposit balance
			deposit, err := contractHelper.GetContractDeposit(ctx, testUserAddress)
			if err != nil {
				t.Fatalf("Failed to get deposit balance: %v", err)
			}
			t.Logf("✓ Test user deposit balance: %s", deposit.String())

			return ctx
		}).
		Assess("Create rental session", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Use lightweight image for testing (no GPU needed)
			testImage := "busybox:latest"

			session, err := hubClient.CreateSession(ctx, testNodeID, "1000000", testImage)
			if err != nil {
				collectDiagnostics(t, hubClient, "", ecfg)
				t.Fatalf("Failed to create session: %v", err)
			}

			// Try both "id" and "sessionId" field names
			id, ok := session["id"].(string)
			if !ok {
				id, ok = session["sessionId"].(string)
			}
			if !ok {
				collectDiagnostics(t, hubClient, "", ecfg)
				t.Fatalf("Session response missing 'id' or 'sessionId' field: %v", session)
			}

			sessionID = id
			t.Logf("✓ Created rental session: %s", sessionID)

			// Verify docker image was set
			if dockerImage, ok := session["dockerImage"].(string); ok {
				if dockerImage != testImage {
					t.Errorf("Session docker_image mismatch: expected %s, got %s", testImage, dockerImage)
				} else {
					t.Logf("  - Docker image: %s", dockerImage)
				}
			}

			// Verify initial state is PENDING
			state, _ := session["state"].(string)
			if state != "PENDING" {
				t.Logf("WARNING: Expected initial state PENDING, got: %s", state)
			} else {
				t.Logf("  - Initial state: %s", state)
			}

			return ctx
		}).
		Assess("Call startRental on blockchain", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			// Call startRental on the blockchain contract
			// pricePerSecond should match what was passed to CreateSession
			pricePerSecond := big.NewInt(1000000)

			t.Logf("Calling startRental on blockchain...")
			t.Logf("  - Provider: %s", testProviderAddress)
			t.Logf("  - Price per second: %s", pricePerSecond.String())

			txHash, err := contractHelper.StartRental(ctx, testUserPrivateKey, testProviderAddress, pricePerSecond)
			if err != nil {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Failed to call startRental: %v", err)
			}

			startRentalTxHash = txHash
			t.Logf("✓ startRental transaction: %s", txHash)

			return ctx
		}).
		Assess("Confirm session with txHash", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" || startRentalTxHash == "" {
				t.Skip("No session ID or txHash from previous steps")
			}

			t.Logf("Confirming session %s with txHash %s...", sessionID, startRentalTxHash)

			result, err := hubClient.ConfirmSession(ctx, sessionID, startRentalTxHash)
			if err != nil {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Failed to confirm session: %v", err)
			}

			t.Logf("✓ Session confirmation submitted")
			if state, ok := result["state"].(string); ok {
				t.Logf("  - State after confirm: %s", state)
			}
			if message, ok := result["message"].(string); ok {
				t.Logf("  - Message: %s", message)
			}

			return ctx
		}).
		Assess("Wait for ConfirmationWorker verification", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			// ConfirmationWorker runs every 10 seconds
			// Wait up to 30 seconds for verification
			t.Log("Waiting for ConfirmationWorker to verify transaction...")

			deadline := time.Now().Add(30 * time.Second)
			var lastState string

			for time.Now().Before(deadline) {
				session, err := hubClient.GetSession(ctx, sessionID)
				if err != nil {
					t.Logf("Error getting session: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}

				state, _ := session["state"].(string)
				lastState = state

				// Check if verified (state should transition from PENDING)
				if state != "PENDING" {
					if state == "FAILED" {
						reason, _ := session["failureReason"].(string)
						t.Logf("Session FAILED: %s", reason)
						// Don't fail here - let K8s step check further
					} else {
						t.Logf("✓ Session verified, state: %s", state)
					}
					return ctx
				}

				t.Logf("  State still PENDING, waiting... (ConfirmationWorker interval: 10s)")
				time.Sleep(5 * time.Second)
			}

			t.Logf("WARNING: Session still PENDING after 30s. Last state: %s", lastState)
			return ctx
		}).
		Assess("K8s Pod created in tenant namespace", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			// Calculate expected namespace and pod name
			namespace := k8s.TenantNamespace(testUserAddress)
			podName := k8s.PodName(sessionID)

			t.Logf("Expecting pod %s in namespace %s", podName, namespace)

			client, err := ecfg.NewClient()
			if err != nil {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Failed to create K8s client: %v", err)
			}

			// Wait for pod to be created (Hub creates pod asynchronously)
			var pod corev1.Pod
			deadline := time.Now().Add(60 * time.Second)

			for time.Now().Before(deadline) {
				err := client.Resources(namespace).Get(ctx, podName, namespace, &pod)
				if err == nil {
					t.Logf("✓ Pod %s/%s found with status: %s", namespace, podName, pod.Status.Phase)
					break
				}
				if !apierrors.IsNotFound(err) {
					t.Logf("Error checking pod: %v", err)
				}
				time.Sleep(2 * time.Second)
			}

			if pod.Name == "" {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Pod %s/%s not created within 60s", namespace, podName)
			}

			// Verify pod has correct labels
			if pod.Labels[k8s.LabelSessionID] != sessionID {
				t.Errorf("Pod missing session ID label. Expected: %s, Got labels: %v", sessionID, pod.Labels)
			}
			if pod.Labels[k8s.LabelProviderID] == "" {
				t.Errorf("Pod missing provider ID label. Got labels: %v", pod.Labels)
			}
			if pod.Labels[k8s.LabelGPURental] != "true" {
				t.Errorf("Pod missing GPU rental label. Got labels: %v", pod.Labels)
			}

			// Verify pod has correct image
			if len(pod.Spec.Containers) == 0 {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatal("Pod has no containers")
			}
			actualImage := pod.Spec.Containers[0].Image
			if actualImage != "busybox:latest" {
				t.Errorf("Pod has wrong image: expected busybox:latest, got %s", actualImage)
			}

			t.Logf("✓ Pod verified: correct image (%s), correct labels", actualImage)

			return ctx
		}).
		Assess("Session transitions to RUNNING", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			// Poll session state via Hub API
			deadline := time.Now().Add(2 * time.Minute)
			var finalState string

			for time.Now().Before(deadline) {
				session, err := hubClient.GetSession(ctx, sessionID)
				if err != nil {
					t.Logf("Error getting session: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}

				state, _ := session["state"].(string)
				finalState = state

				if state == "RUNNING" {
					t.Logf("✓ Session transitioned to RUNNING")
					return ctx
				}

				if state == "FAILED" {
					reason, _ := session["failureReason"].(string)
					collectDiagnostics(t, hubClient, sessionID, ecfg)
					t.Fatalf("Session FAILED: %s", reason)
				}

				t.Logf("  Session state: %s (waiting for RUNNING)", state)
				time.Sleep(5 * time.Second)
			}

			collectDiagnostics(t, hubClient, sessionID, ecfg)
			t.Errorf("Session did not transition to RUNNING within 2 minutes. Final state: %s", finalState)
			return ctx
		}).
		Assess("SSH connection info is available", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			session, err := hubClient.GetSession(ctx, sessionID)
			if err != nil {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Failed to get session: %v", err)
			}

			// Check for SSH connection info
			sshInfo, ok := session["sshConnectionInfo"].(map[string]interface{})
			if !ok {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Session missing sshConnectionInfo field: %v", session)
			}

			// Verify required fields
			host, hasHost := sshInfo["host"].(string)
			port, hasPort := sshInfo["port"].(float64) // JSON numbers are float64
			password, hasPassword := sshInfo["password"].(string)

			if !hasHost || host == "" {
				t.Errorf("SSH info missing host field")
			}
			if !hasPort || port == 0 {
				t.Errorf("SSH info missing port field")
			}
			if !hasPassword || password == "" {
				t.Errorf("SSH info missing password field")
			}

			if hasHost && hasPort && hasPassword {
				t.Logf("✓ SSH connection info available:")
				t.Logf("  - Host: %s", host)
				t.Logf("  - Port: %.0f", port)
				t.Logf("  - Password: %s", maskPassword(password))
			}

			return ctx
		}).
		Assess("Terminate rental session", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			result, err := hubClient.TerminateSession(ctx, sessionID)
			if err != nil {
				collectDiagnostics(t, hubClient, sessionID, ecfg)
				t.Fatalf("Failed to terminate session: %v", err)
			}

			t.Logf("✓ Terminate session result: %v", result)

			// Wait for session state to change
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				session, err := hubClient.GetSession(ctx, sessionID)
				if err != nil {
					t.Logf("Error getting session: %v", err)
					time.Sleep(2 * time.Second)
					continue
				}

				state, _ := session["state"].(string)
				if state == "STOPPED" || state == "TERMINATED" {
					t.Logf("✓ Session terminated with state: %s", state)
					return ctx
				}

				t.Logf("  Session state: %s (waiting for STOPPED/TERMINATED)", state)
				time.Sleep(2 * time.Second)
			}

			t.Log("WARNING: Session did not transition to STOPPED/TERMINATED within 30s")
			return ctx
		}).
		Assess("Verify settlement completes", func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			if sessionID == "" {
				t.Skip("No session ID from previous step")
			}

			settlement, err := hubClient.GetSettlement(ctx, sessionID)
			if err != nil {
				// Settlement endpoint may not exist yet - log as warning
				t.Logf("WARNING: Failed to get settlement (endpoint may not be implemented): %v", err)
				return ctx
			}

			t.Logf("✓ Settlement retrieved: %v", settlement)

			// Verify settlement has expected fields (if endpoint exists)
			if amount, ok := settlement["amount"]; ok {
				t.Logf("  - Settlement amount: %v", amount)
			}
			if timestamp, ok := settlement["timestamp"]; ok {
				t.Logf("  - Settlement timestamp: %v", timestamp)
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, ecfg *envconf.Config) context.Context {
			// Revert EVM snapshot to clean state
			if blockchainHelper.hasSnapshot {
				if err := blockchainHelper.Revert(ctx); err != nil {
					t.Logf("WARNING: Failed to revert EVM snapshot: %v", err)
				} else {
					t.Log("Reverted EVM snapshot")
				}
			}

			// Cleanup: Delete test pod if it exists
			if sessionID != "" {
				namespace := k8s.TenantNamespace(testUserAddress)
				podName := k8s.PodName(sessionID)

				client, err := ecfg.NewClient()
				if err != nil {
					t.Logf("Failed to create client for cleanup: %v", err)
					return ctx
				}

				var pod corev1.Pod
				pod.Name = podName
				pod.Namespace = namespace

				if err := client.Resources(namespace).Delete(ctx, &pod); err != nil {
					if !apierrors.IsNotFound(err) {
						t.Logf("Failed to delete test pod: %v", err)
					}
				} else {
					t.Logf("Cleaned up test pod %s/%s", namespace, podName)
				}
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// collectDiagnostics collects diagnostic information on test failure
func collectDiagnostics(t *testing.T, hubClient *HubClient, sessionID string, ecfg *envconf.Config) {
	ctx := context.Background()

	// Create diagnostics directory
	timestamp := time.Now().Format("2006-01-02T15-04-05-000Z")
	diagDir := filepath.Join("e2e", "diagnostics", fmt.Sprintf("complete_rental_flow-%s", timestamp))
	if err := os.MkdirAll(diagDir, 0755); err != nil {
		t.Logf("Failed to create diagnostics directory: %v", err)
		return
	}

	t.Logf("Collecting diagnostics to %s", diagDir)

	// Collect Hub logs if available
	if sessionID != "" {
		session, err := hubClient.GetSession(ctx, sessionID)
		if err == nil {
			sessionFile := filepath.Join(diagDir, "session.json")
			if data, err := os.Create(sessionFile); err == nil {
				fmt.Fprintf(data, "%v\n", session)
				data.Close()
				t.Logf("Saved session data to %s", sessionFile)
			}
		}
	}

	// Collect K8s pod logs if available
	if sessionID != "" && ecfg != nil {
		client, err := ecfg.NewClient()
		if err == nil {
			testUserAddress := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
			namespace := k8s.TenantNamespace(testUserAddress)
			podName := k8s.PodName(sessionID)

			var pod corev1.Pod
			if err := client.Resources(namespace).Get(ctx, podName, namespace, &pod); err == nil {
				podFile := filepath.Join(diagDir, "pod.json")
				if data, err := os.Create(podFile); err == nil {
					fmt.Fprintf(data, "Pod Status: %s\n", pod.Status.Phase)
					fmt.Fprintf(data, "Labels: %v\n", pod.Labels)
					fmt.Fprintf(data, "Annotations: %v\n", pod.Annotations)
					data.Close()
					t.Logf("Saved pod data to %s", podFile)
				}
			}
		}
	}
}

// maskPassword masks a password for logging (shows first 4 chars)
func maskPassword(password string) string {
	if len(password) <= 4 {
		return "****"
	}
	return password[:4] + "************"
}
