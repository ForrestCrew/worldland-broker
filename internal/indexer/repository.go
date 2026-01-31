// Package indexer provides data storage for indexed blockchain events.
package indexer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DepositWithdrawRecord represents a deposit or withdraw event to be stored.
type DepositWithdrawRecord struct {
	UserAddress    string    // Ethereum address (will be lowercased)
	EventType      string    // "deposit" or "withdraw"
	Amount         string    // Wei amount as string (for big.Int compatibility)
	TxHash         string    // Transaction hash
	BlockNumber    uint64    // Block number
	BlockTimestamp time.Time // Block timestamp
	LogIndex       int       // Log index within transaction
}

// RentalStartedRecord represents a RentalStarted event to be stored.
type RentalStartedRecord struct {
	RentalID        uint64    // Rental ID from contract
	UserAddress     string    // User address (will be lowercased)
	ProviderAddress string    // Provider address (will be lowercased)
	StartTime       time.Time // When rental started
	TxHash          string    // Transaction hash
	BlockNumber     uint64    // Block number
}

// RentalStoppedRecord represents a RentalStopped event update.
type RentalStoppedRecord struct {
	RentalID    uint64    // Rental ID to update
	EndTime     time.Time // When rental ended
	CostWei     string    // Final cost in wei as string
	TxHash      string    // Transaction hash
	BlockNumber uint64    // Block number
}

// IndexerRepository handles database operations for indexed blockchain events.
type IndexerRepository struct {
	db *pgxpool.Pool
}

// NewIndexerRepository creates a new indexer repository with the given database pool.
func NewIndexerRepository(db *pgxpool.Pool) *IndexerRepository {
	return &IndexerRepository{db: db}
}

// InsertDepositWithdraw inserts a deposit or withdraw event.
// Uses ON CONFLICT DO NOTHING for idempotency (same tx_hash + log_index is ignored).
// Addresses are lowercased before storage.
func (r *IndexerRepository) InsertDepositWithdraw(ctx context.Context, record DepositWithdrawRecord) error {
	// Lowercase address for consistent storage
	userAddress := strings.ToLower(record.UserAddress)

	_, err := r.db.Exec(ctx, `
		INSERT INTO deposit_withdraw_history
			(user_address, event_type, amount, tx_hash, block_number, block_timestamp, log_index)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tx_hash, log_index) DO NOTHING
	`, userAddress, record.EventType, record.Amount, record.TxHash, record.BlockNumber, record.BlockTimestamp, record.LogIndex)
	if err != nil {
		return fmt.Errorf("insert deposit/withdraw: %w", err)
	}
	return nil
}

// InsertRentalStarted inserts a new rental record when a RentalStarted event is received.
// Uses ON CONFLICT DO NOTHING for idempotency (same rental_id is ignored).
// Addresses are lowercased before storage.
func (r *IndexerRepository) InsertRentalStarted(ctx context.Context, record RentalStartedRecord) error {
	// Lowercase addresses for consistent storage
	userAddress := strings.ToLower(record.UserAddress)
	providerAddress := strings.ToLower(record.ProviderAddress)

	_, err := r.db.Exec(ctx, `
		INSERT INTO rental_history
			(rental_id, user_address, provider_address, start_time, start_tx_hash, start_block_number)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (rental_id) DO NOTHING
	`, record.RentalID, userAddress, providerAddress, record.StartTime, record.TxHash, record.BlockNumber)
	if err != nil {
		return fmt.Errorf("insert rental started: %w", err)
	}
	return nil
}

// UpdateRentalStopped updates an existing rental record with stop information.
// Calculates duration_seconds from end_time - start_time.
// This is safe to call multiple times with the same data (idempotent update).
func (r *IndexerRepository) UpdateRentalStopped(ctx context.Context, record RentalStoppedRecord) error {
	// Update rental with stop data
	// duration_seconds is calculated as: end_time - start_time (in seconds)
	_, err := r.db.Exec(ctx, `
		UPDATE rental_history
		SET
			end_time = $2,
			duration_seconds = EXTRACT(EPOCH FROM ($2 - start_time))::BIGINT,
			cost_wei = $3,
			stop_tx_hash = $4,
			stop_block_number = $5
		WHERE rental_id = $1
	`, record.RentalID, record.EndTime, record.CostWei, record.TxHash, record.BlockNumber)
	if err != nil {
		return fmt.Errorf("update rental stopped: %w", err)
	}
	return nil
}

// GetLastProcessedBlock returns the last successfully processed block number for the indexer.
// Returns 0 if no checkpoint exists (fresh start).
func (r *IndexerRepository) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	var blockNum int64
	err := r.db.QueryRow(ctx,
		"SELECT block_number FROM indexer_checkpoint WHERE id = 'indexer_events'",
	).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("get checkpoint: %w", err)
	}
	return uint64(blockNum), nil
}

// UpdateCheckpoint updates the last processed block number.
// Only updates if the new block number is greater than the current one,
// preventing checkpoint regression during parallel processing.
func (r *IndexerRepository) UpdateCheckpoint(ctx context.Context, blockNumber uint64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE indexer_checkpoint
		SET block_number = $1, updated_at = NOW()
		WHERE id = 'indexer_events' AND block_number < $1
	`, blockNumber)
	if err != nil {
		return fmt.Errorf("update checkpoint: %w", err)
	}
	return nil
}

// IsProcessed checks if an event with the given tx_hash and log_index has been processed.
// This checks the deposit_withdraw_history table for idempotency.
func (r *IndexerRepository) IsProcessed(ctx context.Context, txHash string, logIndex int) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM deposit_withdraw_history WHERE tx_hash = $1 AND log_index = $2)",
		txHash, logIndex,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}
	return exists, nil
}

// MarkProcessed is a no-op since InsertDepositWithdraw uses ON CONFLICT DO NOTHING.
// Events are automatically marked as processed when inserted.
// This method exists for interface compatibility with checkpoint patterns.
func (r *IndexerRepository) MarkProcessed(ctx context.Context, txHash string, logIndex int) error {
	// No-op: idempotency is handled by ON CONFLICT in insert methods
	return nil
}
