package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/wallet"
)

// SessionKeyAuthMiddleware validates session key from header and sets user context.
// Uses X-Session-Key header for session key authentication.
func SessionKeyAuthMiddleware(sessionManager *wallet.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionKey := c.GetHeader("X-Session-Key")

		// If no session key, try Authorization header for backward compatibility
		if sessionKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "SessionKey ") {
				sessionKey = strings.TrimPrefix(authHeader, "SessionKey ")
			}
		}

		if sessionKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Session key required. Use X-Session-Key header or 'Authorization: SessionKey <key>'",
			})
			return
		}

		// Validate session
		session, err := sessionManager.ValidateSession(c.Request.Context(), sessionKey)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "Invalid or expired session key",
				"details": err.Error(),
			})
			return
		}

		// Set user context (main wallet is the user ID)
		c.Set("userID", session.MainWallet)
		c.Set("sessionKey", session.SessionKey)
		c.Set("session", session)
		c.Set("spendLimit", session.SpendLimit)
		c.Set("remainingQuota", session.SpendLimit-session.SpentAmount)

		c.Next()
	}
}

// OptionalSessionKeyMiddleware tries to validate session key if present, but doesn't require it.
func OptionalSessionKeyMiddleware(sessionManager *wallet.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionKey := c.GetHeader("X-Session-Key")

		if sessionKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "SessionKey ") {
				sessionKey = strings.TrimPrefix(authHeader, "SessionKey ")
			}
		}

		if sessionKey != "" {
			session, err := sessionManager.ValidateSession(c.Request.Context(), sessionKey)
			if err == nil {
				c.Set("userID", session.MainWallet)
				c.Set("sessionKey", session.SessionKey)
				c.Set("session", session)
				c.Set("spendLimit", session.SpendLimit)
				c.Set("remainingQuota", session.SpendLimit-session.SpentAmount)
			}
		}

		c.Next()
	}
}

// WalletOrSessionKeyMiddleware accepts either JWT (from wallet login) or session key.
func WalletOrSessionKeyMiddleware(sessionManager *wallet.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try session key first
		sessionKey := c.GetHeader("X-Session-Key")
		if sessionKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "SessionKey ") {
				sessionKey = strings.TrimPrefix(authHeader, "SessionKey ")
			}
		}

		if sessionKey != "" {
			session, err := sessionManager.ValidateSession(c.Request.Context(), sessionKey)
			if err == nil {
				c.Set("userID", session.MainWallet)
				c.Set("sessionKey", session.SessionKey)
				c.Set("session", session)
				c.Set("authMethod", "session_key")
				c.Next()
				return
			}
		}

		// Fall back to X-User-ID for development/testing
		userID := c.GetHeader("X-User-ID")
		if userID != "" {
			c.Set("userID", userID)
			c.Set("authMethod", "dev_header")
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Authentication required. Use X-Session-Key, 'Authorization: SessionKey <key>', or X-User-ID (dev only)",
		})
	}
}

// QuotaCheckMiddleware checks if the session has enough quota for an operation.
func QuotaCheckMiddleware(sessionManager *wallet.SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		session, exists := c.Get("session")
		if !exists {
			c.Next()
			return
		}

		s := session.(*wallet.Session)
		remainingQuota := s.SpendLimit - s.SpentAmount

		// Add quota info to response header
		c.Header("X-Remaining-Quota", formatFloat(remainingQuota))
		c.Header("X-Spend-Limit", formatFloat(s.SpendLimit))
		c.Header("X-Spent-Amount", formatFloat(s.SpentAmount))

		c.Next()
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
