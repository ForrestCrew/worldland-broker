// Package handler provides HTTP handlers for wallet authentication.
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/auth"
	"github.com/nubro999/worldland-gpu/internal/wallet"
)

// WalletAuthHandler handles wallet-based authentication.
type WalletAuthHandler struct {
	sessionManager *wallet.SessionManager
	verifier       *wallet.Verifier
	jwtManager     *auth.JWTManager
}

// NewWalletAuthHandler creates a new wallet auth handler.
func NewWalletAuthHandler(
	sessionManager *wallet.SessionManager,
	verifier *wallet.Verifier,
	jwtManager *auth.JWTManager,
) *WalletAuthHandler {
	return &WalletAuthHandler{
		sessionManager: sessionManager,
		verifier:       verifier,
		jwtManager:     jwtManager,
	}
}

// WalletLoginRequest represents a wallet login request.
type WalletLoginRequest struct {
	WalletAddress string `json:"wallet_address" binding:"required"`
	Message       string `json:"message" binding:"required"`
	Signature     string `json:"signature" binding:"required"`
	Timestamp     int64  `json:"timestamp" binding:"required"`
}

// WalletLoginResponse represents a wallet login response.
type WalletLoginResponse struct {
	Token         string `json:"token"`
	WalletAddress string `json:"wallet_address"`
	ExpiresAt     int64  `json:"expires_at"`
}

// RegisterSessionKeyRequest represents a session key registration request.
type RegisterSessionKeyRequest struct {
	MainWallet string  `json:"main_wallet" binding:"required"`
	SessionKey string  `json:"session_key" binding:"required"`
	SpendLimit float64 `json:"spend_limit" binding:"required"` // In USDT
	Duration   int64   `json:"duration" binding:"required"`    // In seconds (max 30 days)
	Signature  string  `json:"signature" binding:"required"`   // EIP-712 signature
}

// RegisterSessionKeyResponse represents a session key registration response.
type RegisterSessionKeyResponse struct {
	SessionKey string    `json:"session_key"`
	MainWallet string    `json:"main_wallet"`
	SpendLimit float64   `json:"spend_limit"`
	ExpiresAt  time.Time `json:"expires_at"`
	Message    string    `json:"message"`
}

// SessionInfoResponse represents session info response.
type SessionInfoResponse struct {
	SessionKey     string    `json:"session_key"`
	MainWallet     string    `json:"main_wallet"`
	SpendLimit     float64   `json:"spend_limit"`
	SpentAmount    float64   `json:"spent_amount"`
	RemainingQuota float64   `json:"remaining_quota"`
	ExpiresAt      time.Time `json:"expires_at"`
	IsActive       bool      `json:"is_active"`
	Permissions    []string  `json:"permissions"`
}

// WalletLogin handles wallet-based login with signature verification.
// POST /api/v1/auth/wallet
func (h *WalletAuthHandler) WalletLogin(c *gin.Context) {
	var req WalletLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Verify signature
	loginReq := &wallet.LoginRequest{
		WalletAddress: req.WalletAddress,
		Message:       req.Message,
		Signature:     req.Signature,
		Timestamp:     req.Timestamp,
	}

	valid, err := h.verifier.VerifyLoginSignature(loginReq)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "Signature verification failed",
			"details": err.Error(),
		})
		return
	}

	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid signature",
		})
		return
	}

	// Generate JWT token
	userClaims := &auth.UserClaims{
		Sub:   req.WalletAddress,              // Wallet address as user ID
		Email: "",                             // No email for wallet users
		Name:  req.WalletAddress[:10] + "...", // Shortened address as name
	}

	token, err := h.jwtManager.GenerateToken(userClaims)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to generate token",
		})
		return
	}

	expiresAt := time.Now().Add(24 * time.Hour).Unix()

	c.JSON(http.StatusOK, WalletLoginResponse{
		Token:         token,
		WalletAddress: req.WalletAddress,
		ExpiresAt:     expiresAt,
	})
}

