package http

import (
	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/adapters/http/middleware"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/domain"
	intMiddleware "github.com/worldland/worldland-hub/internal/middleware"
)

// RouterConfig holds optional router configuration
type RouterConfig struct {
	AuthDisabled bool                      // Skip authentication (for E2E testing only)
	ProviderRepo domain.ProviderRepository // For wallet address lookup in auth_disabled mode
}

// NewRouter creates a new Gin router with all routes configured
func NewRouter(
	authHandler *AuthHandler,
	nodeHandler *NodeHandler,
	certHandler *CertHandler,
	rentalHandler *RentalHandler,
	confirmationHandler *ConfirmationHandler,
	balanceHandler *BalanceHandler,
	historyHandler *HistoryHandler,
	monitoringHandler *MonitoringHandler,
	sessionManager *auth.SessionManager,
) *gin.Engine {
	return NewRouterWithConfig(authHandler, nodeHandler, certHandler, rentalHandler, confirmationHandler, balanceHandler, historyHandler, monitoringHandler, sessionManager, RouterConfig{}, nil, nil)
}

// NewRouterWithConfig creates a new Gin router with configuration
func NewRouterWithConfig(
	authHandler *AuthHandler,
	nodeHandler *NodeHandler,
	certHandler *CertHandler,
	rentalHandler *RentalHandler,
	confirmationHandler *ConfirmationHandler,
	balanceHandler *BalanceHandler,
	historyHandler *HistoryHandler,
	monitoringHandler *MonitoringHandler,
	sessionManager *auth.SessionManager,
	cfg RouterConfig,
	providerHandler *ProviderHandler,
	miningHandler *MiningHandler,
) *gin.Engine {
	router := gin.Default()

	// CORS middleware - must be first
	corsConfig := middleware.DefaultCORSConfig()
	router.Use(middleware.CORSMiddleware(corsConfig))

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API v1
	v1 := router.Group("/api/v1")
	{
		// Public auth endpoints
		authGroup := v1.Group("/auth")
		{
			authGroup.GET("/nonce", authHandler.GetNonce)
			authGroup.POST("/login", authHandler.Login)
		}

		// Public CA endpoint (nodes need CA cert to verify Hub)
		v1.GET("/ca/root", certHandler.GetRootCA)

		// Public images endpoint (24-03 - no auth required for preset list)
		if rentalHandler != nil {
			v1.GET("/images", rentalHandler.ListImages)
		}

		// Protected endpoints (require valid session)
		protected := v1.Group("")
		protected.Use(intMiddleware.AuthMiddlewareWithConfig(sessionManager, cfg.ProviderRepo, cfg.AuthDisabled))
		{
			protected.POST("/auth/logout", authHandler.Logout)

			// Node endpoints (PROV-02, PROV-03)
			protected.POST("/nodes", nodeHandler.RegisterNode)
			protected.GET("/nodes", nodeHandler.ListNodes)
			protected.GET("/nodes/:id", nodeHandler.GetNode)
			protected.PATCH("/nodes/:id/price", nodeHandler.UpdateNodePrice)

			// Certificate endpoints (PROV-02 mTLS)
			protected.POST("/nodes/:id/certificate", certHandler.IssueCertificate)
			protected.POST("/certs/bootstrap", certHandler.IssueBootstrapCertificate)

			// Rental endpoints (03-07, 04-05, 14-03)
			if rentalHandler != nil {
				rentals := protected.Group("/rentals")
				{
					rentals.POST("/providers", rentalHandler.FindProviders) // Search providers
					rentals.POST("", rentalHandler.CreateSession)           // Create session
					rentals.GET("", rentalHandler.ListSessions)             // List user sessions
					rentals.DELETE("/:id", rentalHandler.CancelSession)     // Cancel session
					rentals.POST("/:id/start", rentalHandler.HandleStartRental) // Start rental (04-05)
					rentals.POST("/:id/stop", rentalHandler.HandleStopRental)   // Stop rental (04-05)
					rentals.POST("/:id/extend", rentalHandler.HandleExtendSession) // Extend session (16-03)

					// Confirmation endpoints (14-03 ADR-001)
					if confirmationHandler != nil {
						rentals.POST("/:id/confirm", confirmationHandler.ConfirmRental) // Confirm with txHash
						rentals.GET("/:id", confirmationHandler.GetSession)             // Get session status
					}
				}
			}

			// Balance endpoint (04-06)
			if balanceHandler != nil {
				protected.GET("/balance", balanceHandler.GetBalance)
			}

			// Provider endpoints (Phase 3)
			if providerHandler != nil {
				providers := protected.Group("/providers")
				{
					providers.GET("/me", providerHandler.GetMyProvider)
					providers.POST("/k8s", providerHandler.RegisterK8sProvider)
				}
			}

			// Mining endpoints (Phase 3)
			if miningHandler != nil {
				protected.POST("/providers/:id/mining/start", miningHandler.StartMining)
				protected.POST("/providers/:id/mining/stop", miningHandler.StopMining)
				protected.POST("/providers/:id/mining/allocate", miningHandler.AllocateMiningGPU)
				protected.POST("/providers/:id/mining/release", miningHandler.ReleaseMiningGPU)
				protected.GET("/providers/:id/mining", miningHandler.GetMiningStatus)
			}

			// Monitoring endpoints (Phase 23)
			if monitoringHandler != nil {
				monitoring := protected.Group("/monitoring")
				{
					monitoring.GET("/provider/stats", monitoringHandler.GetProviderStats)
					monitoring.GET("/tenant/:address", monitoringHandler.GetTenantUsage)
					monitoring.GET("/sessions", monitoringHandler.GetAllSessions)
					monitoring.GET("/sessions/:id", monitoringHandler.GetSessionMetrics)
				}
			}
		}
	}

	// History endpoints (public - blockchain data is public)
	// No authentication required - users query their own addresses
	if historyHandler != nil {
		history := router.Group("/api/history")
		{
			history.GET("/deposits-withdraws/:address", historyHandler.GetDepositWithdrawHistory)
			history.GET("/rentals/:address", historyHandler.GetRentalHistory)
		}
	}

	return router
}
