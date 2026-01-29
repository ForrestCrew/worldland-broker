package http

import (
	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/middleware"
)

// NewRouter creates a new Gin router with all routes configured
func NewRouter(
	authHandler *AuthHandler,
	nodeHandler *NodeHandler,
	certHandler *CertHandler,
	sessionManager *auth.SessionManager,
) *gin.Engine {
	router := gin.Default()

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
		protected.Use(middleware.AuthMiddleware(sessionManager))
		{
			protected.POST("/auth/logout", authHandler.Logout)

			// Node endpoints (PROV-02, PROV-03)
			protected.POST("/nodes", nodeHandler.RegisterNode)
			protected.GET("/nodes", nodeHandler.ListNodes)
			protected.GET("/nodes/:id", nodeHandler.GetNode)
			protected.PATCH("/nodes/:id/price", nodeHandler.UpdateNodePrice)

			// Certificate endpoint (PROV-02 mTLS)
			protected.POST("/nodes/:id/certificate", certHandler.IssueCertificate)
		}
	}

	return router
}
