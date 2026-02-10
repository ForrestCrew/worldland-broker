package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/indexer"
	"github.com/worldland/worldland-hub/internal/k8s"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/mining"
	"github.com/worldland/worldland-hub/internal/monitoring"
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

	logger.Info("Worldland Hub V4 starting (K8s-only architecture)...")

	// Load configuration
	cfg := config.LoadConfig()

	// Create cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize legacy K8s client (for single-cluster mode)
	var jobManager *k8s.JobManager
	var tenantOrch *k8s.TenantOrchestrator
	var podWatcher *k8s.PodWatcher
	var metricsCollector *k8s.MetricsCollector

	if cfg.K8s.Enabled {
		k8sManager := k8s.GetManager()

		var initErr error
		if cfg.K8s.KubeconfigPath != "" {
			initErr = k8sManager.InitFromKubeconfig(cfg.K8s.KubeconfigPath, logger)
		} else {
			initErr = k8sManager.InitInCluster(logger)
		}

		if initErr != nil {
			logger.Error("Failed to initialize K8s client", "error", initErr)
		} else {
			clientset, _ := k8sManager.GetClientset()

			jobManager = k8s.NewJobManager(clientset, logger).WithExternalHost(cfg.K8s.ExternalHost)
			tenantOrch = k8s.NewTenantOrchestrator(clientset, logger)

			restConfig := k8sManager.GetConfig()
			mc, err := k8s.NewMetricsCollector(restConfig, logger)
			if err != nil {
				logger.Warn("Failed to create metrics collector", "error", err)
			} else {
				metricsCollector = mc
				logger.Info("Metrics collector initialized")
			}

			logger.Info("K8s integration initialized",
				"kubeconfig", cfg.K8s.KubeconfigPath,
			)
		}
	} else {
		logger.Info("K8s integration disabled")
	}

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
	siweVerifier := auth.NewSIWEVerifier(cfg.SIWEDomains, nonceRepo)
	sessionManager := auth.NewSessionManager(sessionRepo, cfg.SessionTTL)

	// Initialize services
	nodeService := services.NewNodeServiceWithProvider(nodeRepo, providerRepo)

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

	// Initialize PodWatcher early so MonitoringService can use it
	if jobManager != nil {
		k8sStateHandler := sessions.NewK8sStateHandler(
			rentalSessionManager,
			rentalSessionRepo,
			logger,
		)
		clientset, _ := k8s.GetManager().GetClientset()
		podWatcher = k8s.NewPodWatcher(clientset, k8sStateHandler, logger)
	}

	// Initialize blockchain components
	checkpointStore := blockchain.NewCheckpointStore(dbPool, "rental_events")
	eventProcessor := blockchain.NewEventProcessor(rentalSessionManager, rentalSessionRepo, logger)

	// Initialize event listener
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

	// Initialize timeout enforcer
	timeoutEnforcer := sessions.NewTimeoutEnforcer(rentalSessionManager, rentalSessionRepo, rentalSessionRepo, logger)

	// Initialize settlement calculator and batch processor
	settlementCalculator := settlement.NewCalculator(rentalSessionRepo)
	var contractTransferer settlement.ContractTransferer
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

	// Initialize balance validator and transaction verifier
	var balanceValidator blockchain.BalanceValidatorInterface
	var transactionVerifier *blockchain.TransactionVerifier
	if cfg.Blockchain.ContractAddress != "" && cfg.Blockchain.HTTPRPCEndpoint != "" {
		ethClient, err := ethclient.Dial(cfg.Blockchain.HTTPRPCEndpoint)
		if err != nil {
			logger.Error("Failed to connect to Ethereum RPC", "error", err)
		} else {
			contractAddr := common.HexToAddress(cfg.Blockchain.ContractAddress)
			balanceValidator, err = blockchain.NewBalanceValidator(ethClient, contractAddr)
			if err != nil {
				logger.Error("Failed to create balance validator", "error", err)
			} else {
				logger.Info("Balance validator initialized",
					"contract", cfg.Blockchain.ContractAddress,
					"rpc", cfg.Blockchain.HTTPRPCEndpoint,
				)
			}

			transactionVerifier = blockchain.NewTransactionVerifier(ethClient, contractAddr)
			logger.Info("Transaction verifier initialized",
				"contract", cfg.Blockchain.ContractAddress,
			)
		}
	} else {
		logger.Warn("Balance validator and transaction verifier disabled")
	}

	// Initialize image repository
	imageRepo := postgres.NewImageRepository(dbPool)

	// Initialize HTTP handlers (V4: no nodeClient or remote dependencies)
	authHandler := httpAdapter.NewAuthHandler(siweVerifier, sessionManager, nonceRepo, providerRepo)
	nodeHandler := httpAdapter.NewNodeHandler(nodeService)
	certHandler := httpAdapter.NewCertHandler(certService)
	rentalHandler := httpAdapter.NewRentalHandler(providerMatcher, rentalSessionManager, rentalSessionRepo, providerRepo, nodeRepo, balanceValidator)

	// Wire image repository
	rentalHandler = rentalHandler.WithImageRepository(imageRepo)
	rentalSessionManager = rentalSessionManager.WithImageRepository(imageRepo)

	// Initialize confirmation handler
	confirmationHandler := httpAdapter.NewConfirmationHandler(rentalSessionRepo, providerRepo)

	balanceHandler := httpAdapter.NewBalanceHandler(settlementCalculator, rentalSessionRepo, providerRepo)

	// Initialize query repository and history handler
	queryRepo := indexer.NewQueryRepository(dbPool)
	historyHandler := httpAdapter.NewHistoryHandler(queryRepo)

	// Initialize MonitoringService and handler
	var monitoringHandler *httpAdapter.MonitoringHandler
	if tenantOrch != nil {
		monitoringService := monitoring.NewMonitoringService(
			tenantOrch,
			metricsCollector,
			podWatcher,
			logger,
		)
		monitoringHandler = httpAdapter.NewMonitoringHandler(monitoringService, logger)
		logger.Info("Monitoring service initialized")
	}

	// Initialize mTLS server for node connections
	mtlsServer, err := initMTLSServer(cfg, certService, logger)
	if err != nil {
		logger.Error("Failed to initialize mTLS server", "error", err)
		os.Exit(1)
	}

	// V4: ExternalClusterRegistry is the single K8s executor path
	clusterRegistry := k8s.NewExternalClusterRegistry(logger)
	var k8sExecutor *k8s.K8sJobExecutor
	var providerHandler *httpAdapter.ProviderHandler
	var miningHandler *httpAdapter.MiningHandler

	if cfg.ExternalProvidersEnabled {
		// Load K8s providers from DB and register their clusters
		k8sProviders, err := providerRepo.ListByType(ctx, domain.ProviderTypeK8s)
		if err != nil {
			logger.Warn("Failed to load K8s providers", "error", err)
		} else {
			for _, p := range k8sProviders {
				if p.KubeconfigData != nil && *p.KubeconfigData != "" {
					if err := clusterRegistry.RegisterCluster(p.ID, []byte(*p.KubeconfigData)); err != nil {
						logger.Warn("Failed to register K8s cluster for provider",
							"providerID", p.ID,
							"error", err,
						)
					}
				}
			}
			logger.Info("Loaded external K8s providers", "count", len(k8sProviders))
		}

		// V4: Single K8s executor (no Docker executor)
		capacityTracker := k8s.NewCapacityTracker(clusterRegistry, nodeRepo, logger)
		k8sExecutor = k8s.NewK8sJobExecutor(clusterRegistry, logger, cfg.K8s.DefaultImage).
			WithNodeRepo(nodeRepo).
			WithCapacityTracker(capacityTracker)

		// Discover initial capacity for all registered clusters
		for _, pid := range clusterRegistry.ListProviderIDs() {
			if err := capacityTracker.DiscoverCapacity(ctx, pid); err != nil {
				logger.Warn("Failed to discover initial capacity", "providerID", pid, "error", err)
			}
		}
		// Start periodic capacity sync (every 2 minutes)
		capacityTracker.StartPeriodicSync(ctx, 2*time.Minute)

		// Start periodic K8s node sync (auto-registers new worker nodes in DB)
		nodeSyncWorker := k8s.NewNodeSyncWorker(clusterRegistry, nodeRepo, logger, 2*time.Minute)
		go func() {
			logger.Info("Starting K8s node sync worker")
			if err := nodeSyncWorker.Start(ctx); err != nil && err != context.Canceled {
				logger.Error("K8s node sync worker error", "error", err)
			}
		}()

		// Wire K8s executor as cleanup handler for event processor
		eventProcessor.WithCleanup(k8sExecutor)

		// Wire K8s executor to handlers
		rentalHandler = rentalHandler.WithExecutor(k8sExecutor)
		confirmationHandler = confirmationHandler.WithExecutor(k8sExecutor)

		// Create provider handler
		providerHandler = httpAdapter.NewProviderHandler(providerRepo, clusterRegistry, logger).WithNodeRepo(nodeRepo)

		// Create mining handler
		gpuPool := mining.NewGPUPool()
		miningManager := mining.NewK8sMiningManager(clusterRegistry, providerRepo, logger).WithGPUPool(gpuPool)
		miningManager.DiscoverGPUs(ctx)
		miningHandler = httpAdapter.NewMiningHandler(miningManager, gpuPool, logger)

		// Recover resource allocations from running Pods (proxy pattern: RecoverJobAllocations on startup)
		if err := capacityTracker.RecoverAllocations(ctx); err != nil {
			logger.Warn("Failed to recover resource allocations", "error", err)
		}

		// Start Pod expiration monitor (safety net for missed session cleanup)
		expirationMonitor := k8s.NewExpirationMonitor(clusterRegistry, capacityTracker, logger)
		go func() {
			logger.Info("Starting Pod expiration monitor")
			if err := expirationMonitor.Start(ctx, 1*time.Minute); err != nil && err != context.Canceled {
				logger.Error("Pod expiration monitor error", "error", err)
			}
			logger.Info("Pod expiration monitor stopped")
		}()

		// Multi-cluster PodWatcher: monitors GPU rental Pods across all registered clusters
		k8sStateHandler := sessions.NewK8sStateHandler(
			rentalSessionManager,
			rentalSessionRepo,
			logger,
		)
		// Set executor so OnPodFailed can clean up K8s resources and release capacity
		if k8sExecutor != nil {
			k8sStateHandler.SetExecutor(k8sExecutor)
		}
		multiWatcher := k8s.NewMultiClusterWatcher(clusterRegistry, k8sStateHandler, logger)
		multiWatcher.StartAll(ctx)
		logger.Info("Multi-cluster pod watchers started",
			"count", multiWatcher.WatcherCount(),
		)

		logger.Info("V4: K8s-only external providers enabled",
			"k8sClusters", len(clusterRegistry.ListProviderIDs()),
		)
	} else {
		logger.Info("V4: External providers disabled")
	}

	// Create router
	routerCfg := httpAdapter.RouterConfig{
		AuthDisabled: cfg.AuthDisabled,
		ProviderRepo: providerRepo,
	}
	if cfg.AuthDisabled {
		logger.Warn("AUTH_DISABLED is true - authentication is bypassed (for E2E testing only)")
	}
	router := httpAdapter.NewRouterWithConfig(authHandler, nodeHandler, certHandler, rentalHandler, confirmationHandler, balanceHandler, historyHandler, monitoringHandler, sessionManager, routerCfg, providerHandler, miningHandler)

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

	// V4: mTLS OnMessage processes SDK heartbeats for node status updates
	mtlsServer.OnMessage = func(nodeID string, msg []byte) {
		var envelope struct {
			Type    string                   `json:"type"`
			Payload services.HeartbeatPayload `json:"payload"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			logger.Debug("failed to parse mTLS message", "nodeID", nodeID, "error", err)
			return
		}

		if envelope.Type == "heartbeat" {
			nodeService.ProcessHeartbeat(ctx, nodeID, envelope.Payload)
		}
	}

	// V4: mTLS OnNodeConnected registers the SDK master node
	mtlsServer.OnNodeConnected = func(nodeID string) {
		logger.Info("SDK node connected via mTLS", "nodeID", nodeID)
		input := services.AutoRegisterNodeInput{
			NodeID:      nodeID,
			GPUType:     "K8s Cluster",
			MemoryGB:    0,
			PricePerSec: "1000000000",
			APIEndpoint: "", // V4: No API endpoint — K8s managed
		}
		if _, err := nodeService.AutoRegisterNode(ctx, input); err != nil {
			logger.Error("Failed to auto-register SDK node", "nodeID", nodeID, "error", err)
		} else {
			logger.Info("SDK node auto-registered", "nodeID", nodeID)
		}
	}

	// Wire node disconnect handler
	mtlsServer.OnNodeDisconnected = func(nodeID string) {
		logger.Info("Node disconnected", "nodeID", nodeID)
		if err := nodeService.MarkNodeOffline(ctx, nodeID); err != nil {
			logger.Error("Failed to mark node offline", "nodeID", nodeID, "error", err)
		}
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
	_ = commandService

	// Start background services
	if eventListener != nil {
		go func() {
			logger.Info("Starting event listener")
			if err := eventListener.Start(ctx); err != nil && err != context.Canceled {
				logger.Error("Event listener error", "error", err)
			}
			logger.Info("Event listener stopped")
		}()
	}

	go func() {
		logger.Info("Starting timeout enforcer")
		if err := timeoutEnforcer.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("Timeout enforcer error", "error", err)
		}
		logger.Info("Timeout enforcer stopped")
	}()

	go func() {
		logger.Info("Starting batch settlement processor")
		batchProcessor.Start(ctx)
		logger.Info("Batch settlement processor stopped")
	}()

	// V4: Confirmation worker uses single K8s executor
	if transactionVerifier != nil {
		var executor domain.JobExecutor
		if k8sExecutor != nil {
			executor = k8sExecutor
		}

		confirmationWorker := sessions.NewConfirmationWorker(
			rentalSessionRepo,
			transactionVerifier,
			rentalSessionManager,
			nodeRepo,
			executor,
			logger,
		).WithDefaultImage(cfg.K8s.DefaultImage)

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

	// V4: Expiration worker uses single K8s executor
	var expirationExecutor domain.JobExecutor
	if k8sExecutor != nil {
		expirationExecutor = k8sExecutor
	}
	expirationWorker := sessions.NewExpirationWorker(
		rentalSessionRepo,
		rentalSessionManager,
		nodeRepo,
		expirationExecutor,
		logger,
	)
	go func() {
		logger.Info("Starting expiration worker")
		if err := expirationWorker.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("Expiration worker error", "error", err)
		}
		logger.Info("Expiration worker stopped")
	}()

	// Start K8s PodWatcher if enabled
	if podWatcher != nil {
		go func() {
			logger.Info("Starting K8s pod watcher")
			if err := podWatcher.Start(ctx); err != nil && err != context.Canceled {
				logger.Error("Pod watcher error", "error", err)
			}
			logger.Info("Pod watcher stopped")
		}()
	}

	logger.Info("Hub V4 fully initialized",
		"httpPort", cfg.ServerPort,
		"mtlsPort", cfg.MTLSPort,
		"blockchainListener", eventListener != nil,
		"k8sIntegration", jobManager != nil,
		"externalProviders", cfg.ExternalProvidersEnabled,
	)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("Received shutdown signal", "signal", sig)
	logger.Info("Shutting down...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	httpServer.Shutdown(shutdownCtx)

	logger.Info("stopping batch processor...")
	batchProcessor.Stop()

	mtlsServer.Stop()

	logger.Info("Shutdown complete")
}

// initCertService initializes the certificate service
func initCertService(cfg *config.Config, nodeRepo domain.NodeRepository, logger *slog.Logger) (*services.CertService, error) {
	caCertPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
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
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

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
func initMTLSServer(cfg *config.Config, certService *services.CertService, logger *slog.Logger) (*mtls.Server, error) {
	serverCert, err := tls.LoadX509KeyPair(cfg.MTLSCertPath, cfg.MTLSKeyPath)
	if err != nil {
		logger.Warn("mTLS server cert not found, generating for development")
		serverCert, err = generateDevServerCert()
		if err != nil {
			return nil, err
		}
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(certService.GetRootCA())

	return mtls.NewServer(serverCert, caCertPool, ":"+cfg.MTLSPort), nil
}

// generateDevServerCert generates a self-signed server certificate for development
func generateDevServerCert() (tls.Certificate, error) {
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

// fileExists checks if a file exists and is not a directory
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
