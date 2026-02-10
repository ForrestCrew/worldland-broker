package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/auth"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/services"
)

// Mocks for node tests

type mockNodeRepo2 struct {
	nodes      map[string]*domain.Node
	byProvider map[string][]*domain.Node
}

func newMockNodeRepo2() *mockNodeRepo2 {
	return &mockNodeRepo2{
		nodes:      make(map[string]*domain.Node),
		byProvider: make(map[string][]*domain.Node),
	}
}

func (m *mockNodeRepo2) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	m.byProvider[node.ProviderID] = append(m.byProvider[node.ProviderID], node)
	return nil
}

func (m *mockNodeRepo2) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, errors.New("node not found")
	}
	return node, nil
}

func (m *mockNodeRepo2) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	nodes := m.byProvider[providerID]
	if nodes == nil {
		return []*domain.Node{}, nil
	}
	return nodes, nil
}

func (m *mockNodeRepo2) Update(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo2) Delete(ctx context.Context, id string) error {
	delete(m.nodes, id)
	return nil
}

func (m *mockNodeRepo2) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

func (m *mockNodeRepo2) ListActiveGroupedByGPU(ctx context.Context) ([]*domain.GPUTypeGroup, error) {
	return nil, nil
}

func (m *mockNodeRepo2) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	for _, node := range m.nodes {
		if node.GPUUUID == gpuUUID {
			return node, nil
		}
	}
	return nil, errors.New("node not found")
}

type mockSessionRepo2 struct {
	sessions    map[string]*domain.Session
	validTokens map[string]string // token -> providerID
}

func newMockSessionRepo2() *mockSessionRepo2 {
	return &mockSessionRepo2{
		sessions:    make(map[string]*domain.Session),
		validTokens: make(map[string]string),
	}
}

func (m *mockSessionRepo2) Create(ctx context.Context, session *domain.Session) error {
	m.sessions[session.Token] = session
	return nil
}

func (m *mockSessionRepo2) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	session, ok := m.sessions[token]
	if !ok {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *mockSessionRepo2) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockSessionRepo2) DeleteExpired(ctx context.Context) error {
	return nil
}

func setupTestRouterWithAuth(providerID string) (*gin.Engine, *mockNodeRepo2) {
	gin.SetMode(gin.TestMode)

	nodeRepo := newMockNodeRepo2()
	sessionRepo := newMockSessionRepo2()

	// Pre-create a session for authentication
	testToken := "test-valid-token"
	sessionRepo.sessions[testToken] = &domain.Session{
		ID:         "session-1",
		ProviderID: providerID,
		Token:      testToken,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}

	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)
	nodeService := services.NewNodeService(nodeRepo)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)

	// Create a simple router with just node routes for testing
	router := gin.New()
	v1 := router.Group("/api/v1")
	protected := v1.Group("")
	protected.Use(func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "Bearer test-valid-token" {
			c.Set("provider_id", providerID)
			c.Next()
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
		}
	})
	protected.POST("/nodes", nodeHandler.RegisterNode)
	protected.GET("/nodes", nodeHandler.ListNodes)
	protected.GET("/nodes/:id", nodeHandler.GetNode)
	protected.PATCH("/nodes/:id/price", nodeHandler.UpdateNodePrice)

	_ = sessionManager // suppress unused warning

	return router, nodeRepo
}

func TestRegisterNode_ValidRequest_Returns201(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	body := `{
		"gpu_uuid": "GPU-12345",
		"gpu_type": "RTX 4090",
		"memory_gb": 24,
		"price_per_sec": "0.001"
	}`

	req := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.NotEmpty(t, response["node_id"])
	assert.Equal(t, "active", response["status"])
}

func TestRegisterNode_InvalidMemory_Returns400(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	// RTX 4090 max is 24GB, claiming 48GB should fail
	body := `{
		"gpu_uuid": "GPU-12345",
		"gpu_type": "RTX 4090",
		"memory_gb": 48,
		"price_per_sec": "0.001"
	}`

	req := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid memory")
}

func TestRegisterNode_InvalidPrice_Returns400(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	body := `{
		"gpu_uuid": "GPU-12345",
		"gpu_type": "RTX 4090",
		"memory_gb": 24,
		"price_per_sec": "-0.001"
	}`

	req := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "price must be positive")
}

func TestRegisterNode_NoAuth_Returns401(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	body := `{
		"gpu_uuid": "GPU-12345",
		"gpu_type": "RTX 4090",
		"memory_gb": 24,
		"price_per_sec": "0.001"
	}`

	req := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateNodePrice_Success(t *testing.T) {
	router, nodeRepo := setupTestRouterWithAuth("provider-123")

	// Pre-create a node
	nodeRepo.nodes["node-123"] = &domain.Node{
		ID:             "node-123",
		ProviderID:     "provider-123",
		GPUUUID:        "GPU-12345",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "0.001",
		Status:         domain.NodeStatusActive,
	}

	body := `{"price_per_sec": "0.002"}`
	req := httptest.NewRequest("PATCH", "/api/v1/nodes/node-123/price", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "0.002", response["price_per_sec"])
}

func TestUpdateNodePrice_WrongOwner_Returns403(t *testing.T) {
	router, nodeRepo := setupTestRouterWithAuth("provider-123")

	// Pre-create a node owned by different provider
	nodeRepo.nodes["node-456"] = &domain.Node{
		ID:             "node-456",
		ProviderID:     "provider-other", // Different owner
		GPUUUID:        "GPU-67890",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "0.001",
		Status:         domain.NodeStatusActive,
	}

	body := `{"price_per_sec": "0.002"}`
	req := httptest.NewRequest("PATCH", "/api/v1/nodes/node-456/price", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateNodePrice_NotFound_Returns404(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	body := `{"price_per_sec": "0.002"}`
	req := httptest.NewRequest("PATCH", "/api/v1/nodes/nonexistent/price", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListNodes_ReturnsProviderNodes(t *testing.T) {
	router, nodeRepo := setupTestRouterWithAuth("provider-123")

	// Pre-create nodes
	node := &domain.Node{
		ID:             "node-123",
		ProviderID:     "provider-123",
		GPUUUID:        "GPU-12345",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "0.001",
		Status:         domain.NodeStatusActive,
	}
	nodeRepo.nodes["node-123"] = node
	nodeRepo.byProvider["provider-123"] = []*domain.Node{node}

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(1), response["count"])
}

func TestListNodes_EmptyList(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(0), response["count"])
}

func TestGetNode_Found(t *testing.T) {
	router, nodeRepo := setupTestRouterWithAuth("provider-123")

	// Pre-create a node
	nodeRepo.nodes["node-123"] = &domain.Node{
		ID:             "node-123",
		ProviderID:     "provider-123",
		GPUUUID:        "GPU-12345",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "0.001",
		Status:         domain.NodeStatusActive,
	}

	req := httptest.NewRequest("GET", "/api/v1/nodes/node-123", nil)
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "node-123", response["id"])
}

func TestGetNode_NotFound(t *testing.T) {
	router, _ := setupTestRouterWithAuth("provider-123")

	req := httptest.NewRequest("GET", "/api/v1/nodes/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer test-valid-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
