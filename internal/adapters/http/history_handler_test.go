package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/indexer"
)

// MockQueryRepository is a mock implementation of QueryRepository for testing.
type MockQueryRepository struct {
	depositWithdrawHistory *indexer.DepositWithdrawHistoryResponse
	rentalHistory          *indexer.RentalHistoryResponse
	err                    error
}

// GetDepositWithdrawHistory returns mock deposit/withdraw history.
func (m *MockQueryRepository) GetDepositWithdrawHistory(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*indexer.DepositWithdrawHistoryResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.depositWithdrawHistory, nil
}

// GetRentalHistory returns mock rental history.
func (m *MockQueryRepository) GetRentalHistory(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*indexer.RentalHistoryResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rentalHistory, nil
}

// GetRentalHistoryByProvider returns mock rental history by provider.
func (m *MockQueryRepository) GetRentalHistoryByProvider(
	ctx context.Context,
	address string,
	afterTimestamp time.Time,
	afterID int64,
	limit int,
) (*indexer.RentalHistoryResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rentalHistory, nil
}

// setupTestHistoryRouter creates a test router with mock query repository.
func setupTestHistoryRouter(mockRepo *MockQueryRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Create handler with mock (using interface approach)
	historyHandler := httpAdapter.NewHistoryHandlerWithQuerier(mockRepo)

	// Register routes
	history := router.Group("/api/history")
	{
		history.GET("/deposits-withdraws/:address", historyHandler.GetDepositWithdrawHistory)
		history.GET("/rentals/:address", historyHandler.GetRentalHistory)
	}

	return router
}

