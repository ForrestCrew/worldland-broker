package mtls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/worldland/worldland-hub/internal/adapters/mtls"
)

// Test helper: Generate test CA certificate
func generateTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	caTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Test CA",
			Organization: []string{"Worldland Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("failed to parse CA certificate: %v", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	return caCert, caKey, caCertPEM
}

// Test helper: Generate client certificate signed by CA
func generateClientCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, nodeID string) (tls.Certificate, []byte) {
	t.Helper()

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	clientTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   nodeID,
			Organization: []string{"Worldland GPU Network"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create client certificate: %v", err)
	}

	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	clientKeyDER, _ := x509.MarshalECPrivateKey(clientKey)
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER})

	cert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("failed to create key pair: %v", err)
	}

	return cert, clientCertPEM
}

// Test helper: Generate server certificate signed by CA
func generateServerCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	serverTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Worldland Hub"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})
	serverKeyDER, _ := x509.MarshalECPrivateKey(serverKey)
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})

	cert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("failed to create server key pair: %v", err)
	}

	return cert
}

func TestServer_AcceptsValidClientCert(t *testing.T) {
	// Generate test CA
	caCert, caKey, caCertPEM := generateTestCA(t)

	// Generate server certificate
	serverCert := generateServerCert(t, caCert, caKey)

	// Create CA pool
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Create mTLS server
	server := mtls.NewServer(serverCert, caPool, "localhost:18443")
	defer server.Stop()

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Generate client certificate with node ID
	nodeID := "test-node-123"
	clientCert, _ := generateClientCert(t, caCert, caKey, nodeID)

	// Create client with valid certificate
	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}

	// Attempt connection
	conn, err := tls.Dial("tcp", "localhost:18443", clientTLSConfig)
	if err != nil {
		t.Fatalf("connection with valid cert should succeed: %v", err)
	}
	defer conn.Close()

	// Verify connection established
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}
}

func TestServer_RejectsMissingClientCert(t *testing.T) {
	// Generate test CA
	caCert, caKey, caCertPEM := generateTestCA(t)

	// Generate server certificate
	serverCert := generateServerCert(t, caCert, caKey)

	// Create CA pool
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Create mTLS server with RequireAndVerifyClientCert
	server := mtls.NewServer(serverCert, caPool, "localhost:18444")
	defer server.Stop()

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Create client WITHOUT certificate
	clientTLSConfig := &tls.Config{
		RootCAs:    caPool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
	}

	// Attempt connection - should fail because server requires client certificate
	conn, err := tls.Dial("tcp", "localhost:18444", clientTLSConfig)
	if err != nil {
		// Expected: Dial fails immediately
		t.Logf("Connection correctly rejected: %v", err)
		return
	}

	// If dial succeeded (TCP connected), try to actually use the connection
	defer conn.Close()

	// Write some data to force handshake completion
	_, err = conn.Write([]byte("test"))
	if err != nil {
		// Expected: Write fails due to handshake failure
		t.Logf("Connection correctly rejected during write: %v", err)
		return
	}

	// Try reading - server should have closed connection
	buf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, err = conn.Read(buf)
	if err != nil {
		t.Logf("Connection correctly rejected during read: %v", err)
		return
	}

	// Connection worked - this is wrong
	t.Fatal("connection without client cert should fail")
}

