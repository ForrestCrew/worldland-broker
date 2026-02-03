package http_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
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

// Mock implementations for testing

type mockNonceRepo struct {
	nonces map[string]time.Time
}

func newMockNonceRepo() *mockNonceRepo {
	return &mockNonceRepo{nonces: make(map[string]time.Time)}
}

func (m *mockNonceRepo) Save(ctx context.Context, nonce string, expiresAt time.Time) error {
	m.nonces[nonce] = expiresAt
	return nil
}

func (m *mockNonceRepo) ConsumeIfValid(ctx context.Context, nonce string) (bool, error) {
	exp, ok := m.nonces[nonce]
	if !ok || time.Now().After(exp) {
		return false, nil
	}
	delete(m.nonces, nonce)
	return true, nil
}

func (m *mockNonceRepo) CleanupExpired(ctx context.Context) error {
	now := time.Now()
	for nonce, exp := range m.nonces {
		if now.After(exp) {
			delete(m.nonces, nonce)
		}
	}
	return nil
}

type mockProviderRepo struct {
	providers map[string]*domain.Provider
	byWallet  map[string]*domain.Provider
}

func newMockProviderRepo() *mockProviderRepo {
	return &mockProviderRepo{
		providers: make(map[string]*domain.Provider),
		byWallet:  make(map[string]*domain.Provider),
	}
}

func (m *mockProviderRepo) Create(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	m.byWallet[provider.WalletAddress] = provider
	return nil
}

func (m *mockProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	provider, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return provider, nil
}

func (m *mockProviderRepo) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	provider, ok := m.byWallet[walletAddress]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return provider, nil
}

func (m *mockProviderRepo) Update(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	m.byWallet[provider.WalletAddress] = provider
	return nil
}

type mockSessionRepo struct {
	sessions map[string]*domain.Session
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[string]*domain.Session)}
}

func (m *mockSessionRepo) Create(ctx context.Context, session *domain.Session) error {
	m.sessions[session.Token] = session
	return nil
}

func (m *mockSessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	session, ok := m.sessions[token]
	if !ok {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *mockSessionRepo) Delete(ctx context.Context, id string) error {
	for token, session := range m.sessions {
		if session.ID == id {
			delete(m.sessions, token)
			return nil
		}
	}
	return nil
}

func (m *mockSessionRepo) DeleteExpired(ctx context.Context) error {
	return nil
}

type mockNodeRepo struct {
	nodes      map[string]*domain.Node
	byProvider map[string][]*domain.Node
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{
		nodes:      make(map[string]*domain.Node),
		byProvider: make(map[string][]*domain.Node),
	}
}

func (m *mockNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	m.byProvider[node.ProviderID] = append(m.byProvider[node.ProviderID], node)
	return nil
}

func (m *mockNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, errors.New("node not found")
	}
	return node, nil
}

func (m *mockNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	nodes := m.byProvider[providerID]
	if nodes == nil {
		return []*domain.Node{}, nil
	}
	return nodes, nil
}

func (m *mockNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo) Delete(ctx context.Context, id string) error {
	delete(m.nodes, id)
	return nil
}

func (m *mockNodeRepo) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

// generateTestCAForRouter creates a test CA for router tests
func generateTestCAForRouter() (caCertPEM, caKeyPEM []byte, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	caTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Test CA",
			Organization: []string{"Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	caKeyDER, _ := x509.MarshalECPrivateKey(caKey)
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	return caCertPEM, caKeyPEM, nil
}

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)

	nonceRepo := newMockNonceRepo()
	providerRepo := newMockProviderRepo()
	sessionRepo := newMockSessionRepo()
	nodeRepo := newMockNodeRepo()

	siweVerifier := auth.NewSIWEVerifier("hub.worldland.io", nonceRepo)
	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)
	nodeService := services.NewNodeService(nodeRepo)

	// Create test CA and cert service
	caCertPEM, caKeyPEM, _ := generateTestCAForRouter()
	certService, _ := services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, 24*time.Hour)

	authHandler := httpAdapter.NewAuthHandler(
		siweVerifier,
		sessionManager,
		nonceRepo,
		providerRepo,
	)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)
	certHandler := httpAdapter.NewCertHandler(certService)

	// RentalHandler, ConfirmationHandler, BalanceHandler, HistoryHandler, MonitoringHandler are nil for auth tests - those routes won't be used
	return httpAdapter.NewRouter(authHandler, nodeHandler, certHandler, nil, nil, nil, nil, nil, sessionManager)
}

func TestGetNonce_ReturnsUniqueNonce(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("GET", "/api/v1/auth/nonce", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotEmpty(t, response["nonce"])
	assert.NotEmpty(t, response["expires_at"])

	// Nonce should be hex encoded (32 chars for 16 bytes)
	nonce, ok := response["nonce"].(string)
	assert.True(t, ok)
	assert.Len(t, nonce, 32)
}

func TestGetNonce_ReturnsUniqueNoncesEachCall(t *testing.T) {
	router := setupTestRouter()

	nonces := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/v1/auth/nonce", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &response)

		nonce := response["nonce"].(string)
		assert.False(t, nonces[nonce], "Nonce should be unique")
		nonces[nonce] = true
	}
}

func TestLogin_MissingBody_Returns400(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogin_InvalidSIWE_Returns401(t *testing.T) {
	router := setupTestRouter()

	body := `{"message": "invalid message", "signature": "0x1234"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProtectedRoute_NoToken_Returns401(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "Missing authorization header")
}

func TestProtectedRoute_InvalidTokenFormat_Returns401(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "InvalidFormat token123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "Invalid authorization format")
}

func TestProtectedRoute_InvalidToken_Returns401(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer invalidtoken123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "Invalid or expired session")
}

func TestHealthCheck_ReturnsOK(t *testing.T) {
	router := setupTestRouter()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "ok", response["status"])
}
