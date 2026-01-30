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
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"

	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/adapters/mtls"
	"github.com/worldland/worldland-hub/internal/adapters/postgres"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/config"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/rental"
	"github.com/worldland/worldland-hub/internal/services"
	"github.com/worldland/worldland-hub/internal/sessions"
)

func main() {
	// Initialize structured logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("Worldland Hub starting...")

	// Load configuration
	cfg := config.LoadConfig()

	// Create cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize PostgreSQL connection
	dbPool, err := postgres.NewPool(ctx, postgres.Config{
		Host:     cfg.DBHost,
		Port:     cfg.DBPort,
		User:     cfg.DBUser,
		Password: cfg.DBPassword,
		Database: cfg.DBName,
	})
	if err != nil {
		logger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	logger.Info("Connected to PostgreSQL")

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
	certService, err := initCertService(cfg, nodeRepo, logger)
	if err != nil {
		logger.Error("Failed to initialize certificate service", "error", err)
		os.Exit(1)
	}
	logger.Info("Certificate service initialized")

	// Initialize rental session repository
	rentalSessionRepo := postgres.NewRentalSessionRepository(dbPool)

	// Initialize rental services
	rentalSessionManager := sessions.NewSessionManager(rentalSessionRepo, nodeRepo, providerRepo)
	providerMatcher := matching.NewProviderMatcher(nodeRepo)

	// Initialize blockchain components
	checkpointStore := blockchain.NewCheckpointStore(dbPool)
	eventProcessor := blockchain.NewEventProcessor(rentalSessionManager, rentalSessionRepo, logger)

	// Initialize event listener (if enabled and contract address configured)
	var eventListener *blockchain.EventListener
	if cfg.Blockchain.ListenerEnabled && cfg.Blockchain.ContractAddress != "" {
		contractAddr := common.HexToAddress(cfg.Blockchain.ContractAddress)
		eventListener = blockchain.NewEventListener(
			cfg.Blockchain.RPCEndpoints,
			contractAddr,
			checkpointStore,
			eventProcessor,
			logger,
		)
		logger.Info("Event listener configured",
			"contract", cfg.Blockchain.ContractAddress,
			"endpoints", cfg.Blockchain.RPCEndpoints,
		)
	} else {
		logger.Warn("Event listener disabled",
			"enabled", cfg.Blockchain.ListenerEnabled,
			"hasContract", cfg.Blockchain.ContractAddress != "",
		)
	}

	// Initialize timeout enforcer for stale session cleanup
	timeoutEnforcer := sessions.NewTimeoutEnforcer(rentalSessionManager, rentalSessionRepo, logger)

	// Initialize Node client for Hub-to-Node communication (04-05)
	// For now, use nil TLSConfig - will be configured with mTLS in production
	nodeClient := rental.NewNodeClient(rental.NodeClientConfig{
		TLSConfig: nil, // TODO: Configure mTLS for Hub-to-Node communication
		Timeout:   2 * time.Minute,
	})

	// Initialize HTTP handlers
	authHandler := httpAdapter.NewAuthHandler(siweVerifier, sessionManager, nonceRepo, providerRepo)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)
	certHandler := httpAdapter.NewCertHandler(certService)
	rentalHandler := httpAdapter.NewRentalHandler(providerMatcher, rentalSessionManager, rentalSessionRepo, providerRepo, nodeRepo, nodeClient)

	// Create router
	router := httpAdapter.NewRouter(authHandler, nodeHandler, certHandler, rentalHandler, sessionManager)

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
	}

	go func() {
		logger.Info("HTTP API listening", "port", cfg.ServerPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Initialize mTLS server for node connections
	mtlsServer, err := initMTLSServer(cfg, certService, logger)
	if err != nil {
		logger.Error("Failed to initialize mTLS server", "error", err)
		os.Exit(1)
	}

	// Wire CommandAck response handler - Hub processes node responses
	mtlsServer.OnMessage = func(nodeID string, msg []byte) {
		var ack mtls.CommandAck
		if err := json.Unmarshal(msg, &ack); err != nil {
			logger.Error("Failed to parse CommandAck", "nodeID", nodeID, "error", err)
			return
		}
		logger.Info("Received CommandAck", "nodeID", nodeID, "commandID", ack.CommandID, "status", ack.Status)
		// TODO: Update command status in database (Phase 4)
	}

	go func() {
		logger.Info("mTLS server listening", "port", cfg.MTLSPort)
		if err := mtlsServer.Start(); err != nil {
			logger.Error("mTLS server error", "error", err)
			os.Exit(1)
		}
	}()

	// Initialize command service
	commandService := services.NewCommandService(mtlsServer)
	_ = commandService // Will be used in Phase 4 for Hub-Node commands

	// Start background services
	// Event listener (if enabled)
	if eventListener != nil {
		go func() {
			logger.Info("Starting event listener")
			if err := eventListener.Start(ctx); err != nil && err != context.Canceled {
				logger.Error("Event listener error", "error", err)
			}
			logger.Info("Event listener stopped")
		}()
	}

	// Timeout enforcer (always runs)
	go func() {
		logger.Info("Starting timeout enforcer")
		if err := timeoutEnforcer.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("Timeout enforcer error", "error", err)
		}
		logger.Info("Timeout enforcer stopped")
	}()

	logger.Info("Hub fully initialized",
		"httpPort", cfg.ServerPort,
		"mtlsPort", cfg.MTLSPort,
		"blockchainListener", eventListener != nil,
	)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("Received shutdown signal", "signal", sig)
	logger.Info("Shutting down...")

	// Cancel context to stop background services
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	httpServer.Shutdown(shutdownCtx)
	mtlsServer.Stop()
	logger.Info("Shutdown complete")
}

// initCertService initializes the certificate service
// For development, generates a self-signed CA if certs don't exist
func initCertService(cfg *config.Config, nodeRepo domain.NodeRepository, logger *slog.Logger) (*services.CertService, error) {
	// Try to load CA certificate and key
	caCertPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		// For development, generate self-signed CA
		logger.Warn("CA cert not found, generating self-signed CA for development")
		return initDevCertService(nodeRepo, cfg.CertTTL, logger)
	}

	caKeyPEM, err := os.ReadFile(cfg.CAKeyPath)
	if err != nil {
		logger.Warn("CA key not found, generating self-signed CA for development")
		return initDevCertService(nodeRepo, cfg.CertTTL, logger)
	}

	return services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, cfg.CertTTL)
}

// initDevCertService generates a self-signed CA for development
func initDevCertService(nodeRepo domain.NodeRepository, ttl time.Duration, logger *slog.Logger) (*services.CertService, error) {
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

	logger.Info("Generated self-signed CA for development")
	return services.NewCertService(caCertPEM, caKeyPEM, nodeRepo, ttl)
}

// initMTLSServer initializes the mTLS server
// For development, generates a self-signed server cert if not found
func initMTLSServer(cfg *config.Config, certService *services.CertService, logger *slog.Logger) (*mtls.Server, error) {
	// Try to load server certificate for mTLS
	serverCert, err := tls.LoadX509KeyPair(cfg.MTLSCertPath, cfg.MTLSKeyPath)
	if err != nil {
		// For development, use cert service to generate
		logger.Warn("mTLS server cert not found, generating for development")
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