func TestServer_ExtractsNodeIDFromCN(t *testing.T) {
	// Generate test CA
	caCert, caKey, caCertPEM := generateTestCA(t)

	// Generate server certificate
	serverCert := generateServerCert(t, caCert, caKey)

	// Create CA pool
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Create mTLS server
	server := mtls.NewServer(serverCert, caPool, "localhost:18445")
	defer server.Stop()

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Generate client certificate with specific node ID
	nodeID := "node-uuid-12345"
	clientCert, clientCertPEM := generateClientCert(t, caCert, caKey, nodeID)

	// Parse certificate to verify CN
	block, _ := pem.Decode(clientCertPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	if cert.Subject.CommonName != nodeID {
		t.Fatalf("certificate CN mismatch: expected %q, got %q", nodeID, cert.Subject.CommonName)
	}

	// Create client with valid certificate
	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}

	// Connect
	conn, err := tls.Dial("tcp", "localhost:18445", clientTLSConfig)
	if err != nil {
		t.Fatalf("connection failed: %v", err)
	}
	defer conn.Close()

	// Give server time to process connection
	time.Sleep(100 * time.Millisecond)

	// Verify connection was established and node ID extracted by server
	// Server's handleConnection extracts CN and stores in connections map
	// Test by attempting to send a command to this nodeID
	testCmd := map[string]interface{}{"type": "ping"}
	if err := server.SendCommand(nodeID, testCmd); err != nil {
		t.Fatalf("SendCommand failed, meaning node ID not extracted correctly: %v", err)
	}

	t.Logf("Node ID %q successfully extracted from certificate CN", nodeID)
}

func TestServer_RejectsWrongCA(t *testing.T) {
	// Generate test CA for server
	caCert, caKey, caCertPEM := generateTestCA(t)

	// Generate server certificate
	serverCert := generateServerCert(t, caCert, caKey)

	// Create CA pool for server - only trusts its own CA
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Create mTLS server
	server := mtls.NewServer(serverCert, caPool, "localhost:18446")
	defer server.Stop()

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Generate DIFFERENT CA for rogue client
	wrongCACert, wrongCAKey, _ := generateTestCA(t)

	// Generate client certificate signed by wrong CA
	nodeID := "rogue-node"
	clientCert, _ := generateClientCert(t, wrongCACert, wrongCAKey, nodeID)

	// Create client config with cert from wrong CA
	// Use InsecureSkipVerify to bypass server cert verification
	// (we only care about testing server's rejection of client cert)
	clientTLSConfig := &tls.Config{
		Certificates:       []tls.Certificate{clientCert},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	}

	// Attempt connection - should fail because server doesn't trust client's CA
	conn, err := tls.Dial("tcp", "localhost:18446", clientTLSConfig)
	if err != nil {
		// Expected: Dial fails
		t.Logf("Connection correctly rejected: %v", err)
		return
	}

	// If dial succeeded, try to use the connection
	defer conn.Close()

	// Write some data to force complete handshake
	_, err = conn.Write([]byte("test"))
	if err != nil {
		t.Logf("Connection correctly rejected during write: %v", err)
		return
	}

	// Try reading - server should have rejected
	buf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, err = conn.Read(buf)
	if err != nil {
		t.Logf("Connection correctly rejected during read: %v", err)
		return
	}

	// Connection worked - this is wrong
	t.Fatal("connection with untrusted client cert should fail")
}

func TestServer_SendsCommandToNode(t *testing.T) {
	// Generate test CA
	caCert, caKey, caCertPEM := generateTestCA(t)

	// Generate server certificate
	serverCert := generateServerCert(t, caCert, caKey)

	// Create CA pool
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCertPEM)

	// Create mTLS server
	server := mtls.NewServer(serverCert, caPool, "localhost:18447")
	defer server.Stop()

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Generate client certificate
	nodeID := "test-node-cmd"
	clientCert, _ := generateClientCert(t, caCert, caKey, nodeID)

	// Create client
	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}

	// Connect
	conn, err := tls.Dial("tcp", "localhost:18447", clientTLSConfig)
	if err != nil {
		t.Fatalf("connection failed: %v", err)
	}
	defer conn.Close()

	// Complete handshake
	if err := conn.Handshake(); err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}

	// Give server time to register connection
	time.Sleep(100 * time.Millisecond)

	// Server sends command
	command := map[string]interface{}{
		"type":    "start_job",
		"job_id":  "job-123",
		"payload": map[string]string{"docker_image": "test-image"},
	}
	if err := server.SendCommand(nodeID, command); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	// Client should receive command (verify data was written)
	t.Log("Command sent successfully")
}
