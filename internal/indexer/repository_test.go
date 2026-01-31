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

// testConfig returns configuration for test database connection
func testDatabaseURL() string {
	return "postgres://test:test@localhost:5432/worldland_test?sslmode=disable"
}

// setupTestRepo creates a repository with test database connection
func setupTestRepo(t *testing.T) (*IndexerRepository, func()) {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testDatabaseURL())
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return nil, nil
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("PostgreSQL not responding: %v", err)
		return nil, nil
	}

	repo := NewIndexerRepository(pool)

	cleanup := func() {
		// Clean up test data
		_, _ = pool.Exec(ctx, "DELETE FROM deposit_withdraw_history WHERE user_address LIKE 'test-%'")
		_, _ = pool.Exec(ctx, "DELETE FROM rental_history WHERE user_address LIKE 'test-%'")
		_, _ = pool.Exec(ctx, "UPDATE indexer_checkpoint SET block_number = 0 WHERE id = 'indexer_events'")
		pool.Close()
	}

	return repo, cleanup
}

// =============================================================================
// InsertDepositWithdraw Tests
// =============================================================================

func TestInsertDepositWithdraw_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	record := DepositWithdrawRecord{
		UserAddress:    "test-deposit-0x1234567890abcdef1234567890abcdef12345678",
		EventType:      "deposit",
		Amount:         "1000000000000000000", // 1 ETH in wei
		TxHash:         "0xabc123def456abc123def456abc123def456abc123def456abc123def456abc1",
		BlockNumber:    12345,
		BlockTimestamp: time.Now(),
		LogIndex:       0,
	}

	err := repo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err, "InsertDepositWithdraw should succeed")

	// Verify insertion
	var count int
	err = repo.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM deposit_withdraw_history WHERE tx_hash = $1 AND log_index = $2",
		record.TxHash, record.LogIndex,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Should have inserted one record")
}

func TestInsertDepositWithdraw_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	record := DepositWithdrawRecord{
		UserAddress:    "test-idempotent-0x1234567890abcdef1234567890abcdef12345678",
		EventType:      "deposit",
		Amount:         "2000000000000000000",
		TxHash:         "0xidempotent123456789012345678901234567890123456789012345678901234",
		BlockNumber:    12346,
		BlockTimestamp: time.Now(),
		LogIndex:       0,
	}

	// Insert first time
	err := repo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err, "First insert should succeed")

	// Insert second time with same tx_hash + log_index (should be ignored)
	err = repo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err, "Second insert should not error (idempotent)")

	// Verify only one record exists
	var count int
	err = repo.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM deposit_withdraw_history WHERE tx_hash = $1 AND log_index = $2",
		record.TxHash, record.LogIndex,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Should only have one record (idempotent)")
}

func TestInsertDepositWithdraw_LowercasesAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Use mixed case address
	mixedCaseAddress := "test-lowercase-0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"
	expectedLowercase := strings.ToLower(mixedCaseAddress)

	record := DepositWithdrawRecord{
		UserAddress:    mixedCaseAddress,
		EventType:      "withdraw",
		Amount:         "500000000000000000",
		TxHash:         "0xlowercase12345678901234567890123456789012345678901234567890123",
		BlockNumber:    12347,
		BlockTimestamp: time.Now(),
		LogIndex:       0,
	}

	err := repo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err)

	// Verify address is stored as lowercase
	var storedAddress string
	err = repo.db.QueryRow(ctx,
		"SELECT user_address FROM deposit_withdraw_history WHERE tx_hash = $1",
		record.TxHash,
	).Scan(&storedAddress)
	require.NoError(t, err)
	assert.Equal(t, expectedLowercase, storedAddress, "Address should be stored lowercase")
}

func TestInsertDepositWithdraw_BothEventTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name      string
		eventType string
	}{
		{"deposit", "deposit"},
		{"withdraw", "withdraw"},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := DepositWithdrawRecord{
				UserAddress:    "test-eventtype-0x1234567890abcdef1234567890abcdef12345678",
				EventType:      tt.eventType,
				Amount:         "100000000000000000",
				TxHash:         "0xeventtype" + string(rune('a'+i)) + "12345678901234567890123456789012345678901234",
				BlockNumber:    uint64(12348 + i),
				BlockTimestamp: time.Now(),
				LogIndex:       0,
			}

			err := repo.InsertDepositWithdraw(ctx, record)
			require.NoError(t, err)

			var storedType string
			err = repo.db.QueryRow(ctx,
				"SELECT event_type FROM deposit_withdraw_history WHERE tx_hash = $1",
				record.TxHash,
			).Scan(&storedType)
			require.NoError(t, err)
			assert.Equal(t, tt.eventType, storedType)
		})
	}
}

// =============================================================================
// InsertRentalStarted Tests
// =============================================================================

