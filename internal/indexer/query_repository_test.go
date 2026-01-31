package indexer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupQueryRepo creates a QueryRepository with test database connection.
func setupQueryRepo(t *testing.T) (*QueryRepository, *IndexerRepository, func()) {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testDatabaseURL())
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return nil, nil, nil
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("PostgreSQL not responding: %v", err)
		return nil, nil, nil
	}

	queryRepo := NewQueryRepository(pool)
	indexerRepo := NewIndexerRepository(pool)

	cleanup := func() {
		// Clean up test data
		_, _ = pool.Exec(ctx, "DELETE FROM deposit_withdraw_history WHERE user_address LIKE 'test-%'")
		_, _ = pool.Exec(ctx, "DELETE FROM rental_history WHERE user_address LIKE 'test-%'")
		pool.Close()
	}

	return queryRepo, indexerRepo, cleanup
}

// =============================================================================
// GetDepositWithdrawHistory Tests
// =============================================================================

func TestGetDepositWithdrawHistory_ReturnsOrderedResults(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-querydepositorder-0x1234567890abcdef1234567890abcdef12345678"

	// Insert 3 events with different timestamps
	baseTime := time.Now().Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		record := DepositWithdrawRecord{
			UserAddress:    userAddress,
			EventType:      "deposit",
			Amount:         "1000000000000000000",
			TxHash:         "0xqueryorder" + string(rune('a'+i)) + "1234567890123456789012345678901234567890123456",
			BlockNumber:    uint64(10000 + i),
			BlockTimestamp: baseTime.Add(time.Duration(i) * time.Hour), // i=0 oldest, i=2 newest
			LogIndex:       0,
		}
		err := indexerRepo.InsertDepositWithdraw(ctx, record)
		require.NoError(t, err)
	}

	// Query should return newest first (DESC ordering)
	response, err := queryRepo.GetDepositWithdrawHistory(ctx, userAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, response.Items, 3)

	// Verify ordering: newest timestamp first
	for i := 0; i < len(response.Items)-1; i++ {
		assert.True(t,
			response.Items[i].BlockTimestamp.After(response.Items[i+1].BlockTimestamp) ||
				response.Items[i].BlockTimestamp.Equal(response.Items[i+1].BlockTimestamp),
			"Items should be ordered by timestamp DESC")
	}
}

func TestGetDepositWithdrawHistory_CursorPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-querycursor-0x1234567890abcdef1234567890abcdef12345678"

	// Insert 5 events
	baseTime := time.Now().Add(-5 * time.Hour)
	for i := 0; i < 5; i++ {
		record := DepositWithdrawRecord{
			UserAddress:    userAddress,
			EventType:      "deposit",
			Amount:         "1000000000000000000",
			TxHash:         "0xcursor" + string(rune('a'+i)) + "123456789012345678901234567890123456789012345678",
			BlockNumber:    uint64(20000 + i),
			BlockTimestamp: baseTime.Add(time.Duration(i) * time.Hour),
			LogIndex:       0,
		}
		err := indexerRepo.InsertDepositWithdraw(ctx, record)
		require.NoError(t, err)
	}

	// First page: limit 2
	page1, err := queryRepo.GetDepositWithdrawHistory(ctx, userAddress, time.Time{}, 0, 2)
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	assert.True(t, page1.Page.HasMore)
	require.NotNil(t, page1.Page.NextCursor)

	// Parse cursor timestamp
	cursorTime, err := time.Parse(time.RFC3339Nano, page1.Page.NextCursor.AfterTimestamp)
	require.NoError(t, err)

	// Second page: use cursor
	page2, err := queryRepo.GetDepositWithdrawHistory(ctx, userAddress, cursorTime, page1.Page.NextCursor.AfterID, 2)
	require.NoError(t, err)
	require.Len(t, page2.Items, 2)
	assert.True(t, page2.Page.HasMore)

	// Verify no overlap between pages
	for _, p1Item := range page1.Items {
		for _, p2Item := range page2.Items {
			assert.NotEqual(t, p1Item.ID, p2Item.ID, "Pages should not have overlapping items")
		}
	}
}

