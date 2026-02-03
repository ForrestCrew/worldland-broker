package middleware

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig holds CORS configuration
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
	MaxAge           int // in seconds
}

// DefaultCORSConfig returns sensible defaults for CORS
func DefaultCORSConfig() CORSConfig {
	// Get allowed origins from environment
	// Format: comma-separated list "http://localhost:3000,https://worldland.io"
	originsEnv := os.Getenv("CORS_ALLOWED_ORIGINS")
	var origins []string
	if originsEnv != "" {
		origins = strings.Split(originsEnv, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
	} else {
		// Default for development
		origins = []string{
			"http://localhost:3000",
			"http://localhost:3001",
			"http://127.0.0.1:3000",
		}
	}

	return CORSConfig{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           86400, // 24 hours
	}
}

// CORSMiddleware creates a CORS middleware with the given config
func CORSMiddleware(config CORSConfig) gin.HandlerFunc {
	allowedOriginsMap := make(map[string]bool)
	for _, origin := range config.AllowedOrigins {
		allowedOriginsMap[origin] = true
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Check if origin is allowed
		if allowedOriginsMap[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		// Always set these headers
		c.Header("Access-Control-Allow-Methods", strings.Join(config.AllowedMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(config.AllowedHeaders, ", "))
		c.Header("Access-Control-Max-Age", fmt.Sprintf("%d", config.MaxAge))

		if config.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		// Handle preflight OPTIONS requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
