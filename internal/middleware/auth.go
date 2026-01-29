package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/auth"
)

// AuthMiddleware validates session tokens from Authorization header
// Security notes:
// - Only accepts Authorization header (not query params)
// - Bearer token format only
// - Session validation checks expiry
// - Provider ID stored in context for downstream handlers
func AuthMiddleware(sessionManager *auth.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		c.Next()
	}
}
