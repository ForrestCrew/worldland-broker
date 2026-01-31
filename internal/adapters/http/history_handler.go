// Package http provides HTTP handlers for the Hub API.
package http

import (
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/indexer"
)

// HistoryHandler handles history query HTTP requests.
type HistoryHandler struct {
	queryRepo *indexer.QueryRepository
}

// NewHistoryHandler creates a new history handler.
func NewHistoryHandler(queryRepo *indexer.QueryRepository) *HistoryHandler {
	return &HistoryHandler{queryRepo: queryRepo}
}

// GetDepositWithdrawHistory returns deposit/withdraw history for an address.
// GET /api/history/deposits-withdraws/:address
func (h *HistoryHandler) GetDepositWithdrawHistory(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		ErrorBadRequest(c, ErrCodeValidation, "address parameter is required")
		return
	}

	// Parse cursor params with defaults
	afterTimestamp, afterID, limit := parseCursorParams(c)

	// Query repository
	response, err := h.queryRepo.GetDepositWithdrawHistory(
		c.Request.Context(),
		address,
		afterTimestamp,
		afterID,
		limit,
	)
	if err != nil {
		ErrorInternal(c, "Failed to query deposit/withdraw history")
		return
	}

	SuccessOK(c, response)
}

// GetRentalHistory returns rental history for a user address.
// GET /api/history/rentals/:address
func (h *HistoryHandler) GetRentalHistory(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		ErrorBadRequest(c, ErrCodeValidation, "address parameter is required")
		return
	}

	// Parse cursor params with defaults
	afterTimestamp, afterID, limit := parseCursorParams(c)

	// Query repository
	response, err := h.queryRepo.GetRentalHistory(
		c.Request.Context(),
		address,
		afterTimestamp,
		afterID,
		limit,
	)
	if err != nil {
		ErrorInternal(c, "Failed to query rental history")
		return
	}

	SuccessOK(c, response)
}

// parseCursorParams extracts pagination cursor parameters from query string.
// Returns defaults if params are missing or invalid:
// - afterTimestamp: current time (start from latest)
// - afterID: MaxInt64 (start from highest ID)
// - limit: 50 (default), capped at 100
func parseCursorParams(c *gin.Context) (time.Time, int64, int) {
	// Parse after_timestamp (RFC3339 format)
	afterTimestamp := time.Now().UTC()
	if ts := c.Query("after_timestamp"); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			afterTimestamp = parsed
		} else if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			afterTimestamp = parsed
		}
	}

	// Parse after_id
	afterID := int64(math.MaxInt64)
	if id := c.Query("after_id"); id != "" {
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil {
			afterID = parsed
		}
	}

	// Parse limit with default 50, max 100
	limit := indexer.DefaultLimit
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	if limit <= 0 {
		limit = indexer.DefaultLimit
	}
	if limit > indexer.MaxLimit {
		limit = indexer.MaxLimit
	}

	return afterTimestamp, afterID, limit
}
