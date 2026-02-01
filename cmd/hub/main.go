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
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	httpAdapter "github.com/worldland/worldland-hub/internal/adapters/http"
	"github.com/worldland/worldland-hub/internal/adapters/mtls"
	"github.com/worldland/worldland-hub/internal/adapters/postgres"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/config"
	"github.com/worldland/worldland-hub/internal/indexer"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/rental"
	"github.com/worldland/worldland-hub/internal/services"
	"github.com/worldland/worldland-hub/internal/sessions"
	"github.com/worldland/worldland-hub/internal/settlement"
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
	// Hub uses 'rental_events' checkpoint; standalone indexer uses 'indexer_events'
	checkpointStore := blockchain.NewCheckpointStore(dbPool, "rental_events")
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
	// rentalSessionRepo implements SoftDeleter via SoftDeletePendingBefore (Phase 14)
	timeoutEnforcer := sessions.NewTimeoutEnforcer(rentalSessionManager, rentalSessionRepo, rentalSessionRepo, logger)

	// Initialize settlement calculator and batch processor (04-07)
	settlementCalculator := settlement.NewCalculator(rentalSessionRepo)
	// TODO (DEBT-03): Implement ContractTransferer when contract SDK available
	// For now, pass nil - blockchain transfer is optional, DB is source of truth
	var contractTransferer settlement.ContractTransferer // nil
	if contractTransferer != nil {
		logger.Info("Blockchain settlement transfer enabled")
	} else {
		logger.Info("Blockchain settlement transfer disabled (no ContractTransferer)")
	}
	batchProcessor := settlement.NewBatchProcessor(
		settlementCalculator,
		rentalSessionRepo,
		contractTransferer,
		logger,
	)

	// Initialize Node client for Hub-to-Node communication (04-05)
	// Load mTLS certificates for secure Hub-to-Node communication (DEBT-01)
	nodeTLSConfig, err := loadNodeClientTLS(cfg, logger)
	if err != nil {
		logger.Error("Failed to load Node client TLS config", "error", err)
		os.Exit(1)
	}
	nodeClient := rental.NewNodeClient(rental.NodeClientConfig{
		TLSConfig: nodeTLSConfig,
		Timeout:   2 * time.Minute,
	})

	// Initialize balance validator and transaction verifier for on-chain checks (06-06, 14-03)
	var balanceValidator blockchain.BalanceValidatorInterface
	var transactionVerifier *blockchain.TransactionVerifier
	if cfg.Blockchain.ContractAddress != "" && cfg.Blockchain.HTTPRPCEndpoint != "" {
		ethClient, err := ethclient.Dial(cfg.Blockchain.HTTPRPCEndpoint)
		if err != nil {
			logger.Error("Failed to connect to Ethereum RPC", "error", err)
			// Non-fatal: balance validation and tx verification will be skipped
		} else {
			contractAddr := common.HexToAddress(cfg.Blockchain.ContractAddress)
			balanceValidator, err = blockchain.NewBalanceValidator(ethClient, contractAddr)
			if err != nil {
				logger.Error("Failed to create balance validator", "error", err)
				// Non-fatal: balance validation will be skipped
			} else {
				logger.Info("Balance validator initialized",
					"contract", cfg.Blockchain.ContractAddress,
					"rpc", cfg.Blockchain.HTTPRPCEndpoint,
				)
			}

			// Initialize transaction verifier for confirmation worker (14-03 ADR-001)
			transactionVerifier = blockchain.NewTransactionVerifier(ethClient, contractAddr)
			logger.Info("Transaction verifier initialized",
				"contract", cfg.Blockchain.ContractAddress,
			)
		}
	} else {
		logger.Warn("Balance validator and transaction verifier disabled (no contract address or HTTP RPC endpoint configured)")
	}

	// Initialize HTTP handlers
	authHandler := httpAdapter.NewAuthHandler(siweVerifier, sessionManager, nonceRepo, providerRepo)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)
	certHandler := httpAdapter.NewCertHandler(certService)
	rentalHandler := httpAdapter.NewRentalHandler(providerMatcher, rentalSessionManager, rentalSessionRepo, providerRepo, nodeRepo, nodeClient, balanceValidator)

	// Initialize confirmation handler (14-03 ADR-001)
	confirmationHandler := httpAdapter.NewConfirmationHandler(rentalSessionRepo, providerRepo)

	balanceHandler := httpAdapter.NewBalanceHandler(settlementCalculator, rentalSessionRepo, providerRepo)

	// Initialize query repository and history handler (09-04)
	queryRepo := indexer.NewQueryRepository(dbPool)
	historyHandler := httpAdapter.NewHistoryHandler(queryRepo)

	// Create router
	router := httpAdapter.NewRouter(authHandler, nodeHandler, certHandler, rentalHandler, confirmationHandler, balanceHandler, historyHandler, sessionManager)

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

	// Batch settlement processor (always runs - 04-07)
	go func() {
		logger.Info("Starting batch settlement processor")
		batchProcessor.Start(ctx)
		logger.Info("Batch settlement processor stopped")
	}()

	// Confirmation worker for processing pending txHash verifications (14-03 ADR-001)
	if transactionVerifier != nil {
		confirmationWorker := sessions.NewConfirmationWorker(
			rentalSessionRepo,
			transactionVerifier,
			rentalSessionManager,
			nodeClient,
			nodeRepo,
			logger,
		)
		go func() {
			logger.Info("Starting confirmation worker")
			if err := confirmationWorker.Start(ctx); err != nil && err != context.Canceled {
				logger.Error("Confirmation worker error", "error", err)
			}
			logger.Info("Confirmation worker stopped")
		}()
	} else {
		logger.Warn("Confirmation worker disabled (no transaction verifier)")
	}

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

	// Graceful shutdown ordering:
	// 1. Stop HTTP server (no new requests)
	httpServer.Shutdown(shutdownCtx)

	// 2. Stop batch processor (finish pending settlements)
	logger.Info("stopping batch processor...")
	batchProcessor.Stop()

	// 3. Stop mTLS server
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

// loadNodeClientTLS loads mTLS configuration for Hub-to-Node communication (DEBT-01)
// Returns nil TLSConfig in development mode (when cert files don't exist)
func loadNodeClientTLS(cfg *config.Config, logger *slog.Logger) (*tls.Config, error) {
	// Check if certificate files exist
	certExists := fileExists(cfg.NodeClientCertPath)
	keyExists := fileExists(cfg.NodeClientKeyPath)
	caExists := fileExists(cfg.CACertPath)

	// Development mode fallback: if certs don't exist, use nil TLSConfig
	if !certExists || !keyExists || !caExists {
		logger.Warn("Node client mTLS certs not found, using insecure connection for development",
			"certPath", cfg.NodeClientCertPath,
			"keyPath", cfg.NodeClientKeyPath,
			"caPath", cfg.CACertPath,
			"certExists", certExists,
			"keyExists", keyExists,
			"caExists", caExists,
		)
		return nil, nil
	}

	// Load client certificate
	cert, err := tls.LoadX509KeyPair(cfg.NodeClientCertPath, cfg.NodeClientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}

	// Load CA certificate pool
	caCertPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS13, // Per Phase 2 decision
	}

	logger.Info("Node client mTLS configured",
		"certPath", cfg.NodeClientCertPath,
		"caPath", cfg.CACertPath,
	)

	return tlsConfig, nil
}

// fileExists checks if a file exists and is not a directory
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
