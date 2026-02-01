package http

import (
	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/adapters/http/middleware"
	"github.com/worldland/worldland-hub/internal/auth"
	intMiddleware "github.com/worldland/worldland-hub/internal/middleware"
)

// NewRouter creates a new Gin router with all routes configured
func NewRouter(
	authHandler *AuthHandler,
	nodeHandler *NodeHandler,
	certHandler *CertHandler,
	rentalHandler *RentalHandler,
	confirmationHandler *ConfirmationHandler,
	balanceHandler *BalanceHandler,
	historyHandler *HistoryHandler,
	sessionManager *auth.SessionManager,
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

		// Protected endpoints (require valid session)
		protected := v1.Group("")
		protected.Use(intMiddleware.AuthMiddleware(sessionManager))
		{
			protected.POST("/auth/logout", authHandler.Logout)

			// Node endpoints (PROV-02, PROV-03)
			protected.POST("/nodes", nodeHandler.RegisterNode)
			protected.GET("/nodes", nodeHandler.ListNodes)
			protected.GET("/nodes/:id", nodeHandler.GetNode)
			protected.PATCH("/nodes/:id/price", nodeHandler.UpdateNodePrice)

			// Certificate endpoint (PROV-02 mTLS)
			protected.POST("/nodes/:id/certificate", certHandler.IssueCertificate)

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