func TestGetDepositWithdrawHistory_LimitEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, _, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-querylimit-0x1234567890abcdef1234567890abcdef12345678"

	// Request with limit > MaxLimit (100)
	response, err := queryRepo.GetDepositWithdrawHistory(ctx, userAddress, time.Time{}, 0, 200)
	require.NoError(t, err)
	assert.Equal(t, 100, response.Page.Limit, "Limit should be capped at MaxLimit (100)")

	// Request with limit = 0 should use DefaultLimit (50)
	response, err = queryRepo.GetDepositWithdrawHistory(ctx, userAddress, time.Time{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 50, response.Page.Limit, "Zero limit should use DefaultLimit (50)")
}

func TestGetDepositWithdrawHistory_AddressNormalized(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	// Store with lowercase (as repository does)
	lowerAddress := "test-querynormalize-0xabcdef1234567890abcdef1234567890abcdef12"

	record := DepositWithdrawRecord{
		UserAddress:    lowerAddress,
		EventType:      "deposit",
		Amount:         "1000000000000000000",
		TxHash:         "0xnormalize123456789012345678901234567890123456789012345678901234",
		BlockNumber:    30000,
		BlockTimestamp: time.Now(),
		LogIndex:       0,
	}
	err := indexerRepo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err)

	// Query with uppercase address - should still find results
	upperAddress := strings.ToUpper(lowerAddress)
	response, err := queryRepo.GetDepositWithdrawHistory(ctx, upperAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	assert.Len(t, response.Items, 1, "Should find result when querying with uppercase address")
}

func TestGetDepositWithdrawHistory_EmptyResult(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, _, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	// Address with no events
	nonExistentAddress := "test-queryempty-0x0000000000000000000000000000000000000000"

	response, err := queryRepo.GetDepositWithdrawHistory(ctx, nonExistentAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	assert.Len(t, response.Items, 0, "Should return empty items")
	assert.False(t, response.Page.HasMore, "HasMore should be false for empty result")
	assert.Nil(t, response.Page.NextCursor, "NextCursor should be nil for empty result")
}

// =============================================================================
// GetRentalHistory Tests
// =============================================================================

func TestGetRentalHistory_ReturnsOrderedResults(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-queryrentalorder-0x1234567890abcdef1234567890abcdef12345678"

	// Insert 3 rentals with different start times
	baseTime := time.Now().Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		record := RentalStartedRecord{
			RentalID:        uint64(5000 + i),
			UserAddress:     userAddress,
			ProviderAddress: "test-queryrentalorder-0xprovider1234567890abcdef1234567890ab",
			StartTime:       baseTime.Add(time.Duration(i) * time.Hour),
			TxHash:          "0xrentalorder" + string(rune('a'+i)) + "12345678901234567890123456789012345678901",
			BlockNumber:     uint64(40000 + i),
		}
		err := indexerRepo.InsertRentalStarted(ctx, record)
		require.NoError(t, err)
	}

	// Query should return newest first (DESC ordering by start_time)
	response, err := queryRepo.GetRentalHistory(ctx, userAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, response.Items, 3)

	// Verify ordering: newest start_time first
	for i := 0; i < len(response.Items)-1; i++ {
		assert.True(t,
			response.Items[i].StartTime.After(response.Items[i+1].StartTime) ||
				response.Items[i].StartTime.Equal(response.Items[i+1].StartTime),
			"Items should be ordered by start_time DESC")
	}
}

