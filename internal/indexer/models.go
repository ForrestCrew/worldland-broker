// Package indexer provides data storage and query capabilities for indexed blockchain events.
package indexer

import "time"

// DepositWithdrawItem represents a deposit or withdraw event for API response.
type DepositWithdrawItem struct {
	ID             int64     `json:"id"`
	EventType      string    `json:"event_type"`      // "deposit" or "withdraw"
	Amount         string    `json:"amount"`          // Wei as string (uint256)
	TxHash         string    `json:"tx_hash"`
	BlockNumber    int64     `json:"block_number"`
	BlockTimestamp time.Time `json:"block_timestamp"`
}

// RentalHistoryItem represents a rental session for API response.
type RentalHistoryItem struct {
	RentalID        int64      `json:"rental_id"`
	UserAddress     string     `json:"user_address"`
	ProviderAddress string     `json:"provider_address"`
	StartTime       time.Time  `json:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty"`         // nil if active
	DurationSeconds *int64     `json:"duration_seconds,omitempty"` // nil if active
	CostWei         *string    `json:"cost_wei,omitempty"`         // nil if active
	StartTxHash     string     `json:"start_tx_hash"`
	StopTxHash      *string    `json:"stop_tx_hash,omitempty"`
	IsActive        bool       `json:"is_active"`
}

// PageCursor for cursor-based pagination.
type PageCursor struct {
	AfterTimestamp string `json:"after_timestamp,omitempty"` // RFC3339 format
	AfterID        int64  `json:"after_id,omitempty"`
}

// PageInfo for pagination response.
type PageInfo struct {
	Limit      int         `json:"limit"`
	HasMore    bool        `json:"has_more"`
	NextCursor *PageCursor `json:"next_cursor,omitempty"`
}

// DepositWithdrawHistoryResponse for API response.
type DepositWithdrawHistoryResponse struct {
	Items []DepositWithdrawItem `json:"items"`
	Page  PageInfo              `json:"page"`
}

// RentalHistoryResponse for API response.
type RentalHistoryResponse struct {
	Items []RentalHistoryItem `json:"items"`
	Page  PageInfo            `json:"page"`
}
