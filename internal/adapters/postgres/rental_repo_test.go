package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/domain"
)

// testConfig returns configuration for test database
func testConfig() Config {
	return Config{
		Host:     "localhost",
		Port:     5432,
		User:     "test",
		Password: "test",
		Database: "worldland_test",
		MaxConns: 10,
	}
}

// setupRentalTestRepo creates a repository with test database connection
func setupRentalTestRepo(t *testing.T) (*RentalSessionRepository, func()) {
	ctx := context.Background()
	cfg := testConfig()

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return nil, nil
	}

	repo := NewRentalSessionRepository(pool)

	cleanup := func() {
		// Clean up test data
		_, _ = pool.Exec(ctx, "DELETE FROM rental_sessions WHERE user_address LIKE 'test-%'")
		pool.Close()
	}

	return repo, cleanup
}

// createTestSession creates a RentalSession for testing
func createTestSession(userSuffix string) *domain.RentalSession {
	now := time.Now()
	return &domain.RentalSession{
		ID:              uuid.New().String(),
		UserAddress:     "test-" + userSuffix + "-user",
		ProviderAddress: "test-" + userSuffix + "-provider",
		NodeID:          uuid.New().String(),
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000", // 0.001 ETH in Wei
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestRentalSessionRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a test session
	session := createTestSession("create")

	err := repo.Create(ctx, session)
	require.NoError(t, err, "Create should succeed")

	// Verify ID and timestamps were set
	assert.NotEmpty(t, session.ID, "ID should be set")
	assert.False(t, session.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, session.UpdatedAt.IsZero(), "UpdatedAt should be set")
	assert.Equal(t, domain.RentalStatePending, session.State, "State should be PENDING")
}

func TestRentalSessionRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a session first
	original := createTestSession("getbyid")
	err := repo.Create(ctx, original)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, original.ID)
	require.NoError(t, err, "GetByID should succeed")

	assert.Equal(t, original.ID, retrieved.ID)
	assert.Equal(t, original.UserAddress, retrieved.UserAddress)
	assert.Equal(t, original.ProviderAddress, retrieved.ProviderAddress)
	assert.Equal(t, original.NodeID, retrieved.NodeID)
	assert.Equal(t, original.State, retrieved.State)
	assert.Equal(t, original.PricePerSecond, retrieved.PricePerSecond)
}

func TestRentalSessionRepository_GetByID_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Try to get a non-existent session
	_, err := repo.GetByID(ctx, uuid.New().String())
	assert.Error(t, err, "GetByID should fail for non-existent ID")
	assert.Contains(t, err.Error(), "not found")
}

func TestRentalSessionRepository_GetByRentalID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a session with a rental ID
	session := createTestSession("getbyrentalid")
	rentalID := uint64(12345)
	session.RentalID = &rentalID
	err := repo.Create(ctx, session)
	require.NoError(t, err)

	// Retrieve by rental ID
	retrieved, err := repo.GetByRentalID(ctx, rentalID)
	require.NoError(t, err, "GetByRentalID should succeed")

	assert.Equal(t, session.ID, retrieved.ID)
	assert.Equal(t, rentalID, *retrieved.RentalID)
}

func TestRentalSessionRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a session
	session := createTestSession("update")
	err := repo.Create(ctx, session)
	require.NoError(t, err)

	// Update the session to RUNNING
	rentalID := uint64(67890)
	startTime := time.Now()
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	blockNumber := uint64(100)

	session.RentalID = &rentalID
	session.State = domain.RentalStateRunning
	session.StartTime = &startTime
	session.TxHash = &txHash
	session.BlockNumber = &blockNumber

	err = repo.Update(ctx, session)
	require.NoError(t, err, "Update should succeed")

	// Verify the update
	retrieved, err := repo.GetByID(ctx, session.ID)
	require.NoError(t, err)

	assert.Equal(t, domain.RentalStateRunning, retrieved.State)
	assert.Equal(t, rentalID, *retrieved.RentalID)
	assert.Equal(t, txHash, *retrieved.TxHash)
	assert.Equal(t, blockNumber, *retrieved.BlockNumber)
	assert.NotNil(t, retrieved.StartTime)
}

func TestRentalSessionRepository_Update_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Try to update a non-existent session
	session := createTestSession("update-notfound")
	err := repo.Update(ctx, session)
	assert.Error(t, err, "Update should fail for non-existent session")
	assert.Contains(t, err.Error(), "not found")
}

