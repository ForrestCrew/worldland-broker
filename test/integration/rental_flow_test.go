//go:build integration

// Package integration_test provides integration tests for the rental execution flow.
// These tests validate the complete rental lifecycle from session creation through
// Hub-to-Node orchestration, verifying Phase 4 rental execution criteria.
package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/worldland/worldland-hub/internal/adapters/postgres"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/rental"
	"github.com/worldland/worldland-hub/internal/sessions"
	"github.com/worldland/worldland-hub/internal/settlement"
)

// TestRentalFlow_StartToStop validates the full rental execution flow:
// 1. Session created in PENDING state (via SessionManager)
// 2. Hub calls Node /rentals/start endpoint
// 3. Session transitions to RUNNING
// 4. Hub calls Node /rentals/stop endpoint
// 5. Session transitions to STOPPED
// 6. Settlement processed
func TestRentalFlow_StartToStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	dbPool := setupTestDB(t)
	defer dbPool.Close()

	// Initialize repositories
	providerRepo := postgres.NewProviderRepository(dbPool)
	nodeRepo := postgres.NewNodeRepository(dbPool)
	rentalSessionRepo := postgres.NewRentalSessionRepository(dbPool)

	// Create test provider and node
	provider, node := createTestProviderAndNode(t, providerRepo, nodeRepo)
	defer cleanupTestData(t, dbPool, provider.ID, node.ID)

	userAddress := "0x" + uuid.New().String()[:40]

	// Mock Node server that simulates GPU container lifecycle
	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rentals/start":
			// Simulate successful container start
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"sessionId": "test-session",
				"sshHost": "192.168.1.100",
				"sshPort": 2222,
				"sshUser": "rental",
				"sshCommand": "ssh -p 2222 rental@192.168.1.100"
			}`))
		case "/rentals/stop":
			// Simulate successful container stop
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"sessionId": "test-session",
				"message": "Container stopped successfully"
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer nodeServer.Close()

	// Update node with mock API endpoint
	node.APIEndpoint = nodeServer.URL
	err := nodeRepo.Update(ctx, node)
	require.NoError(t, err)

	// Initialize services
	sessionManager := sessions.NewSessionManager(rentalSessionRepo, nodeRepo, providerRepo)
	nodeClient := rental.NewNodeClient(rental.NodeClientConfig{
		Timeout: 30 * time.Second,
	})
	logger := testLogger()
	calculator := settlement.NewCalculator(rentalSessionRepo)
	batchProcessor := settlement.NewBatchProcessor(calculator, rentalSessionRepo, logger)

	// === STEP 1: Create rental session ===
	session, err := sessionManager.CreateSession(ctx, userAddress, node.ID, node.PricePerSecond, "", nil)
	require.NoError(t, err)
	assert.Equal(t, domain.RentalStatePending, session.State)

	// === STEP 2: Simulate blockchain RentalStarted event ===
	rentalID := uint64(123)
	blockNumber := uint64(1000)
	txHash := "0x" + uuid.New().String()[:64]
	startTime := time.Now()

	err = sessionManager.TransitionToRunning(ctx, session.ID, rentalID, blockNumber, txHash, startTime)
	require.NoError(t, err)

	// === STEP 3: Call Node to start rental (Hub-to-Node orchestration) ===
	startReq := rental.StartRentalRequest{
		SessionID:    session.ID,
		GPUDeviceID:  node.GPUUUID,
		SSHPublicKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...",
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
		MemoryBytes:  8 * 1024 * 1024 * 1024,
		CPUCount:     4,
	}

	startResp, err := nodeClient.StartRental(ctx, node.APIEndpoint, startReq)
	require.NoError(t, err)
	assert.NotEmpty(t, startResp.SSHHost)
	assert.Equal(t, 2222, startResp.SSHPort)
	assert.Equal(t, "rental", startResp.SSHUser)

	// === STEP 4: Simulate some rental usage time ===
	time.Sleep(100 * time.Millisecond) // Simulate rental duration

	// === STEP 5: Call Node to stop rental ===
	stopReq := rental.StopRentalRequest{
		SessionID: session.ID,
	}

	stopResp, err := nodeClient.StopRental(ctx, node.APIEndpoint, stopReq)
	require.NoError(t, err)
	assert.NotEmpty(t, stopResp.Message)

	// === STEP 6: Simulate blockchain RentalStopped event ===
	endTime := time.Now()
	stopTxHash := "0x" + uuid.New().String()[:64]
	stopBlockNumber := uint64(1100)
	totalCost := "1000000000000000" // 0.001 ETH

	err = sessionManager.TransitionToStopped(ctx, session.ID, endTime, totalCost, stopTxHash, stopBlockNumber)
	require.NoError(t, err)

	// Verify session is STOPPED
	finalSession, err := rentalSessionRepo.GetByID(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RentalStateStopped, finalSession.State)
	assert.NotNil(t, finalSession.StartTime)
	assert.NotNil(t, finalSession.EndTime)

	// === STEP 7: Process settlement ===
	err = batchProcessor.ProcessBatch(ctx)
	require.NoError(t, err)

	// Verify settlement was recorded
	settledSession, err := rentalSessionRepo.GetByID(ctx, session.ID)
	require.NoError(t, err)
	assert.NotNil(t, settledSession.SettledAt, "Settlement should be recorded")
	assert.NotEmpty(t, settledSession.SettledAmount, "Settlement amount should be calculated")
}

// TestRentalFlow_NodeUnreachable validates error handling when Node is unreachable
func TestRentalFlow_NodeUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	dbPool := setupTestDB(t)
	defer dbPool.Close()

	// Initialize repositories
	providerRepo := postgres.NewProviderRepository(dbPool)
	nodeRepo := postgres.NewNodeRepository(dbPool)

	// Create test provider and node
	provider, node := createTestProviderAndNode(t, providerRepo, nodeRepo)
	defer cleanupTestData(t, dbPool, provider.ID, node.ID)

	// Set invalid API endpoint (unreachable)
	node.APIEndpoint = "http://192.0.2.1:9999" // TEST-NET-1 (RFC 5737)
	err := nodeRepo.Update(ctx, node)
	require.NoError(t, err)

	// Initialize NodeClient with short timeout for faster test
	nodeClient := rental.NewNodeClient(rental.NodeClientConfig{
		Timeout: 2 * time.Second,
	})

	// Attempt to start rental on unreachable node
	startReq := rental.StartRentalRequest{
		SessionID:    "test-session-id",
		GPUDeviceID:  node.GPUUUID,
		SSHPublicKey: "ssh-rsa AAAAB3...",
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04",
	}

	_, err = nodeClient.StartRental(ctx, node.APIEndpoint, startReq)
	assert.Error(t, err)
	assert.ErrorIs(t, err, rental.ErrNodeUnreachable, "Should return ErrNodeUnreachable for network errors")
}

// TestRentalFlow_NodeErrorResponse validates error handling when Node returns error
func TestRentalFlow_NodeErrorResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Mock Node server that returns error responses
	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rentals/start" {
			// Simulate Node error (e.g., GPU already in use)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{
				"error": "GPU device already allocated",
				"code": "GPU_IN_USE"
			}`))
		}
	}))
	defer nodeServer.Close()

	// Initialize NodeClient
	nodeClient := rental.NewNodeClient(rental.NodeClientConfig{
		Timeout: 10 * time.Second,
	})

	// Attempt to start rental on busy GPU
	startReq := rental.StartRentalRequest{
		SessionID:    "test-session-id",
		GPUDeviceID:  "GPU-TEST-123",
		SSHPublicKey: "ssh-rsa AAAAB3...",
	}

	_, err := nodeClient.StartRental(ctx, nodeServer.URL, startReq)
	assert.Error(t, err)
	assert.ErrorIs(t, err, rental.ErrNodeStartFailed, "Should return ErrNodeStartFailed for Node errors")
	assert.Contains(t, err.Error(), "GPU device already allocated")
}

