package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// CertService handles certificate issuance for nodes
// Note: For MVP, we use Hub-managed CA certificates.
// Production would use step-ca ACME integration for automation.
type CertService struct {
	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey
	nodeRepo domain.NodeRepository
	certTTL  time.Duration
}

// NewCertService creates a certificate service
// In production, this would connect to step-ca ACME endpoint
func NewCertService(caCertPEM, caKeyPEM []byte, nodeRepo domain.NodeRepository, ttl time.Duration) (*CertService, error) {
	// Parse CA certificate
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA key: %w", err)
	}

	return &CertService{
		caCert:   caCert,
		caKey:    caKey,
		nodeRepo: nodeRepo,
		certTTL:  ttl,
	}, nil
}

// CertificateBundle contains issued certificate and key
type CertificateBundle struct {
	Certificate []byte    // PEM encoded certificate
	PrivateKey  []byte    // PEM encoded private key
	ExpiresAt   time.Time
	NodeID      string
}

// IssueCertificate creates a new mTLS client certificate for a node
func (s *CertService) IssueCertificate(ctx context.Context, nodeID, providerID string) (*CertificateBundle, error) {
	// Verify node exists and belongs to provider
	node, err := s.nodeRepo.GetByID(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}
	if node.ProviderID != providerID {
		return nil, fmt.Errorf("not authorized")
	}

	// Generate key pair for node
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Create certificate
	notBefore := time.Now()
	notAfter := notBefore.Add(s.certTTL)

	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   nodeID, // CN = node UUID for identification
			Organization: []string{"Worldland GPU Network"},
		},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, // mTLS client auth
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, s.caCert, &privateKey.PublicKey, s.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Update node with certificate expiry and set status to active
	node.CertificateExpiry = &notAfter
	node.Status = domain.NodeStatusActive
	node.UpdatedAt = time.Now()
	if err := s.nodeRepo.Update(ctx, node); err != nil {
		return nil, fmt.Errorf("failed to update node: %w", err)
	}

	return &CertificateBundle{
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		ExpiresAt:   notAfter,
		NodeID:      nodeID,
	}, nil
}

// GetRootCA returns the CA certificate for client verification
func (s *CertService) GetRootCA() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw})
}

// BootstrapBundle contains certificate bundle for initial node setup
type BootstrapBundle struct {
	Certificate []byte    // PEM encoded certificate
	PrivateKey  []byte    // PEM encoded private key
	CACert      []byte    // PEM encoded CA certificate
	ExpiresAt   time.Time
	WalletAddr  string
}

// IssueBootstrapCertificate creates a new mTLS client certificate for a wallet address
// This is used during initial node setup before node registration
// The certificate CN contains the wallet address for identification
func (s *CertService) IssueBootstrapCertificate(ctx context.Context, walletAddress string) (*BootstrapBundle, error) {
	if walletAddress == "" {
		return nil, fmt.Errorf("wallet address is required")
	}

	// Generate key pair for node
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Create certificate
	notBefore := time.Now()
	notAfter := notBefore.Add(s.certTTL)

	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   walletAddress, // CN = wallet address for identification
			Organization: []string{"Worldland GPU Network"},
		},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, // mTLS client auth
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, s.caCert, &privateKey.PublicKey, s.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw})

	return &BootstrapBundle{
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		CACert:      caCertPEM,
		ExpiresAt:   notAfter,
		WalletAddr:  walletAddress,
	}, nil
}