// APIResponse matches the standard response wrapper.
type APIResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     interface{} `json:"error,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// DepositWithdrawResponse for parsing test responses.
type DepositWithdrawResponse struct {
	Items []indexer.DepositWithdrawItem `json:"items"`
	Page  indexer.PageInfo              `json:"page"`
}

// RentalHistoryResponse for parsing test responses.
type RentalHistoryTestResponse struct {
	Items []indexer.RentalHistoryItem `json:"items"`
	Page  indexer.PageInfo            `json:"page"`
}

// TestGetDepositWithdrawHistory_Success tests successful deposit/withdraw history retrieval.
func TestGetDepositWithdrawHistory_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	mockRepo := &MockQueryRepository{
		depositWithdrawHistory: &indexer.DepositWithdrawHistoryResponse{
			Items: []indexer.DepositWithdrawItem{
				{
					ID:             1,
					EventType:      "deposit",
					Amount:         "1000000000000000000",
					TxHash:         "0x123abc",
					BlockNumber:    12345,
					BlockTimestamp: now,
				},
				{
					ID:             2,
					EventType:      "withdraw",
					Amount:         "500000000000000000",
					TxHash:         "0x456def",
					BlockNumber:    12346,
					BlockTimestamp: now.Add(-time.Hour),
				},
			},
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: false,
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/deposits-withdraws/0x1234567890abcdef", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
	assert.NotEmpty(t, response.Timestamp)

	// Parse data field
	dataBytes, err := json.Marshal(response.Data)
	require.NoError(t, err)
	var data DepositWithdrawResponse
	err = json.Unmarshal(dataBytes, &data)
	require.NoError(t, err)

	assert.Len(t, data.Items, 2)
	assert.Equal(t, "deposit", data.Items[0].EventType)
	assert.Equal(t, "1000000000000000000", data.Items[0].Amount)
	assert.Equal(t, "withdraw", data.Items[1].EventType)
	assert.Equal(t, 50, data.Page.Limit)
	assert.False(t, data.Page.HasMore)
}

// TestGetDepositWithdrawHistory_EmptyResult tests empty result handling.
func TestGetDepositWithdrawHistory_EmptyResult(t *testing.T) {
	mockRepo := &MockQueryRepository{
		depositWithdrawHistory: &indexer.DepositWithdrawHistoryResponse{
			Items: []indexer.DepositWithdrawItem{},
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: false,
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/deposits-withdraws/0xNewAddress", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)

	dataBytes, _ := json.Marshal(response.Data)
	var data DepositWithdrawResponse
	json.Unmarshal(dataBytes, &data)

	assert.Empty(t, data.Items)
	assert.False(t, data.Page.HasMore)
}

// TestGetDepositWithdrawHistory_Pagination tests pagination cursor in response.
func TestGetDepositWithdrawHistory_Pagination(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	mockRepo := &MockQueryRepository{
		depositWithdrawHistory: &indexer.DepositWithdrawHistoryResponse{
			Items: make([]indexer.DepositWithdrawItem, 50), // Full page
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: true,
				NextCursor: &indexer.PageCursor{
					AfterTimestamp: now.Add(-time.Hour).Format(time.RFC3339Nano),
					AfterID:        50,
				},
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/deposits-withdraws/0x123?limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	dataBytes, _ := json.Marshal(response.Data)
	var data DepositWithdrawResponse
	json.Unmarshal(dataBytes, &data)

	assert.True(t, data.Page.HasMore)
	assert.NotNil(t, data.Page.NextCursor)
	assert.Equal(t, int64(50), data.Page.NextCursor.AfterID)
}

// TestGetDepositWithdrawHistory_InvalidLimit tests that limit > 100 is capped.
func TestGetDepositWithdrawHistory_InvalidLimit(t *testing.T) {
	mockRepo := &MockQueryRepository{
		depositWithdrawHistory: &indexer.DepositWithdrawHistoryResponse{
			Items: []indexer.DepositWithdrawItem{},
			Page: indexer.PageInfo{
				Limit:   100, // Should be capped at 100
				HasMore: false,
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	// Request with limit > 100 should be capped
	req := httptest.NewRequest("GET", "/api/history/deposits-withdraws/0x123?limit=500", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	dataBytes, _ := json.Marshal(response.Data)
	var data DepositWithdrawResponse
	json.Unmarshal(dataBytes, &data)

	// Limit should be capped at 100
	assert.LessOrEqual(t, data.Page.Limit, 100)
}

// TestGetRentalHistory_Success tests successful rental history retrieval.
func TestGetRentalHistory_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endTime := now.Add(-time.Hour)
	duration := int64(3600)
	costWei := "1000000000000000000"
	stopTxHash := "0xstop123"

	mockRepo := &MockQueryRepository{
		rentalHistory: &indexer.RentalHistoryResponse{
			Items: []indexer.RentalHistoryItem{
				{
					RentalID:        1,
					UserAddress:     "0xuser123",
					ProviderAddress: "0xprovider456",
					StartTime:       now,
					EndTime:         &endTime,
					DurationSeconds: &duration,
					CostWei:         &costWei,
					StartTxHash:     "0xstart123",
					StopTxHash:      &stopTxHash,
					IsActive:        false,
				},
			},
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: false,
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/rentals/0xuser123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Success)

	dataBytes, _ := json.Marshal(response.Data)
	var data RentalHistoryTestResponse
	json.Unmarshal(dataBytes, &data)

	assert.Len(t, data.Items, 1)
	assert.Equal(t, int64(1), data.Items[0].RentalID)
	assert.Equal(t, "0xuser123", data.Items[0].UserAddress)
	assert.False(t, data.Items[0].IsActive)
}

// TestGetRentalHistory_ActiveAndCompleted tests IsActive field for different rental states.
func TestGetRentalHistory_ActiveAndCompleted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endTime := now.Add(-time.Hour)
	duration := int64(3600)
	costWei := "1000000000000000000"
	stopTxHash := "0xstop123"

	mockRepo := &MockQueryRepository{
		rentalHistory: &indexer.RentalHistoryResponse{
			Items: []indexer.RentalHistoryItem{
				{
					RentalID:        1,
					UserAddress:     "0xuser123",
					ProviderAddress: "0xprovider456",
					StartTime:       now,
					EndTime:         nil, // Active rental
					DurationSeconds: nil,
					CostWei:         nil,
					StartTxHash:     "0xstart123",
					StopTxHash:      nil,
					IsActive:        true,
				},
				{
					RentalID:        2,
					UserAddress:     "0xuser123",
					ProviderAddress: "0xprovider789",
					StartTime:       now.Add(-2 * time.Hour),
					EndTime:         &endTime,
					DurationSeconds: &duration,
					CostWei:         &costWei,
					StartTxHash:     "0xstart456",
					StopTxHash:      &stopTxHash,
					IsActive:        false,
				},
			},
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: false,
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/rentals/0xuser123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	dataBytes, _ := json.Marshal(response.Data)
	var data RentalHistoryTestResponse
	json.Unmarshal(dataBytes, &data)

	assert.Len(t, data.Items, 2)
	assert.True(t, data.Items[0].IsActive, "First rental should be active")
	assert.False(t, data.Items[1].IsActive, "Second rental should be completed")
}

// TestGetRentalHistory_Pagination tests pagination cursor in response.
func TestGetRentalHistory_Pagination(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	mockRepo := &MockQueryRepository{
		rentalHistory: &indexer.RentalHistoryResponse{
			Items: make([]indexer.RentalHistoryItem, 50), // Full page
			Page: indexer.PageInfo{
				Limit:   50,
				HasMore: true,
				NextCursor: &indexer.PageCursor{
					AfterTimestamp: now.Add(-time.Hour).Format(time.RFC3339Nano),
					AfterID:        50,
				},
			},
		},
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/rentals/0x123?limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	dataBytes, _ := json.Marshal(response.Data)
	var data RentalHistoryTestResponse
	json.Unmarshal(dataBytes, &data)

	assert.True(t, data.Page.HasMore)
	assert.NotNil(t, data.Page.NextCursor)
}

// TestGetDepositWithdrawHistory_DBError tests database error handling.
func TestGetDepositWithdrawHistory_DBError(t *testing.T) {
	mockRepo := &MockQueryRepository{
		err: errors.New("database connection failed"),
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/deposits-withdraws/0x123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	assert.False(t, response.Success)
	assert.NotNil(t, response.Error)
}

// TestGetRentalHistory_DBError tests database error handling.
func TestGetRentalHistory_DBError(t *testing.T) {
	mockRepo := &MockQueryRepository{
		err: errors.New("database connection failed"),
	}

	router := setupTestHistoryRouter(mockRepo)

	req := httptest.NewRequest("GET", "/api/history/rentals/0x123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response APIResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	assert.False(t, response.Success)
	assert.NotNil(t, response.Error)
}
