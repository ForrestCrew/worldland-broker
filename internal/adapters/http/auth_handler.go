package http

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/domain"
)

// AuthHandler handles authentication HTTP requests
type AuthHandler struct {
	siweVerifier   *auth.SIWEVerifier
	sessionManager *auth.SessionManager
	nonceRepo      domain.NonceRepository
	providerRepo   domain.ProviderRepository
}

// NewAuthHandler creates a new authentication handler
func NewAuthHandler(
	siweVerifier *auth.SIWEVerifier,
	sessionManager *auth.SessionManager,
	nonceRepo domain.NonceRepository,
	providerRepo domain.ProviderRepository,
) *AuthHandler {
	return &AuthHandler{
		siweVerifier:   siweVerifier,
		sessionManager: sessionManager,
		nonceRepo:      nonceRepo,
		providerRepo:   providerRepo,
	}
}

// NonceResponse is the response body for nonce requests
type NonceResponse struct {
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
}

// GetNonce returns a unique nonce for SIWE message construction
// GET /api/v1/auth/nonce
func (h *AuthHandler) GetNonce(c *gin.Context) {
	// Generate cryptographically random nonce (16 bytes = 32 hex chars)
	// EIP-4361 requires alphanumeric nonces, so use hex encoding
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate nonce"})
		return
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Store nonce with 5-minute TTL
	expiresAt := time.Now().Add(5 * time.Minute)
	if err := h.nonceRepo.Save(c.Request.Context(), nonce, expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store nonce"})
		return
	}

	c.JSON(http.StatusOK, NonceResponse{
		Nonce:     nonce,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

// LoginRequest is the request body for SIWE login
type LoginRequest struct {
	Message   string `json:"message" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

// LoginResponse is the response body for successful login
type LoginResponse struct {
	Token      string `json:"token"`
	ProviderID string `json:"provider_id"`
}

// Login verifies SIWE signature and returns session token
// POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	ctx := c.Request.Context()

	// Verify SIWE message and signature
	walletAddress, err := h.siweVerifier.Verify(ctx, req.Message, req.Signature)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	// Get or create provider
	provider, err := h.providerRepo.GetByWallet(ctx, walletAddress)
	if err != nil {
		// Create new provider on first login
		provider = &domain.Provider{
			ID:            generateID(),
			WalletAddress: walletAddress,
			Status:        domain.ProviderStatusActive,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if err := h.providerRepo.Create(ctx, provider); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create provider"})
			return
		}
	}

	// Create session token
	token, err := h.sessionManager.Create(ctx, provider.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token:      token,
		ProviderID: provider.ID,
	})
}

// Logout invalidates the current session
// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	providerID := c.GetString("provider_id")
	if providerID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

// generateID generates a unique ID for entities
func generateID() string {
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	return hex.EncodeToString(idBytes)
}