func TestInsertRentalStarted_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	record := RentalStartedRecord{
		RentalID:        1001,
		UserAddress:     "test-rentalstart-0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "test-rentalstart-0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		StartTime:       time.Now(),
		TxHash:          "0xrentalstart123456789012345678901234567890123456789012345678901",
		BlockNumber:     20000,
	}

	err := repo.InsertRentalStarted(ctx, record)
	require.NoError(t, err, "InsertRentalStarted should succeed")

	// Verify insertion
	var count int
	err = repo.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM rental_history WHERE rental_id = $1",
		record.RentalID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Should have inserted one rental record")
}

func TestInsertRentalStarted_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	record := RentalStartedRecord{
		RentalID:        1002,
		UserAddress:     "test-rentalidem-0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "test-rentalidem-0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		StartTime:       time.Now(),
		TxHash:          "0xrentalidem1234567890123456789012345678901234567890123456789012",
		BlockNumber:     20001,
	}

	// Insert first time
	err := repo.InsertRentalStarted(ctx, record)
	require.NoError(t, err, "First insert should succeed")

	// Insert second time with same rental_id (should be ignored)
	err = repo.InsertRentalStarted(ctx, record)
	require.NoError(t, err, "Second insert should not error (idempotent)")

	// Verify only one record exists
	var count int
	err = repo.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM rental_history WHERE rental_id = $1",
		record.RentalID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Should only have one rental record (idempotent)")
}

func TestInsertRentalStarted_LowercasesAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	mixedUser := "test-rentallower-0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"
	mixedProvider := "test-rentallower-0xFeDcBa0987654321FeDcBa0987654321FeDcBa09"

	record := RentalStartedRecord{
		RentalID:        1003,
		UserAddress:     mixedUser,
		ProviderAddress: mixedProvider,
		StartTime:       time.Now(),
		TxHash:          "0xrentallower12345678901234567890123456789012345678901234567890",
		BlockNumber:     20002,
	}

	err := repo.InsertRentalStarted(ctx, record)
	require.NoError(t, err)

	var storedUser, storedProvider string
	err = repo.db.QueryRow(ctx,
		"SELECT user_address, provider_address FROM rental_history WHERE rental_id = $1",
		record.RentalID,
	).Scan(&storedUser, &storedProvider)
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(mixedUser), storedUser, "User address should be lowercase")
	assert.Equal(t, strings.ToLower(mixedProvider), storedProvider, "Provider address should be lowercase")
}

// =============================================================================
// UpdateRentalStopped Tests
// =============================================================================

func TestUpdateRentalStopped_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// First create a rental
	startTime := time.Now().Add(-time.Hour) // Started 1 hour ago
	startRecord := RentalStartedRecord{
		RentalID:        2001,
		UserAddress:     "test-rentalstop-0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "test-rentalstop-0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		StartTime:       startTime,
		TxHash:          "0xrentalstopstart1234567890123456789012345678901234567890123456",
		BlockNumber:     30000,
	}

	err := repo.InsertRentalStarted(ctx, startRecord)
	require.NoError(t, err)

	// Now update with stop info
	endTime := time.Now()
	stopRecord := RentalStoppedRecord{
		RentalID:    2001,
		EndTime:     endTime,
		CostWei:     "3600000000000000000", // 3.6 ETH
		TxHash:      "0xrentalstopend12345678901234567890123456789012345678901234567890",
		BlockNumber: 30100,
	}

	err = repo.UpdateRentalStopped(ctx, stopRecord)
	require.NoError(t, err, "UpdateRentalStopped should succeed")

	// Verify update
	var storedEndTime *time.Time
	var storedCost *string
	var storedStopTxHash *string
	err = repo.db.QueryRow(ctx,
		"SELECT end_time, cost_wei, stop_tx_hash FROM rental_history WHERE rental_id = $1",
		stopRecord.RentalID,
	).Scan(&storedEndTime, &storedCost, &storedStopTxHash)
	require.NoError(t, err)

	require.NotNil(t, storedEndTime, "End time should be set")
	require.NotNil(t, storedCost, "Cost should be set")
	require.NotNil(t, storedStopTxHash, "Stop tx hash should be set")
	assert.Equal(t, stopRecord.CostWei, *storedCost)
	assert.Equal(t, stopRecord.TxHash, *storedStopTxHash)
}

func TestUpdateRentalStopped_CalculatesDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Create rental with specific start time
	startTime := time.Now().Add(-2 * time.Hour) // Started 2 hours ago
	startRecord := RentalStartedRecord{
		RentalID:        2002,
		UserAddress:     "test-duration-0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "test-duration-0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		StartTime:       startTime,
		TxHash:          "0xdurationstart12345678901234567890123456789012345678901234567890",
		BlockNumber:     31000,
	}

	err := repo.InsertRentalStarted(ctx, startRecord)
	require.NoError(t, err)

	// Stop rental 2 hours after start
	endTime := startTime.Add(2 * time.Hour)
	stopRecord := RentalStoppedRecord{
		RentalID:    2002,
		EndTime:     endTime,
		CostWei:     "7200000000000000000", // Cost for 2 hours
		TxHash:      "0xdurationend123456789012345678901234567890123456789012345678901234",
		BlockNumber: 31200,
	}

	err = repo.UpdateRentalStopped(ctx, stopRecord)
	require.NoError(t, err)

	// Verify duration is calculated correctly (should be ~7200 seconds = 2 hours)
	var durationSeconds *int64
	err = repo.db.QueryRow(ctx,
		"SELECT duration_seconds FROM rental_history WHERE rental_id = $1",
		stopRecord.RentalID,
	).Scan(&durationSeconds)
	require.NoError(t, err)

	require.NotNil(t, durationSeconds, "Duration should be calculated")
	// Allow small tolerance for time difference
	assert.InDelta(t, 7200, *durationSeconds, 5, "Duration should be approximately 7200 seconds (2 hours)")
}

