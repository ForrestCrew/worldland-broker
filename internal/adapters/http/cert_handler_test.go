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
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/domain"
	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/services"
)

// mockNodeRepoForCert implements domain.NodeRepository for cert handler tests
type mockNodeRepoForCert struct {
	nodes map[string]*domain.Node
}

func newMockNodeRepoForCert() *mockNodeRepoForCert {
	return &mockNodeRepoForCert{
		nodes: make(map[string]*domain.Node),
	}
}

func (m *mockNodeRepoForCert) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepoForCert) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	if node, ok := m.nodes[id]; ok {
		return node, nil
	}
	return nil, assert.AnError
}

func (m *mockNodeRepoForCert) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	var result []*domain.Node
	for _, node := range m.nodes {
		if node.ProviderID == providerID {
			result = append(result, node)
		}
	}
	return result, nil
}

func (m *mockNodeRepoForCert) Update(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepoForCert) Delete(ctx context.Context, id string) error {
	delete(m.nodes, id)
	return nil
}

func (m *mockNodeRepoForCert) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

// generateTestCAForHandler creates a test CA certificate and key
func generateTestCAForHandler(t *testing.T) (caCertPEM, caKeyPEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	caTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Worldland Test CA",
			Organization: []string{"Worldland Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	require.NoError(t, err)
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	return caCertPEM, caKeyPEM
}

func setupCertHandler(t *testing.T) (*httpAdapter.CertHandler, *mockNodeRepoForCert) {
	t.Helper()

	caCertPEM, caKeyPEM := generateTestCAForHandler(t)
	nodeRepo := newMockNodeRepoForCert()

	testNode := &domain.Node{
		ID:             "node-cert-test",
		ProviderID:     "provider-cert-test",
		GPUUUID:        "GPU-CERT",
		GPUType:        "RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "1000000000000000",
		Status:         domain.NodeStatusPending,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	nodeRepo.Create(context.Background(), testNode)

	certService, err := services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, 24*time.Hour)
	require.NoError(t, err)

	handler := httpAdapter.NewCertHandler(certService)
	return handler, nodeRepo
}

func TestCertHandler_IssueCertificate_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupCertHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("provider_id", "provider-cert-test")
	c.Params = gin.Params{{Key: "id", Value: "node-cert-test"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/nodes/node-cert-test/certificate", nil)

	handler.IssueCertificate(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Contains(t, response, "certificate")
	assert.Contains(t, response, "private_key")
	assert.Contains(t, response, "expires_at")
	assert.Contains(t, response, "node_id")
	assert.Contains(t, response, "message")

	// Verify certificate is valid PEM
	certPEM := response["certificate"].(string)
	block, _ := pem.Decode([]byte(certPEM))
	assert.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)
}

func TestCertHandler_IssueCertificate_NotAuthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupCertHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("provider_id", "wrong-provider")
	c.Params = gin.Params{{Key: "id", Value: "node-cert-test"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/nodes/node-cert-test/certificate", nil)

	handler.IssueCertificate(c)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["error"], "Not authorized")
}

func TestCertHandler_IssueCertificate_NodeNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupCertHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("provider_id", "provider-cert-test")
	c.Params = gin.Params{{Key: "id", Value: "nonexistent-node"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/nodes/nonexistent-node/certificate", nil)

	handler.IssueCertificate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["error"], "not found")
}

func TestCertHandler_IssueCertificate_MissingNodeID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupCertHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("provider_id", "provider-cert-test")
	c.Params = gin.Params{{Key: "id", Value: ""}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/nodes//certificate", nil)

	handler.IssueCertificate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["error"], "required")
}

func TestCertHandler_GetRootCA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupCertHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/ca/root", nil)

	handler.GetRootCA(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-pem-file", w.Header().Get("Content-Type"))

	// Verify response is valid PEM certificate
	body := w.Body.Bytes()
	block, _ := pem.Decode(body)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)

	// Verify it's a valid certificate
	_, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
}

func TestCertHandler_IssueCertificate_UpdatesNodeToActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, nodeRepo := setupCertHandler(t)

	// Verify initial status
	node, _ := nodeRepo.GetByID(context.Background(), "node-cert-test")
	assert.Equal(t, domain.NodeStatusPending, node.Status)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("provider_id", "provider-cert-test")
	c.Params = gin.Params{{Key: "id", Value: "node-cert-test"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/nodes/node-cert-test/certificate", nil)

	handler.IssueCertificate(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify node status is now active
	node, _ = nodeRepo.GetByID(context.Background(), "node-cert-test")
	assert.Equal(t, domain.NodeStatusActive, node.Status)
	assert.NotNil(t, node.CertificateExpiry)
}
