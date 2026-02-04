package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/domain"
)

// AuthMiddleware validates session tokens from Authorization header
// Security notes:
// - Only accepts Authorization header (not query params)
// - Bearer token format only
// - Session validation checks expiry
// - Provider ID stored in context for downstream handlers
func AuthMiddleware(sessionManager *auth.SessionManager) gin.HandlerFunc {
	return AuthMiddlewareWithConfig(sessionManager, nil, false)
}

// AuthMiddlewareWithConfig creates auth middleware with configurable auth bypass
// When disabled is true, still validates session but sets auth_disabled flag for handler logic
// providerRepo is used to look up wallet addresses from provider IDs
func AuthMiddlewareWithConfig(sessionManager *auth.SessionManager, providerRepo domain.ProviderRepository, disabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// In auth_disabled mode, try to validate session first
		// This allows E2E tests to use real SIWE auth while still marking auth_disabled
		if disabled {
			c.Set("auth_disabled", true)

			// Try to get real session if Authorization header exists
			authHeader := c.GetHeader("Authorization")
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) == 2 && parts[0] == "Bearer" {
					token := parts[1]
					session, err := sessionManager.Validate(c.Request.Context(), token)
					if err == nil {
						// Valid session - look up provider to get wallet address
						c.Set("provider_id", session.ProviderID)
						c.Set("session_token", token)

						// Look up wallet address from provider
						if providerRepo != nil {
							provider, err := providerRepo.GetByID(c.Request.Context(), session.ProviderID)
							if err == nil {
								c.Set("user_address", provider.WalletAddress)
								c.Set("wallet_address", provider.WalletAddress)
							}
						}
						c.Next()
						return
					}
				}
			}

			// No valid session - fall back to default test address
			testUserAddress := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
			testProviderID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(testUserAddress)).String()
			c.Set("provider_id", testProviderID)
			c.Set("user_address", testUserAddress)
			c.Set("wallet_address", testUserAddress)
			c.Set("session_token", "e2e-test-token")
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing authorization header"})
			c.Abort()
			return
		}

		// Expect "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization format"})
			c.Abort()
			return
		}

		token := parts[1]

		// Validate session
		session, err := sessionManager.Validate(c.Request.Context(), token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired session"})
			c.Abort()
			return
		}

		// Store session info in context for handlers
		c.Set("provider_id", session.ProviderID)
		c.Set("session_token", token)

		// Look up wallet address from provider
		if providerRepo != nil {
			provider, err := providerRepo.GetByID(c.Request.Context(), session.ProviderID)
			if err == nil {
				c.Set("user_address", provider.WalletAddress)
				c.Set("wallet_address", provider.WalletAddress)
			}
		}

		c.Next()
	}
}
