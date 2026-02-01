package http_test

import (
	"bytes"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/domain"
)

// Helper to setup test router with ConfirmationHandler
func setupConfirmationTestRouter(handler *httpAdapter.ConfirmationHandler, providerID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Mock auth middleware that sets provider_id in context
	mockAuth := func(c *gin.Context) {
		if providerID != "" {
			c.Set("provider_id", providerID)
		}
		c.Next()
	}

	api := router.Group("/api/v1")
	rentals := api.Group("/rentals")
	rentals.Use(mockAuth)
	{
		rentals.POST("/:id/confirm", handler.ConfirmRental)
		rentals.GET("/:id", handler.GetSession)
	}

	return router
}

// Tests for ConfirmRental

func TestConfirmRental_Success_Returns202(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusAccepted, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["sessionId"])
	assert.Equal(t, "PENDING", resp["state"])
	assert.Contains(t, resp["message"], "verification")
}

func TestConfirmRental_Idempotent_SameTxHash(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000",
		TxHash:          &txHash, // Already has txHash
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": txHash, // Same txHash
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return 202 Accepted for idempotent request
	assert.Equal(t, nethttp.StatusAccepted, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["sessionId"])
}

func TestConfirmRental_Conflict_DifferentSession(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	sessionRepo := newRentalMockRentalSessionRepo()
	// Session 1 already has this txHash
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x9999999999999999999999999999999999999999",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		TxHash:          &txHash,
		CreatedAt:       time.Now(),
	}
	// Session 2 is the user's session, trying to use the same txHash
	sessionRepo.sessions["session-2"] = &domain.RentalSession{
		ID:              "session-2",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		CreatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": txHash, // Already used by session-1
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-2/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusConflict, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "CONFIRM_003", resp["code"])
	assert.Contains(t, resp["error"], "already used")
}

func TestConfirmRental_InvalidTxHashFormat(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		CreatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	testCases := []struct {
		name   string
		txHash string
	}{
		{"Too short", "0x1234567890abcdef"},
		{"Too long", "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef00"},
		{"No 0x prefix", "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"},
		{"Invalid characters", "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdeg"},
		{"Empty", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reqBody := map[string]interface{}{
				"txHash": tc.txHash,
			}
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, nethttp.StatusBadRequest, w.Code, "Expected 400 for txHash: %s", tc.txHash)
		})
	}
}

func TestConfirmRental_SessionNotFound(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo() // Empty

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/nonexistent/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusNotFound, w.Code)
}

func TestConfirmRental_NotOwner(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x9999999999999999999999999999999999999999", // Different user
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		CreatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusForbidden, w.Code)
}

func TestConfirmRental_NotPending(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStateRunning, // Not PENDING
		CreatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"txHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "CONFIRM_002", resp["code"])
	assert.Contains(t, resp["error"], "PENDING")
}

func TestConfirmRental_AuthRequired(t *testing.T) {
	sessionRepo := newRentalMockRentalSessionRepo()
	providerRepo := newRentalMockProviderRepo()

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)

	// Setup router WITHOUT auth middleware
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/rentals/:id/confirm", handler.ConfirmRental)

	reqBody := map[string]interface{}{
		"txHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/confirm", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusUnauthorized, w.Code)
}

// Tests for GetSession

func TestGetSession_ReturnsSessionDetails(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	now := time.Now()
	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000",
		TxHash:          &txHash,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	req := httptest.NewRequest("GET", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["id"])
	assert.Equal(t, "PENDING", resp["state"])
	assert.Equal(t, "node-1", resp["nodeId"])
	assert.Equal(t, txHash, resp["txHash"])
}

func TestGetSession_NotFound(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo() // Empty

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	req := httptest.NewRequest("GET", "/api/v1/rentals/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusNotFound, w.Code)
}

func TestGetSession_NotAuthorized(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x9999999999999999999999999999999999999999", // Different user
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		CreatedAt:       time.Now(),
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	req := httptest.NewRequest("GET", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusForbidden, w.Code)
}

func TestGetSession_RunningWithSSHCredentials(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	now := time.Now()
	startTime := now.Add(-5 * time.Minute)
	rentalID := uint64(12345)
	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStateRunning,
		PricePerSecond:  "1000000000000000",
		RentalID:        &rentalID,
		StartTime:       &startTime,
		CreatedAt:       now.Add(-10 * time.Minute),
		UpdatedAt:       now,
	}

	handler := httpAdapter.NewConfirmationHandler(sessionRepo, providerRepo)
	router := setupConfirmationTestRouter(handler, "provider-123")

	req := httptest.NewRequest("GET", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["id"])
	assert.Equal(t, "RUNNING", resp["state"])
	assert.Equal(t, float64(12345), resp["rentalId"])
	assert.NotNil(t, resp["startTime"])
}