// RegisterSessionKey registers a new session key.
// POST /api/v1/auth/session-key
func (h *WalletAuthHandler) RegisterSessionKey(c *gin.Context) {
	var req RegisterSessionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Validate duration (max 30 days)
	if req.Duration <= 0 || req.Duration > 30*24*60*60 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Duration must be between 1 second and 30 days",
		})
		return
	}

	// Validate spend limit
	if req.SpendLimit <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Spend limit must be greater than 0",
		})
		return
	}

	// Register session
	sessionReq := &wallet.SessionKeyRequest{
		MainWallet: req.MainWallet,
		SessionKey: req.SessionKey,
		SpendLimit: req.SpendLimit,
		Duration:   req.Duration,
		Signature:  req.Signature,
	}

	session, err := h.sessionManager.RegisterSession(c.Request.Context(), sessionReq)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "Failed to register session key",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, RegisterSessionKeyResponse{
		SessionKey: session.SessionKey,
		MainWallet: session.MainWallet,
		SpendLimit: session.SpendLimit,
		ExpiresAt:  session.ExpiresAt,
		Message:    "Session key registered successfully",
	})
}

// RevokeSessionKey revokes a session key.
// DELETE /api/v1/auth/session-key/:key
func (h *WalletAuthHandler) RevokeSessionKey(c *gin.Context) {
	sessionKey := c.Param("key")
	if sessionKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Session key is required"})
		return
	}

	// Get main wallet from auth context (must be authenticated)
	mainWallet, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	err := h.sessionManager.RevokeSession(c.Request.Context(), mainWallet.(string), sessionKey)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "Failed to revoke session key",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Session key revoked successfully",
		"session_key": sessionKey,
	})
}

// GetSessionInfo returns information about a session key.
// GET /api/v1/auth/session-key/:key
func (h *WalletAuthHandler) GetSessionInfo(c *gin.Context) {
	sessionKey := c.Param("key")
	if sessionKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Session key is required"})
		return
	}

	session, err := h.sessionManager.ValidateSession(c.Request.Context(), sessionKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Session not found or invalid",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, SessionInfoResponse{
		SessionKey:     session.SessionKey,
		MainWallet:     session.MainWallet,
		SpendLimit:     session.SpendLimit,
		SpentAmount:    session.SpentAmount,
		RemainingQuota: session.SpendLimit - session.SpentAmount,
		ExpiresAt:      session.ExpiresAt,
		IsActive:       session.IsActive,
		Permissions:    session.Permissions,
	})
}

// GetMySessions returns all sessions for the authenticated wallet.
// GET /api/v1/auth/sessions
func (h *WalletAuthHandler) GetMySessions(c *gin.Context) {
	mainWallet, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	sessions, err := h.sessionManager.GetSessionsByWallet(c.Request.Context(), mainWallet.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get sessions",
			"details": err.Error(),
		})
		return
	}

	var responses []SessionInfoResponse
	for _, s := range sessions {
		responses = append(responses, SessionInfoResponse{
			SessionKey:     s.SessionKey,
			MainWallet:     s.MainWallet,
			SpendLimit:     s.SpendLimit,
			SpentAmount:    s.SpentAmount,
			RemainingQuota: s.SpendLimit - s.SpentAmount,
			ExpiresAt:      s.ExpiresAt,
			IsActive:       s.IsActive,
			Permissions:    s.Permissions,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": responses,
		"count":    len(responses),
	})
}

// GenerateSessionKey generates a new session key pair (client-side helper).
// POST /api/v1/auth/generate-session-key
func (h *WalletAuthHandler) GenerateSessionKey(c *gin.Context) {
	privateKey, publicAddress, err := wallet.GenerateSessionKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to generate session key",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"private_key": privateKey,
		"address":     publicAddress,
		"warning":     "Store the private key securely. It cannot be recovered!",
	})
}

// GetLoginMessage returns the message to sign for login.
// GET /api/v1/auth/login-message
func (h *WalletAuthHandler) GetLoginMessage(c *gin.Context) {
	walletAddress := c.Query("wallet")
	if walletAddress == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wallet query parameter is required"})
		return
	}

	timestamp := time.Now().Unix()
	message := "Sign in to GPU Rental Platform\nWallet: " + walletAddress + "\nTimestamp: " + string(rune(timestamp))

	c.JSON(http.StatusOK, gin.H{
		"message":   message,
		"timestamp": timestamp,
	})
}
