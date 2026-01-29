package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/adapters/mtls"
	"github.com/worldland/worldland-hub/internal/adapters/postgres"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/config"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/services"
	"github.com/worldland/worldland-hub/internal/sessions"
)

func main() {
	log.Println("Worldland Hub starting...")

	// Load configuration
	cfg := config.LoadConfig()

	// Initialize PostgreSQL connection
	ctx := context.Background()
	dbPool, err := postgres.NewPool(ctx, postgres.Config{
		Host:     cfg.DBHost,
		Port:     cfg.DBPort,
		User:     cfg.DBUser,
		Password: cfg.DBPassword,
		Database: cfg.DBName,
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbPool.Close()
	log.Println("Connected to PostgreSQL")

	// Initialize repositories
	providerRepo := postgres.NewProviderRepository(dbPool)
	nodeRepo := postgres.NewNodeRepository(dbPool)
	sessionRepo := postgres.NewSessionRepository(dbPool)
	nonceRepo := postgres.NewNonceRepository(dbPool)

	// Initialize auth components
	siweVerifier := auth.NewSIWEVerifier(cfg.SIWEDomain, nonceRepo)
	sessionManager := auth.NewSessionManager(sessionRepo, cfg.SessionTTL)

	// Initialize services
	nodeService := services.NewNodeService(nodeRepo)

	// Initialize certificate service
	certService, err := initCertService(cfg, nodeRepo)
	if err != nil {
		log.Fatalf("Failed to initialize certificate service: %v", err)
	}
	log.Println("Certificate service initialized")

	// Initialize rental session repository
	rentalSessionRepo := postgres.NewRentalSessionRepository(dbPool)

	// Initialize rental services
	rentalSessionManager := sessions.NewSessionManager(rentalSessionRepo, nodeRepo, providerRepo)
	providerMatcher := matching.NewProviderMatcher(nodeRepo)

	// Initialize HTTP handlers
	authHandler := httpAdapter.NewAuthHandler(siweVerifier, sessionManager, nonceRepo, providerRepo)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)
	certHandler := httpAdapter.NewCertHandler(certService)
	rentalHandler := httpAdapter.NewRentalHandler(providerMatcher, rentalSessionManager, rentalSessionRepo, providerRepo)

	// Create router
	router := httpAdapter.NewRouter(authHandler, nodeHandler, certHandler, rentalHandler, sessionManager)

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
	}

	go func() {
		log.Printf("HTTP API listening on port %s", cfg.ServerPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Initialize mTLS server for node connections
	mtlsServer, err := initMTLSServer(cfg, certService)
	if err != nil {
		log.Fatalf("Failed to initialize mTLS server: %v", err)
	}

	// Wire CommandAck response handler - Hub processes node responses
	mtlsServer.OnMessage = func(nodeID string, msg []byte) {
		var ack mtls.CommandAck
		if err := json.Unmarshal(msg, &ack); err != nil {
			log.Printf("Failed to parse CommandAck from node %s: %v", nodeID, err)
			return
		}
		log.Printf("Received CommandAck from node %s: command=%s status=%s",
			nodeID, ack.CommandID, ack.Status)
		// TODO: Update command status in database (Phase 3)
	}

	go func() {
		log.Printf("mTLS server listening on port %s", cfg.MTLSPort)
		if err := mtlsServer.Start(); err != nil {
			log.Fatalf("mTLS server error: %v", err)
		}
	}()

	// Initialize command service
	commandService := services.NewCommandService(mtlsServer)
	_ = commandService // Will be used in Phase 3 for Hub-Node commands

	log.Println("Hub fully initialized")

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpServer.Shutdown(shutdownCtx)
	mtlsServer.Stop()
	log.Println("Shutdown complete")
}

// initCertService initializes the certificate service
// For development, generates a self-signed CA if certs don't exist
func initCertService(cfg *config.Config, nodeRepo domain.NodeRepository) (*services.CertService, error) {
	// Try to load CA certificate and key
	caCertPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		// For development, generate self-signed CA
		log.Println("CA cert not found, generating self-signed CA for development")
		return initDevCertService(nodeRepo, cfg.CertTTL)
	}

	caKeyPEM, err := os.ReadFile(cfg.CAKeyPath)
	if err != nil {
		log.Println("CA key not found, generating self-signed CA for development")
		return initDevCertService(nodeRepo, cfg.CertTTL)
	}

	return services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, cfg.CertTTL)
}

// initDevCertService generates a self-signed CA for development
func initDevCertService(nodeRepo domain.NodeRepository, ttl time.Duration) (*services.CertService, error) {
	// Generate CA key pair
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	// Create CA certificate
	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	caCertTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Worldland Hub CA (DEV)",
			Organization: []string{"Worldland"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caCertTemplate, &caCertTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	caKeyDER, _ := x509.MarshalECPrivateKey(caKey)
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	log.Println("Generated self-signed CA for development")
	return services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, ttl)
}

// initMTLSServer initializes the mTLS server
// For development, generates a self-signed server cert if not found
func initMTLSServer(cfg *config.Config, certService *services.CertService) (*mtls.Server, error) {
	// Try to load server certificate for mTLS
	serverCert, err := tls.LoadX509KeyPair(cfg.MTLSCertPath, cfg.MTLSKeyPath)
	if err != nil {
		// For development, use cert service to generate
		log.Println("mTLS server cert not found, generating for development")
		serverCert, err = generateDevServerCert()
		if err != nil {
			return nil, err
		}
	}

	// Get CA cert pool
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(certService.GetRootCA())

	return mtls.NewServer(serverCert, caCertPool, ":"+cfg.MTLSPort), nil
}

// generateDevServerCert generates a self-signed server certificate for development
func generateDevServerCert() (tls.Certificate, error) {
	// Generate key pair
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "Worldland Hub (DEV)",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}
