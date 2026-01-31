// Package indexer provides query repository for fetching indexed blockchain events.
package indexer

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultLimit is the default number of items per page.
	DefaultLimit = 50
	// MaxLimit is the maximum number of items per page.
	MaxLimit = 100
)

// QueryRepository provides read-only access to indexed blockchain events.
type QueryRepository struct {
	db *pgxpool.Pool
}

// NewQueryRepository creates a new query repository with the given database pool.
func NewQueryRepository(db *pgxpool.Pool) *QueryRepository {
	return &QueryRepository{db: db}
}

// GetDepositWithdrawHistory returns deposit/withdraw history for an address with cursor pagination.
// Address is normalized to lowercase before query.
// Results are ordered by block_timestamp DESC, id DESC (most recent first).
func (r *QueryRepository) GetDepositWithdrawHistory(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*DepositWithdrawHistoryResponse, error) {
	// Normalize address to lowercase
	address = strings.ToLower(address)

	// Apply limit constraints
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	// Default cursor values for first page
	if afterTimestamp.IsZero() {
		afterTimestamp = time.Now().UTC()
	}
	if afterID == 0 {
		afterID = math.MaxInt64
	}

	// Query uses composite index: idx_deposit_withdraw_user_time
	// (user_address, block_timestamp DESC, id DESC)
	rows, err := r.db.Query(ctx, `
		SELECT id, event_type, amount, tx_hash, block_number, block_timestamp
		FROM deposit_withdraw_history
		WHERE user_address = $1
		  AND (block_timestamp, id) < ($2, $3)
		ORDER BY block_timestamp DESC, id DESC
		LIMIT $4
	`, address, afterTimestamp, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DepositWithdrawItem, 0, limit)
	for rows.Next() {
		var item DepositWithdrawItem
		if err := rows.Scan(
			&item.ID,
			&item.EventType,
			&item.Amount,
			&item.TxHash,
			&item.BlockNumber,
			&item.BlockTimestamp,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Build pagination info
	response := &DepositWithdrawHistoryResponse{
		Items: items,
		Page: PageInfo{
			Limit:   limit,
			HasMore: len(items) == limit,
		},
	}

	// Set next cursor from last item
	if len(items) > 0 && response.Page.HasMore {
		lastItem := items[len(items)-1]
		response.Page.NextCursor = &PageCursor{
			AfterTimestamp: lastItem.BlockTimestamp.Format(time.RFC3339Nano),
			AfterID:        lastItem.ID,
		}
	}

	return response, nil
}

// GetRentalHistory returns rental history for a user address with cursor pagination.
// Address is normalized to lowercase before query.
// Results are ordered by start_time DESC, rental_id DESC (most recent first).
func (r *QueryRepository) GetRentalHistory(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*RentalHistoryResponse, error) {
	// Normalize address to lowercase
	address = strings.ToLower(address)

	// Apply limit constraints
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	// Default cursor values for first page
	if afterTimestamp.IsZero() {
		afterTimestamp = time.Now().UTC()
	}
	if afterID == 0 {
		afterID = math.MaxInt64
	}

	// Query uses composite index: idx_rental_user_start
	// (user_address, start_time DESC)
	rows, err := r.db.Query(ctx, `
		SELECT rental_id, user_address, provider_address, start_time, end_time,
		       duration_seconds, cost_wei, start_tx_hash, stop_tx_hash
		FROM rental_history
		WHERE user_address = $1
		  AND (start_time, rental_id) < ($2, $3)
		ORDER BY start_time DESC, rental_id DESC
		LIMIT $4
	`, address, afterTimestamp, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items, err := scanRentalItems(rows, limit)
	if err != nil {
		return nil, err
	}

	// Build pagination info
	response := &RentalHistoryResponse{
		Items: items,
		Page: PageInfo{
			Limit:   limit,
			HasMore: len(items) == limit,
		},
	}

	// Set next cursor from last item
	if len(items) > 0 && response.Page.HasMore {
		lastItem := items[len(items)-1]
		response.Page.NextCursor = &PageCursor{
			AfterTimestamp: lastItem.StartTime.Format(time.RFC3339Nano),
			AfterID:        lastItem.RentalID,
		}
	}

	return response, nil
}

// GetRentalHistoryByProvider returns rental history for a provider address with cursor pagination.
// Address is normalized to lowercase before query.
// Results are ordered by start_time DESC, rental_id DESC (most recent first).
func (r *QueryRepository) GetRentalHistoryByProvider(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*RentalHistoryResponse, error) {
	// Normalize address to lowercase
	address = strings.ToLower(address)

	// Apply limit constraints
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	// Default cursor values for first page
	if afterTimestamp.IsZero() {
		afterTimestamp = time.Now().UTC()
	}
	if afterID == 0 {
		afterID = math.MaxInt64
	}

	// Query uses composite index: idx_rental_provider_start
	// (provider_address, start_time DESC)
	rows, err := r.db.Query(ctx, `
		SELECT rental_id, user_address, provider_address, start_time, end_time,
		       duration_seconds, cost_wei, start_tx_hash, stop_tx_hash
		FROM rental_history
		WHERE provider_address = $1
		  AND (start_time, rental_id) < ($2, $3)
		ORDER BY start_time DESC, rental_id DESC
		LIMIT $4
	`, address, afterTimestamp, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items, err := scanRentalItems(rows, limit)
	if err != nil {
		return nil, err
	}

	// Build pagination info
	response := &RentalHistoryResponse{
		Items: items,
		Page: PageInfo{
			Limit:   limit,
			HasMore: len(items) == limit,
		},
	}

	// Set next cursor from last item
	if len(items) > 0 && response.Page.HasMore {
		lastItem := items[len(items)-1]
		response.Page.NextCursor = &PageCursor{
			AfterTimestamp: lastItem.StartTime.Format(time.RFC3339Nano),
			AfterID:        lastItem.RentalID,
		}
	}

	return response, nil
}

// scanRentalItems scans rental rows into RentalHistoryItem slice.
// Handles NULL fields and sets IsActive based on end_time.
func scanRentalItems(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}, capacity int) ([]RentalHistoryItem, error) {
	items := make([]RentalHistoryItem, 0, capacity)
	for rows.Next() {
		var item RentalHistoryItem
		var costWei *string // For scanning NUMERIC as string

		if err := rows.Scan(
			&item.RentalID,
			&item.UserAddress,
			&item.ProviderAddress,
			&item.StartTime,
			&item.EndTime,
			&item.DurationSeconds,
			&costWei,
			&item.StartTxHash,
			&item.StopTxHash,
		); err != nil {
			return nil, err
		}

		// Set CostWei (NUMERIC scans as string)
		item.CostWei = costWei

		// Set IsActive based on end_time being NULL
		item.IsActive = item.EndTime == nil

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