// TestRentalFlow_ConcurrentRentals validates multiple concurrent rental sessions
func TestRentalFlow_ConcurrentRentals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	dbPool := setupTestDB(t)
	defer dbPool.Close()

	// Initialize repositories
	providerRepo := postgres.NewProviderRepository(dbPool)
	nodeRepo := postgres.NewNodeRepository(dbPool)
	rentalSessionRepo := postgres.NewRentalSessionRepository(dbPool)

	// Create multiple test nodes
	provider, node1 := createTestProviderAndNode(t, providerRepo, nodeRepo)
	defer cleanupTestData(t, dbPool, provider.ID, node1.ID)

	node2 := &domain.Node{
		ID:             uuid.New().String(),
		ProviderID:     provider.ID,
		GPUUUID:        "GPU-TEST-" + uuid.New().String()[:8],
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "1000000000000000",
		Status:         domain.NodeStatusActive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := nodeRepo.Create(ctx, node2)
	require.NoError(t, err)
	defer func() {
		_, _ = dbPool.Exec(ctx, "DELETE FROM nodes WHERE id = $1", node2.ID)
	}()

	// Initialize services
	sessionManager := sessions.NewSessionManager(rentalSessionRepo, nodeRepo, providerRepo)

	// Create concurrent sessions
	user1 := "0x" + uuid.New().String()[:40]
	user2 := "0x" + uuid.New().String()[:40]

	session1, err := sessionManager.CreateSession(ctx, user1, node1.ID, node1.PricePerSecond, "", nil)
	require.NoError(t, err)

	session2, err := sessionManager.CreateSession(ctx, user2, node2.ID, node2.PricePerSecond, "", nil)
	require.NoError(t, err)

	// Both sessions should be independent
	assert.NotEqual(t, session1.ID, session2.ID)
	assert.Equal(t, domain.RentalStatePending, session1.State)
	assert.Equal(t, domain.RentalStatePending, session2.State)

	// Transition session1 to RUNNING
	err = sessionManager.TransitionToRunning(ctx, session1.ID, 1, 1000, "0x"+uuid.New().String()[:64], time.Now())
	require.NoError(t, err)

	// Session2 should still be PENDING (independent state)
	session2Updated, err := rentalSessionRepo.GetByID(ctx, session2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RentalStatePending, session2Updated.State)
}