func TestRentalSessionRepository_ListByUser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create multiple sessions for the same user
	userAddress := "test-listbyuser-user"
	for i := 0; i < 3; i++ {
		session := createTestSession("listbyuser")
		session.UserAddress = userAddress
		err := repo.Create(ctx, session)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond) // Ensure different created_at
	}

	// List sessions
	sessions, err := repo.ListByUser(ctx, userAddress, 10, 0)
	require.NoError(t, err, "ListByUser should succeed")

	assert.GreaterOrEqual(t, len(sessions), 3, "Should have at least 3 sessions")

	// Verify all returned sessions belong to the user
	for _, s := range sessions {
		assert.Equal(t, userAddress, s.UserAddress)
	}
}

func TestRentalSessionRepository_ListByUser_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create 5 sessions
	userAddress := "test-pagination-user"
	for i := 0; i < 5; i++ {
		session := createTestSession("pagination")
		session.UserAddress = userAddress
		err := repo.Create(ctx, session)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}

	// First page
	page1, err := repo.ListByUser(ctx, userAddress, 2, 0)
	require.NoError(t, err)
	assert.Len(t, page1, 2, "First page should have 2 sessions")

	// Second page
	page2, err := repo.ListByUser(ctx, userAddress, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 2, "Second page should have 2 sessions")

	// Verify pages don't overlap
	assert.NotEqual(t, page1[0].ID, page2[0].ID, "Pages should not overlap")
}

func TestRentalSessionRepository_ListByState(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create sessions with different states
	pendingSession := createTestSession("listbystate-pending")
	pendingSession.State = domain.RentalStatePending
	err := repo.Create(ctx, pendingSession)
	require.NoError(t, err)

	runningSession := createTestSession("listbystate-running")
	runningSession.State = domain.RentalStateRunning
	startTime := time.Now()
	runningSession.StartTime = &startTime
	err = repo.Create(ctx, runningSession)
	require.NoError(t, err)

	// List PENDING sessions
	pendingSessions, err := repo.ListByState(ctx, domain.RentalStatePending, 100, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(pendingSessions), 1, "Should have at least 1 PENDING session")

	// Verify all returned are PENDING
	for _, s := range pendingSessions {
		assert.Equal(t, domain.RentalStatePending, s.State)
	}
}

func TestRentalSessionRepository_FindStale(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a "stale" session by manually setting created_at in the past
	staleSession := createTestSession("findstale")
	staleSession.State = domain.RentalStatePending
	err := repo.Create(ctx, staleSession)
	require.NoError(t, err)

	// Manually update created_at to 10 minutes ago
	_, err = repo.pool.Exec(ctx,
		"UPDATE rental_sessions SET created_at = $1 WHERE id = $2",
		time.Now().Add(-10*time.Minute), staleSession.ID)
	require.NoError(t, err)

	// Find stale PENDING sessions (older than 5 minutes)
	staleSessions, err := repo.FindStale(ctx, domain.RentalStatePending, 5*time.Minute)
	require.NoError(t, err, "FindStale should succeed")

	// Should find our stale session
	found := false
	for _, s := range staleSessions {
		if s.ID == staleSession.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "Should find the stale session")
}

func TestRentalSessionRepository_FindStale_NoResults(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create a fresh session
	freshSession := createTestSession("findstale-fresh")
	freshSession.State = domain.RentalStatePending
	err := repo.Create(ctx, freshSession)
	require.NoError(t, err)

	// Find stale sessions (older than 1 hour) - should not include fresh session
	staleSessions, err := repo.FindStale(ctx, domain.RentalStatePending, 1*time.Hour)
	require.NoError(t, err)

	// Should not find the fresh session
	for _, s := range staleSessions {
		assert.NotEqual(t, freshSession.ID, s.ID, "Fresh session should not be found")
	}
}

func TestRentalSessionRepository_ListByProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupRentalTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create sessions for a specific provider
	providerAddress := "test-listbyprovider-provider"
	for i := 0; i < 2; i++ {
		session := createTestSession("listbyprovider")
		session.ProviderAddress = providerAddress
		err := repo.Create(ctx, session)
		require.NoError(t, err)
	}

	// List by provider
	sessions, err := repo.ListByProvider(ctx, providerAddress, 10, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(sessions), 2, "Should have at least 2 sessions")

	// Verify all belong to provider
	for _, s := range sessions {
		assert.Equal(t, providerAddress, s.ProviderAddress)
	}
}