func TestGetRentalHistory_IncludesActiveRentals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-queryrentalactive-0x1234567890abcdef1234567890abcdef12345678"

	// Insert an active rental (no end_time)
	activeRental := RentalStartedRecord{
		RentalID:        6001,
		UserAddress:     userAddress,
		ProviderAddress: "test-queryrentalactive-0xprovider1234567890abcdef1234567890",
		StartTime:       time.Now().Add(-time.Hour),
		TxHash:          "0xactive12345678901234567890123456789012345678901234567890123456789",
		BlockNumber:     50000,
	}
	err := indexerRepo.InsertRentalStarted(ctx, activeRental)
	require.NoError(t, err)

	// Insert a completed rental
	completedRental := RentalStartedRecord{
		RentalID:        6002,
		UserAddress:     userAddress,
		ProviderAddress: "test-queryrentalactive-0xprovider1234567890abcdef1234567890",
		StartTime:       time.Now().Add(-2 * time.Hour),
		TxHash:          "0xcompleted23456789012345678901234567890123456789012345678901234567",
		BlockNumber:     49000,
	}
	err = indexerRepo.InsertRentalStarted(ctx, completedRental)
	require.NoError(t, err)

	// Stop the completed rental
	stopRecord := RentalStoppedRecord{
		RentalID:    6002,
		EndTime:     time.Now().Add(-time.Hour),
		CostWei:     "3600000000000000000",
		TxHash:      "0xstop1234567890123456789012345678901234567890123456789012345678901234",
		BlockNumber: 50001,
	}
	err = indexerRepo.UpdateRentalStopped(ctx, stopRecord)
	require.NoError(t, err)

	// Query rentals
	response, err := queryRepo.GetRentalHistory(ctx, userAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, response.Items, 2)

	// Find active and completed rentals by IsActive flag
	var activeCount, completedCount int
	for _, item := range response.Items {
		if item.IsActive {
			activeCount++
			assert.Nil(t, item.EndTime, "Active rental should have nil EndTime")
			assert.Nil(t, item.DurationSeconds, "Active rental should have nil DurationSeconds")
			assert.Nil(t, item.CostWei, "Active rental should have nil CostWei")
		} else {
			completedCount++
			assert.NotNil(t, item.EndTime, "Completed rental should have EndTime")
			assert.NotNil(t, item.CostWei, "Completed rental should have CostWei")
		}
	}
	assert.Equal(t, 1, activeCount, "Should have 1 active rental")
	assert.Equal(t, 1, completedCount, "Should have 1 completed rental")
}

func TestGetRentalHistory_CursorPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-queryrentalcursor-0x1234567890abcdef1234567890abcdef12345678"

	// Insert 5 rentals
	baseTime := time.Now().Add(-5 * time.Hour)
	for i := 0; i < 5; i++ {
		record := RentalStartedRecord{
			RentalID:        uint64(7000 + i),
			UserAddress:     userAddress,
			ProviderAddress: "test-queryrentalcursor-0xprovider1234567890abcdef123456789",
			StartTime:       baseTime.Add(time.Duration(i) * time.Hour),
			TxHash:          "0xrentalcursor" + string(rune('a'+i)) + "1234567890123456789012345678901234567890",
			BlockNumber:     uint64(60000 + i),
		}
		err := indexerRepo.InsertRentalStarted(ctx, record)
		require.NoError(t, err)
	}

	// First page: limit 2
	page1, err := queryRepo.GetRentalHistory(ctx, userAddress, time.Time{}, 0, 2)
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	assert.True(t, page1.Page.HasMore)
	require.NotNil(t, page1.Page.NextCursor)

	// Parse cursor timestamp
	cursorTime, err := time.Parse(time.RFC3339Nano, page1.Page.NextCursor.AfterTimestamp)
	require.NoError(t, err)

	// Second page: use cursor
	page2, err := queryRepo.GetRentalHistory(ctx, userAddress, cursorTime, page1.Page.NextCursor.AfterID, 2)
	require.NoError(t, err)
	require.Len(t, page2.Items, 2)
	assert.True(t, page2.Page.HasMore)

	// Verify no overlap between pages
	for _, p1Item := range page1.Items {
		for _, p2Item := range page2.Items {
			assert.NotEqual(t, p1Item.RentalID, p2Item.RentalID, "Pages should not have overlapping items")
		}
	}
}

func TestGetRentalHistory_NoActiveRentals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	userAddress := "test-querynoactive-0x1234567890abcdef1234567890abcdef12345678"

	// Insert and complete a rental
	record := RentalStartedRecord{
		RentalID:        8001,
		UserAddress:     userAddress,
		ProviderAddress: "test-querynoactive-0xprovider1234567890abcdef1234567890ab",
		StartTime:       time.Now().Add(-2 * time.Hour),
		TxHash:          "0xnoactive1234567890123456789012345678901234567890123456789012345",
		BlockNumber:     70000,
	}
	err := indexerRepo.InsertRentalStarted(ctx, record)
	require.NoError(t, err)

	stopRecord := RentalStoppedRecord{
		RentalID:    8001,
		EndTime:     time.Now().Add(-time.Hour),
		CostWei:     "3600000000000000000",
		TxHash:      "0xnoactivestop12345678901234567890123456789012345678901234567890123",
		BlockNumber: 70100,
	}
	err = indexerRepo.UpdateRentalStopped(ctx, stopRecord)
	require.NoError(t, err)

	// Query rentals - all should be inactive
	response, err := queryRepo.GetRentalHistory(ctx, userAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, response.Items, 1)
	assert.False(t, response.Items[0].IsActive, "All rentals should be inactive")
}

