package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/rental"
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

func (m *rentalMockRentalSessionRepo) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	return nil
}

func (m *rentalMockRentalSessionRepo) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	for _, s := range m.sessions {
		if s.TxHash != nil && *s.TxHash == txHash {
			return s, nil
		}
	}
	return nil, errors.New("session not found")
}

func (m *rentalMockRentalSessionRepo) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	if s, ok := m.sessions[sessionID]; ok {
		s.TxHash = &txHash
		return nil
	}
	return errors.New("session not found")
}

func (m *rentalMockRentalSessionRepo) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (m *rentalMockRentalSessionRepo) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *rentalMockRentalSessionRepo) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	return nil
}

func (m *rentalMockRentalSessionRepo) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	return "ext-record-id", nil
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

func (m *rentalMockNodeRepo) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

func (m *rentalMockNodeRepo) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	for _, node := range m.nodes {
		if node.GPUUUID == gpuUUID {
			return node, nil
		}
	}
	return nil, errors.New("node not found")
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

// Mock NodeClient for testing
type mockNodeClient struct {
	startRentalResp *rental.StartRentalResponse
	startRentalErr  error
	stopRentalResp  *rental.StopRentalResponse
	stopRentalErr   error
}

func (m *mockNodeClient) StartRental(ctx context.Context, nodeURL string, req rental.StartRentalRequest) (*rental.StartRentalResponse, error) {
	if m.startRentalErr != nil {
		return nil, m.startRentalErr
	}
	return m.startRentalResp, nil
}