// =============================================================================
// Checkpoint Tests
// =============================================================================

func TestGetLastProcessedBlock_ReturnsCorrectValue(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Get initial value (should be 0 from schema)
	block, err := repo.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), block, "Initial block should be 0")
}

func TestUpdateCheckpoint_UpdatesBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Update to block 1000
	err := repo.UpdateCheckpoint(ctx, 1000)
	require.NoError(t, err)

	block, err := repo.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(1000), block, "Block should be updated to 1000")
}

func TestUpdateCheckpoint_OnlyIncreases(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Update to block 2000
	err := repo.UpdateCheckpoint(ctx, 2000)
	require.NoError(t, err)

	// Try to update to lower block (should be ignored due to WHERE block_number < $1)
	err = repo.UpdateCheckpoint(ctx, 1500)
	require.NoError(t, err, "Should not error when updating to lower block")

	// Verify block is still 2000
	block, err := repo.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(2000), block, "Block should still be 2000 (not regressed)")

	// Update to higher block should work
	err = repo.UpdateCheckpoint(ctx, 3000)
	require.NoError(t, err)

	block, err = repo.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(3000), block, "Block should be updated to 3000")
}

func TestIsProcessed_ReturnsFalseForNewEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// Check for non-existent event
	processed, err := repo.IsProcessed(ctx, "0xnonexistent123456789012345678901234567890123456789012345678901234", 0)
	require.NoError(t, err)
	assert.False(t, processed, "Non-existent event should not be processed")
}

func TestIsProcessed_ReturnsTrueAfterInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	txHash := "0xisprocessed1234567890123456789012345678901234567890123456789012345"
	logIndex := 5

	// Insert a deposit event
	record := DepositWithdrawRecord{
		UserAddress:    "test-isprocessed-0x1234567890abcdef1234567890abcdef12345678",
		EventType:      "deposit",
		Amount:         "100000000000000000",
		TxHash:         txHash,
		BlockNumber:    50000,
		BlockTimestamp: time.Now(),
		LogIndex:       logIndex,
	}

	err := repo.InsertDepositWithdraw(ctx, record)
	require.NoError(t, err)

	// Now check if it's processed
	processed, err := repo.IsProcessed(ctx, txHash, logIndex)
	require.NoError(t, err)
	assert.True(t, processed, "Event should be marked as processed after insert")
}

func TestMarkProcessed_NoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	repo, cleanup := setupTestRepo(t)
	if repo == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()

	// MarkProcessed should be a no-op (idempotency is handled by insert)
	err := repo.MarkProcessed(ctx, "0xanytxhash1234567890123456789012345678901234567890123456789012345", 0)
	require.NoError(t, err, "MarkProcessed should not error (it's a no-op)")
}

// =============================================================================
// Unit Tests (no database required)
// =============================================================================

func TestNewIndexerRepository(t *testing.T) {
	// Test that constructor works with nil pool (for compile check)
	repo := NewIndexerRepository(nil)
	assert.NotNil(t, repo, "Repository should be created")
	assert.Nil(t, repo.db, "DB pool should be nil when passed nil")
}

func TestRecordTypes(t *testing.T) {
	// Test that record types can be constructed properly
	now := time.Now()

	depositRecord := DepositWithdrawRecord{
		UserAddress:    "0x1234567890abcdef1234567890abcdef12345678",
		EventType:      "deposit",
		Amount:         "1000000000000000000",
		TxHash:         "0xabc123",
		BlockNumber:    12345,
		BlockTimestamp: now,
		LogIndex:       0,
	}
	assert.Equal(t, "deposit", depositRecord.EventType)

	rentalStarted := RentalStartedRecord{
		RentalID:        1,
		UserAddress:     "0x1234",
		ProviderAddress: "0x5678",
		StartTime:       now,
		TxHash:          "0xabc",
		BlockNumber:     100,
	}
	assert.Equal(t, uint64(1), rentalStarted.RentalID)

	rentalStopped := RentalStoppedRecord{
		RentalID:    1,
		EndTime:     now,
		CostWei:     "500000000000000000",
		TxHash:      "0xdef",
		BlockNumber: 200,
	}
	assert.Equal(t, "500000000000000000", rentalStopped.CostWei)
}
