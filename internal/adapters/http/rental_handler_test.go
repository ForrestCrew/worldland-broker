package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// Mock implementations specific to rental handler tests

type rentalMockNodeLister struct {
	nodes []*domain.Node
	err   error
}

func (m *rentalMockNodeLister) ListActive(ctx context.Context) ([]*domain.Node, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.nodes, nil
}

type rentalMockRentalSessionRepo struct {
	sessions       map[string]*domain.RentalSession
	createErr      error
	getByIDErr     error
	listByUserResp []*domain.RentalSession
	listByUserErr  error
}

func newRentalMockRentalSessionRepo() *rentalMockRentalSessionRepo {
	return &rentalMockRentalSessionRepo{
		sessions: make(map[string]*domain.RentalSession),
	}
}

func (m *rentalMockRentalSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *rentalMockRentalSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	session, ok := m.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *rentalMockRentalSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	for _, s := range m.sessions {
		if s.RentalID != nil && *s.RentalID == rentalID {
			return s, nil
		}
	}
	return nil, errors.New("session not found")
}

func (m *rentalMockRentalSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *rentalMockRentalSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	if m.listByUserErr != nil {
		return nil, m.listByUserErr
	}
	if m.listByUserResp != nil {
		return m.listByUserResp, nil
	}
	var result []*domain.RentalSession
	for _, s := range m.sessions {
		if s.UserAddress == userAddress {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *rentalMockRentalSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	return nil, nil
}

type rentalMockNodeRepo struct {
	nodes map[string]*domain.Node
}

func newRentalMockNodeRepo() *rentalMockNodeRepo {
	return &rentalMockNodeRepo{
		nodes: make(map[string]*domain.Node),
	}
}

func (m *rentalMockNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *rentalMockNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, errors.New("node not found")
	}
	return node, nil
}

func (m *rentalMockNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return nil, nil
}

func (m *rentalMockNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	return nil
}

func (m *rentalMockNodeRepo) Delete(ctx context.Context, id string) error {
	return nil
}

type rentalMockProviderRepo struct {
	providers map[string]*domain.Provider
}

func newRentalMockProviderRepo() *rentalMockProviderRepo {
	return &rentalMockProviderRepo{
		providers: make(map[string]*domain.Provider),
	}
}

func (m *rentalMockProviderRepo) Create(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	return nil
}

func (m *rentalMockProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	provider, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return provider, nil
}

func (m *rentalMockProviderRepo) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	for _, p := range m.providers {
		if p.WalletAddress == walletAddress {
			return p, nil
		}
	}
	return nil, errors.New("provider not found")
}

func (m *rentalMockProviderRepo) Update(ctx context.Context, provider *domain.Provider) error {
	return nil
}

// Helper to setup test router with mock middleware
func setupRentalTestRouter(handler *httpAdapter.RentalHandler, providerID string) *gin.Engine {
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
		rentals.POST("/providers", handler.FindProviders)
		rentals.POST("", handler.CreateSession)
		rentals.GET("", handler.ListSessions)
		rentals.DELETE("/:id", handler.CancelSession)
	}

	return router
}

// Tests

func TestFindProviders_ReturnsMatchingNodes(t *testing.T) {
	// Setup
	nodes := []*domain.Node{
		{
			ID:             "node-1",
			ProviderID:     "provider-1",
			GPUType:        "RTX 4090",
			MemoryGB:       24,
			PricePerSecond: "1000000000000000",
		},
		{
			ID:             "node-2",
			ProviderID:     "provider-2",
			GPUType:        "RTX 4090",
			MemoryGB:       24,
			PricePerSecond: "1500000000000000",
		},
	}

	nodeLister := &rentalMockNodeLister{nodes: nodes}
	matcher := matching.NewProviderMatcher(nodeLister)
	handler := httpAdapter.NewRentalHandler(matcher, nil, nil, nil)
	router := setupRentalTestRouter(handler, "")

	reqBody := map[string]interface{}{
		"gpuType": "RTX 4090",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/providers", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	providers := resp["providers"].([]interface{})
	assert.Equal(t, 2, len(providers))
	assert.Equal(t, 2, int(resp["totalCount"].(float64)))
}

func TestFindProviders_MissingGPUType_Returns400(t *testing.T) {
	nodeLister := &rentalMockNodeLister{nodes: []*domain.Node{}}
	matcher := matching.NewProviderMatcher(nodeLister)
	handler := httpAdapter.NewRentalHandler(matcher, nil, nil, nil)
	router := setupRentalTestRouter(handler, "")

	// Missing required gpuType field
	reqBody := map[string]interface{}{
		"minMemoryGb": 24,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/providers", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadRequest, w.Code)
}

func TestCreateSession_AuthRequired(t *testing.T) {
	handler := httpAdapter.NewRentalHandler(nil, nil, nil, nil)

	// Setup router without auth (no provider_id set)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/rentals", handler.CreateSession)

	reqBody := map[string]interface{}{
		"nodeId":         "node-1",
		"pricePerSecond": "1000000000000000",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusUnauthorized, w.Code)
}

func TestCreateSession_CreatesInPendingState(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	nodeRepo := newRentalMockNodeRepo()
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:             "node-1",
		ProviderID:     "gpu-provider-1",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "1000000000000000",
	}

	// Setup another provider for the GPU node
	providerRepo.providers["gpu-provider-1"] = &domain.Provider{
		ID:            "gpu-provider-1",
		WalletAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"nodeId":         "node-1",
		"pricePerSecond": "1000000000000000",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusCreated, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotEmpty(t, resp["sessionId"])
	assert.Equal(t, "PENDING", resp["state"])
}

func TestListSessions_ReturnsUserSessions(t *testing.T) {
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
	}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("GET", "/api/v1/rentals", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	sessions := resp["sessions"].([]interface{})
	assert.Equal(t, 1, len(sessions))
}

func TestCancelSession_CancelsPending(t *testing.T) {
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
	}

	sessionManager := sessions.NewSessionManager(sessionRepo, nil, nil)
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("DELETE", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	// Verify session is cancelled
	session := sessionRepo.sessions["session-1"]
	assert.Equal(t, domain.RentalStateCancelled, session.State)
}

func TestCancelSession_RejectsNonPending(t *testing.T) {
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
		State:           domain.RentalStateRunning, // Not PENDING
		PricePerSecond:  "1000000000000000",
		CreatedAt:       time.Now(),
	}

	sessionManager := sessions.NewSessionManager(sessionRepo, nil, nil)
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("DELETE", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "PENDING")
}

func TestCancelSession_RejectsOtherUserSession(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678", // User wallet
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x9999999999999999999999999999999999999999", // Different user
		ProviderAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000",
		CreatedAt:       time.Now(),
	}

	sessionManager := sessions.NewSessionManager(sessionRepo, nil, nil)
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("DELETE", "/api/v1/rentals/session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusForbidden, w.Code)
}

func TestCancelSession_SessionNotFound_Returns404(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo() // Empty repo

	sessionManager := sessions.NewSessionManager(sessionRepo, nil, nil)
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("DELETE", "/api/v1/rentals/nonexistent-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusNotFound, w.Code)
}