// TestRentalFlow_SettlementBatch validates batch settlement processing
func TestRentalFlow_SettlementBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	dbPool := setupTestDB(t)
	defer dbPool.Close()

	// Initialize repositories
	providerRepo := postgres.NewProviderRepository(dbPool)
	nodeRepo := postgres.NewNodeRepository(dbPool)
	rentalSessionRepo := postgres.NewRentalSessionRepository(dbPool)

	// Create test provider and node
	provider, node := createTestProviderAndNode(t, providerRepo, nodeRepo)
	defer cleanupTestData(t, dbPool, provider.ID, node.ID)

	// Initialize services
	sessionManager := sessions.NewSessionManager(rentalSessionRepo, nodeRepo, providerRepo)
	logger := testLogger()
	calculator := settlement.NewCalculator(rentalSessionRepo)
	batchProcessor := settlement.NewBatchProcessor(calculator, rentalSessionRepo, logger)

	// Create multiple STOPPED sessions awaiting settlement
	user := "0x" + uuid.New().String()[:40]

	// Session 1: 10 minutes rental
	session1, err := sessionManager.CreateSession(ctx, user, node.ID, node.PricePerSecond, "", nil)
	require.NoError(t, err)
	start1 := time.Now().Add(-20 * time.Minute)
	end1 := time.Now().Add(-10 * time.Minute)
	err = sessionManager.TransitionToRunning(ctx, session1.ID, 1, 1000, "0x"+uuid.New().String()[:64], start1)
	require.NoError(t, err)
	err = sessionManager.TransitionToStopped(ctx, session1.ID, end1, "600000000000000000", "0x"+uuid.New().String()[:64], 1100)
	require.NoError(t, err)

	// Session 2: 5 minutes rental
	session2, err := sessionManager.CreateSession(ctx, user, node.ID, node.PricePerSecond, "", nil)
	require.NoError(t, err)
	start2 := time.Now().Add(-15 * time.Minute)
	end2 := time.Now().Add(-10 * time.Minute)
	err = sessionManager.TransitionToRunning(ctx, session2.ID, 2, 1200, "0x"+uuid.New().String()[:64], start2)
	require.NoError(t, err)
	err = sessionManager.TransitionToStopped(ctx, session2.ID, end2, "300000000000000000", "0x"+uuid.New().String()[:64], 1300)
	require.NoError(t, err)

	// Process batch settlement
	err = batchProcessor.ProcessBatch(ctx)
	require.NoError(t, err)

	// Verify both sessions were settled
	settled1, err := rentalSessionRepo.GetByID(ctx, session1.ID)
	require.NoError(t, err)
	assert.NotNil(t, settled1.SettledAt)
	assert.NotEmpty(t, settled1.SettledAmount)

	settled2, err := rentalSessionRepo.GetByID(ctx, session2.ID)
	require.NoError(t, err)
	assert.NotNil(t, settled2.SettledAt)
	assert.NotEmpty(t, settled2.SettledAmount)
}