func (m *mockNodeClient) StopRental(ctx context.Context, nodeURL string, req rental.StopRentalRequest) (*rental.StopRentalResponse, error) {
	if m.stopRentalErr != nil {
		return nil, m.stopRentalErr
	}
	return m.stopRentalResp, nil
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
		rentals.POST("/:id/start", handler.HandleStartRental) // 04-05
		rentals.POST("/:id/stop", handler.HandleStopRental)   // 04-05
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
	handler := httpAdapter.NewRentalHandler(matcher, nil, nil, nil, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(matcher, nil, nil, nil, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(nil, nil, nil, nil, nil, nil, nil)

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

	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
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

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
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
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("DELETE", "/api/v1/rentals/nonexistent-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusNotFound, w.Code)
}

// Tests for 04-05: HandleStartRental and HandleStopRental

func TestHandleStartRental_Success(t *testing.T) {
	// Setup mocks
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	nodeRepo := newRentalMockNodeRepo()
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:          "node-1",
		GPUUUID:     "GPU-uuid-123",
		APIEndpoint: "https://node.example.com:8443",
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

	nodeClient := &mockNodeClient{
		startRentalResp: &rental.StartRentalResponse{
			SessionID:  "session-1",
			SSHHost:    "node.example.com",
			SSHPort:    30001,
			SSHUser:    "ubuntu",
			SSHCommand: "ssh -p 30001 ubuntu@node.example.com",
		},
	}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"sshPublicKey": "ssh-rsa AAAA...",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/start", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["sessionId"])
	assert.Equal(t, "node.example.com", resp["sshHost"])
	assert.Equal(t, float64(30001), resp["sshPort"])
	assert.Equal(t, "ubuntu", resp["sshUser"])
	assert.Contains(t, resp["message"], "started")
}

func TestHandleStartRental_SessionNotFound_Returns404(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo() // Empty
	nodeRepo := newRentalMockNodeRepo()
	nodeClient := &mockNodeClient{}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"sshPublicKey": "ssh-rsa AAAA...",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/nonexistent/start", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusNotFound, w.Code)
}

func TestHandleStartRental_NotAuthorized_Returns403(t *testing.T) {
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

	nodeRepo := newRentalMockNodeRepo()
	nodeClient := &mockNodeClient{}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"sshPublicKey": "ssh-rsa AAAA...",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/start", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusForbidden, w.Code)
}

func TestHandleStartRental_NodeUnreachable_Returns502(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	nodeRepo := newRentalMockNodeRepo()
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:          "node-1",
		GPUUUID:     "GPU-uuid-123",
		APIEndpoint: "https://node.example.com:8443",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		CreatedAt:       time.Now(),
	}

	nodeClient := &mockNodeClient{
		startRentalErr: rental.ErrNodeUnreachable,
	}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"sshPublicKey": "ssh-rsa AAAA...",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/start", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadGateway, w.Code)
}

func TestHandleStopRental_Success(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	nodeRepo := newRentalMockNodeRepo()
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:          "node-1",
		GPUUUID:     "GPU-uuid-123",
		APIEndpoint: "https://node.example.com:8443",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		NodeID:          "node-1",
		State:           domain.RentalStateRunning, // Must be RUNNING to stop
		CreatedAt:       time.Now(),
	}

	nodeClient := &mockNodeClient{
		stopRentalResp: &rental.StopRentalResponse{
			SessionID: "session-1",
			Message:   "Rental stopped",
		},
	}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/stop", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "session-1", resp["sessionId"])
	assert.Contains(t, resp["message"], "stop")
}

func TestHandleStopRental_NotRunning_Returns400(t *testing.T) {
	providerRepo := newRentalMockProviderRepo()
	providerRepo.providers["provider-123"] = &domain.Provider{
		ID:            "provider-123",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     "0x1234567890abcdef1234567890abcdef12345678",
		NodeID:          "node-1",
		State:           domain.RentalStatePending, // Not RUNNING
		CreatedAt:       time.Now(),
	}

	nodeRepo := newRentalMockNodeRepo()
	nodeClient := &mockNodeClient{}

	handler := httpAdapter.NewRentalHandler(nil, nil, sessionRepo, providerRepo, nodeRepo, nodeClient, nil)
	router := setupRentalTestRouter(handler, "provider-123")

	req := httptest.NewRequest("POST", "/api/v1/rentals/session-1/stop", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadRequest, w.Code)
}

// Mock BalanceValidator for testing (06-06)
type mockBalanceValidator struct {
	hasSufficient  bool
	currentBalance *big.Int
	err            error
}

func (m *mockBalanceValidator) GetDepositBalance(ctx context.Context, userAddress common.Address) (*big.Int, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.currentBalance, nil
}

func (m *mockBalanceValidator) ValidateDepositBalance(ctx context.Context, userAddress common.Address, requiredAmount *big.Int) (bool, *big.Int, error) {
	if m.err != nil {
		return false, nil, m.err
	}
	return m.hasSufficient, m.currentBalance, nil
}

// Tests for 06-06: Balance validation in CreateSession

func TestCreateSession_InsufficientBalance_Returns400(t *testing.T) {
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

	providerRepo.providers["gpu-provider-1"] = &domain.Provider{
		ID:            "gpu-provider-1",
		WalletAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// Mock balance validator with insufficient balance
	balanceValidator := &mockBalanceValidator{
		hasSufficient:  false,
		currentBalance: big.NewInt(100000000000000), // 0.0001 ETH (insufficient)
	}

	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, balanceValidator)
	router := setupRentalTestRouter(handler, "provider-123")

	reqBody := map[string]interface{}{
		"nodeId":         "node-1",
		"pricePerSecond": "1000000000000000", // 0.001 ETH/sec, needs 3.6 ETH for 1 hour
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/rentals", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, nethttp.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Verify Korean error message and error code
	assert.Equal(t, "예치금이 부족합니다", resp["error"])
	assert.Equal(t, "BAL_002", resp["code"])

	// Verify details contains required and current amounts
	details, ok := resp["details"].(map[string]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, details["required"])
	assert.NotEmpty(t, details["current"])
}

func TestCreateSession_SufficientBalance_Succeeds(t *testing.T) {
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

	providerRepo.providers["gpu-provider-1"] = &domain.Provider{
		ID:            "gpu-provider-1",
		WalletAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// Mock balance validator with sufficient balance
	// 1 hour at 0.001 ETH/sec = 3.6 ETH required
	balanceValidator := &mockBalanceValidator{
		hasSufficient:  true,
		currentBalance: new(big.Int).SetUint64(10000000000000000000), // 10 ETH (sufficient)
	}

	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, balanceValidator)
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

func TestCreateSession_BalanceValidationError_Returns500(t *testing.T) {
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

	providerRepo.providers["gpu-provider-1"] = &domain.Provider{
		ID:            "gpu-provider-1",
		WalletAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// Mock balance validator with error
	balanceValidator := &mockBalanceValidator{
		err: errors.New("blockchain connection failed"),
	}

	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, balanceValidator)
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

	assert.Equal(t, nethttp.StatusInternalServerError, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "failed to validate balance", resp["error"])
	assert.Equal(t, "BAL_001", resp["code"])
}

func TestCreateSession_NoBalanceValidator_SkipsCheck(t *testing.T) {
	// Setup mocks (same as TestCreateSession_CreatesInPendingState but no balanceValidator)
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

	providerRepo.providers["gpu-provider-1"] = &domain.Provider{
		ID:            "gpu-provider-1",
		WalletAddress: "0xabcdef1234567890abcdef1234567890abcdef12",
	}

	sessionRepo := newRentalMockRentalSessionRepo()
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// No balance validator - should skip balance check and create session
	handler := httpAdapter.NewRentalHandler(nil, sessionManager, sessionRepo, providerRepo, nil, nil, nil)
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

	// Should succeed because balance check is skipped when validator is nil
	assert.Equal(t, nethttp.StatusCreated, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotEmpty(t, resp["sessionId"])
	assert.Equal(t, "PENDING", resp["state"])
}
