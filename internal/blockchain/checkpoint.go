// Package blockchain provides checkpoint store for managing event processing state.
package blockchain

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckpointStore manages event processing checkpoint and idempotency.
// It tracks the last successfully processed block and ensures events are not processed twice.
type CheckpointStore struct {
	db *pgxpool.Pool
}

// NewCheckpointStore creates a new checkpoint store with the given database pool.
func NewCheckpointStore(db *pgxpool.Pool) *CheckpointStore {
	return &CheckpointStore{db: db}
}

// GetLastProcessedBlock returns the last successfully processed block number.
// This is used during startup to determine where to begin backfilling events.
func (s *CheckpointStore) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	var blockNum int64
	err := s.db.QueryRow(ctx,
		"SELECT block_number FROM event_checkpoint WHERE id = 'rental_events'",
	).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("get checkpoint: %w", err)
	}
	return uint64(blockNum), nil
}

// UpdateCheckpoint updates the last processed block number.
// Only updates if the new block number is greater than the current one,
// preventing checkpoint regression during parallel processing.
func (s *CheckpointStore) UpdateCheckpoint(ctx context.Context, blockNumber uint64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE event_checkpoint
		SET block_number = $1, updated_at = NOW()
		WHERE id = 'rental_events' AND block_number < $1
	`, blockNumber)
	return err
}

// IsProcessed checks if a transaction has already been processed.
// Used for idempotency to prevent duplicate event handling.
func (s *CheckpointStore) IsProcessed(ctx context.Context, txHash string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM processed_events WHERE tx_hash = $1)",
		txHash,
	).Scan(&exists)
	return exists, err
}

// MarkProcessed marks a transaction as processed.
// Uses ON CONFLICT DO NOTHING to handle race conditions safely.
func (s *CheckpointStore) MarkProcessed(ctx context.Context, txHash string, blockNumber uint64, logIndex int, eventType string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO processed_events (tx_hash, block_number, log_index, event_type, processed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (tx_hash) DO NOTHING
	`, txHash, blockNumber, logIndex, eventType)
	return err
}

// SaveFailedEvent stores a failed event in the dead letter queue.
// Failed events can be inspected and retried manually or automatically.
func (s *CheckpointStore) SaveFailedEvent(ctx context.Context, txHash string, blockNumber uint64, logIndex int, eventType string, rawData []byte, errMsg string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO failed_events (tx_hash, block_number, log_index, event_type, raw_data, error_message)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, txHash, blockNumber, logIndex, eventType, rawData, errMsg)
	return err
}

// GetFailedEvents retrieves failed events for retry.
// Returns events ordered by failed_at, with optional limit.
func (s *CheckpointStore) GetFailedEvents(ctx context.Context, limit int) ([]FailedEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tx_hash, block_number, log_index, event_type, raw_data, error_message, retry_count, failed_at, last_retry_at
		FROM failed_events
		ORDER BY failed_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query failed events: %w", err)
	}
	defer rows.Close()

	var events []FailedEvent
	for rows.Next() {
		var e FailedEvent
		err := rows.Scan(&e.ID, &e.TxHash, &e.BlockNumber, &e.LogIndex, &e.EventType, &e.RawData, &e.ErrorMessage, &e.RetryCount, &e.FailedAt, &e.LastRetryAt)
		if err != nil {
			return nil, fmt.Errorf("scan failed event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// IncrementRetryCount increments the retry count for a failed event.
func (s *CheckpointStore) IncrementRetryCount(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE failed_events
		SET retry_count = retry_count + 1, last_retry_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// DeleteFailedEvent removes a failed event after successful retry.
func (s *CheckpointStore) DeleteFailedEvent(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM failed_events WHERE id = $1
	`, id)
	return err
}

// FailedEvent represents a failed event stored in the dead letter queue.
type FailedEvent struct {
	ID           int64
	TxHash       string
	BlockNumber  uint64
	LogIndex     int
	EventType    string
	RawData      []byte
	ErrorMessage string
	RetryCount   int
	FailedAt     *string
	LastRetryAt  *string
}