// =============================================================================
// GetRentalHistoryByProvider Tests
// =============================================================================

func TestGetRentalHistoryByProvider_ReturnsProviderRentals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	queryRepo, indexerRepo, cleanup := setupQueryRepo(t)
	if queryRepo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	providerAddress := "test-queryprovider-0xprovider1234567890abcdef1234567890abcdef"

	// Insert rental for this provider
	record := RentalStartedRecord{
		RentalID:        9001,
		UserAddress:     "test-queryprovider-0xuser1234567890abcdef1234567890abcdef12",
		ProviderAddress: providerAddress,
		StartTime:       time.Now().Add(-time.Hour),
		TxHash:          "0xproviderquery12345678901234567890123456789012345678901234567890",
		BlockNumber:     80000,
	}
	err := indexerRepo.InsertRentalStarted(ctx, record)
	require.NoError(t, err)

	// Query by provider address
	response, err := queryRepo.GetRentalHistoryByProvider(ctx, providerAddress, time.Time{}, 0, 10)
	require.NoError(t, err)
	require.Len(t, response.Items, 1)
	assert.Equal(t, int64(9001), response.Items[0].RentalID)
}

// =============================================================================
// Unit Tests (no database required)
// =============================================================================

func TestNewQueryRepository(t *testing.T) {
	repo := NewQueryRepository(nil)
	assert.NotNil(t, repo, "Repository should be created")
	assert.Nil(t, repo.db, "DB pool should be nil when passed nil")
}

func TestModelTypes(t *testing.T) {
	// Test DepositWithdrawItem
	depositItem := DepositWithdrawItem{
		ID:             1,
		EventType:      "deposit",
		Amount:         "1000000000000000000",
		TxHash:         "0x123",
		BlockNumber:    100,
		BlockTimestamp: time.Now(),
	}
	assert.Equal(t, "deposit", depositItem.EventType)

	// Test RentalHistoryItem with active rental
	activeRental := RentalHistoryItem{
		RentalID:        1,
		UserAddress:     "0x123",
		ProviderAddress: "0x456",
		StartTime:       time.Now(),
		EndTime:         nil, // active
		DurationSeconds: nil,
		CostWei:         nil,
		StartTxHash:     "0xstart",
		StopTxHash:      nil,
		IsActive:        true,
	}
	assert.True(t, activeRental.IsActive)
	assert.Nil(t, activeRental.EndTime)

	// Test RentalHistoryItem with completed rental
	endTime := time.Now()
	duration := int64(3600)
	cost := "1000000000000000000"
	stopTx := "0xstop"
	completedRental := RentalHistoryItem{
		RentalID:        2,
		UserAddress:     "0x123",
		ProviderAddress: "0x456",
		StartTime:       time.Now().Add(-time.Hour),
		EndTime:         &endTime,
		DurationSeconds: &duration,
		CostWei:         &cost,
		StartTxHash:     "0xstart",
		StopTxHash:      &stopTx,
		IsActive:        false,
	}
	assert.False(t, completedRental.IsActive)
	assert.NotNil(t, completedRental.EndTime)

	// Test PageCursor
	cursor := PageCursor{
		AfterTimestamp: "2026-01-01T00:00:00Z",
		AfterID:        100,
	}
	assert.Equal(t, int64(100), cursor.AfterID)

	// Test PageInfo
	pageInfo := PageInfo{
		Limit:      50,
		HasMore:    true,
		NextCursor: &cursor,
	}
	assert.True(t, pageInfo.HasMore)
	assert.NotNil(t, pageInfo.NextCursor)
}

func TestDefaultAndMaxLimits(t *testing.T) {
	assert.Equal(t, 50, DefaultLimit, "DefaultLimit should be 50")
	assert.Equal(t, 100, MaxLimit, "MaxLimit should be 100")
}
