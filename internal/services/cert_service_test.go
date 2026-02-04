package services_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/services"
)

// mockNodeRepo implements domain.NodeRepository for testing
type mockNodeRepo struct {
	nodes map[string]*domain.Node
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{
		nodes: make(map[string]*domain.Node),
	}
}

func (m *mockNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	if node, ok := m.nodes[id]; ok {
		return node, nil
	}
	return nil, assert.AnError
}

func (m *mockNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	var result []*domain.Node
	for _, node := range m.nodes {
		if node.ProviderID == providerID {
			result = append(result, node)
		}
	}
	return result, nil
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

func (m *mockNodeRepo) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	for _, node := range m.nodes {
		if node.GPUUUID == gpuUUID {
			return node, nil
		}
	}
	return nil, assert.AnError
}

// generateTestCA creates a test CA certificate and key for testing
func generateTestCA(t *testing.T) (caCertPEM, caKeyPEM []byte) {
	t.Helper()

	// Generate CA key
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Create CA certificate template
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

	// Self-sign the CA certificate
	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	// Encode CA certificate to PEM
	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// Encode CA key to PEM
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	require.NoError(t, err)
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	return caCertPEM, caKeyPEM
}

func setupTestCertService(t *testing.T) (*services.CertService, *mockNodeRepo) {
	t.Helper()

	caCertPEM, caKeyPEM := generateTestCA(t)
	nodeRepo := newMockNodeRepo()

	// Add a test node
	testNode := &domain.Node{
		ID:             "node-123",
		ProviderID:     "provider-456",
		GPUUUID:        "GPU-ABC",
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

	return certService, nodeRepo
}

func TestNewCertService_Success(t *testing.T) {
	caCertPEM, caKeyPEM := generateTestCA(t)
	nodeRepo := newMockNodeRepo()

	certService, err := services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, 24*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, certService)
}

func TestNewCertService_InvalidCertPEM(t *testing.T) {
	_, caKeyPEM := generateTestCA(t)
	nodeRepo := newMockNodeRepo()

	_, err := services.NewCertService([]byte("invalid"), caKeyPEM, nodeRepo, 24*time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode CA certificate PEM")
}

func TestNewCertService_InvalidKeyPEM(t *testing.T) {
	caCertPEM, _ := generateTestCA(t)
	nodeRepo := newMockNodeRepo()

	_, err := services.NewCertService(caCertPEM, []byte("invalid"), nodeRepo, 24*time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode CA key PEM")
}

func TestCertService_IssueCertificate_Success(t *testing.T) {
	certService, _ := setupTestCertService(t)

	bundle, err := certService.IssueCertificate(
		context.Background(),
		"node-123",
		"provider-456",
	)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// Verify certificate can be parsed
	block, _ := pem.Decode(bundle.Certificate)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// Verify certificate properties
	assert.Equal(t, "node-123", cert.Subject.CommonName)
	assert.Contains(t, cert.Subject.Organization, "Worldland GPU Network")
	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)

	// Verify private key can be parsed
	keyBlock, _ := pem.Decode(bundle.PrivateKey)
	require.NotNil(t, keyBlock)

	_, err = x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)

	// Verify expiry
	assert.Equal(t, "node-123", bundle.NodeID)
	assert.True(t, bundle.ExpiresAt.After(time.Now()))
	assert.True(t, bundle.ExpiresAt.Before(time.Now().Add(25*time.Hour)))
}

func TestCertService_IssueCertificate_24HourTTL(t *testing.T) {
	certService, _ := setupTestCertService(t)

	bundle, err := certService.IssueCertificate(
		context.Background(),
		"node-123",
		"provider-456",
	)
	require.NoError(t, err)

	// Verify 24-hour TTL
	expectedExpiry := time.Now().Add(24 * time.Hour)
	diff := bundle.ExpiresAt.Sub(expectedExpiry)
	assert.True(t, diff < time.Minute && diff > -time.Minute, "Expiry should be within 1 minute of 24 hours from now")
}

func TestCertService_IssueCertificate_UpdatesNodeStatus(t *testing.T) {
	certService, nodeRepo := setupTestCertService(t)

	// Verify initial status is pending
	node, _ := nodeRepo.GetByID(context.Background(), "node-123")
	assert.Equal(t, domain.NodeStatusPending, node.Status)

	_, err := certService.IssueCertificate(
		context.Background(),
		"node-123",
		"provider-456",
	)
	require.NoError(t, err)

	// Verify status updated to active
	node, _ = nodeRepo.GetByID(context.Background(), "node-123")
	assert.Equal(t, domain.NodeStatusActive, node.Status)
	assert.NotNil(t, node.CertificateExpiry)
}

func TestCertService_IssueCertificate_NotAuthorized(t *testing.T) {
	certService, _ := setupTestCertService(t)

	_, err := certService.IssueCertificate(
		context.Background(),
		"node-123",
		"wrong-provider",
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")
}

func TestCertService_IssueCertificate_NodeNotFound(t *testing.T) {
	certService, _ := setupTestCertService(t)

	_, err := certService.IssueCertificate(
		context.Background(),
		"nonexistent-node",
		"provider-456",
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node not found")
}

func TestCertService_GetRootCA(t *testing.T) {
	certService, _ := setupTestCertService(t)

	caCert := certService.GetRootCA()
	require.NotEmpty(t, caCert)

	// Verify it's valid PEM
	block, _ := pem.Decode(caCert)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)

	// Verify it's a valid certificate
	_, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
}

func TestCertService_IssueCertificate_HasClientAuthEKU(t *testing.T) {
	certService, _ := setupTestCertService(t)

	bundle, err := certService.IssueCertificate(
		context.Background(),
		"node-123",
		"provider-456",
	)
	require.NoError(t, err)

	block, _ := pem.Decode(bundle.Certificate)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// Certificate MUST have ClientAuth EKU for mTLS
	found := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			found = true
			break
		}
	}
	assert.True(t, found, "Certificate must have ExtKeyUsageClientAuth for mTLS")
}

func TestCertService_IssueCertificate_CertificateChainValid(t *testing.T) {
	caCertPEM, caKeyPEM := generateTestCA(t)
	nodeRepo := newMockNodeRepo()

	testNode := &domain.Node{
		ID:             "node-chain-test",
		ProviderID:     "provider-chain",
		GPUUUID:        "GPU-CHAIN",
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

	bundle, err := certService.IssueCertificate(
		context.Background(),
		"node-chain-test",
		"provider-chain",
	)
	require.NoError(t, err)

	// Parse issued certificate
	certBlock, _ := pem.Decode(bundle.Certificate)
	issuedCert, err := x509.ParseCertificate(certBlock.Bytes)
	require.NoError(t, err)

	// Parse CA certificate
	caBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)

	// Create certificate pool with CA
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	// Verify the chain
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	_, err = issuedCert.Verify(opts)
	assert.NoError(t, err, "Issued certificate should be verifiable against CA")
}
