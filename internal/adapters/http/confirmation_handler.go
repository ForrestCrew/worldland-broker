package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// cleanConfirmPriceString removes decimal points from price strings for BigInt compatibility
func cleanConfirmPriceString(price string) string {
	if idx := strings.Index(price, "."); idx != -1 {
		return price[:idx]
	}
	return price
}

// txHashRegex validates Ethereum transaction hash format: 0x followed by 64 hex characters
var txHashRegex = regexp.MustCompile(`^0x[a-fA-F0-9]{64}$`)

// ConfirmRequest represents a rental confirmation request with txHash
type ConfirmRequest struct {
	TxHash string `json:"txHash" binding:"required"`
}

// ConfirmResponse represents the confirmation response
type ConfirmResponse struct {
	SessionID  string `json:"sessionId"`
	State      string `json:"state"`
	Message    string `json:"message"`
	SSHHost    string `json:"sshHost,omitempty"`
	SSHPort    int    `json:"sshPort,omitempty"`
	SSHUser    string `json:"sshUser,omitempty"`
	SSHCommand string `json:"sshCommand,omitempty"`
}

// ConfirmationHandler handles rental confirmation HTTP requests
type ConfirmationHandler struct {
	sessionRepo    domain.RentalSessionRepository
	providerRepo   domain.ProviderRepository
	nodeRepo       domain.NodeRepository
	jobManager     *k8s.JobManager            // Can be nil if K8s disabled (legacy)
	tenantOrch     *k8s.TenantOrchestrator     // Can be nil if K8s disabled (legacy)
	executorRouter *sessions.ExecutorRouter     // Phase 3: provider-type-aware (can be nil)
	logger         *slog.Logger
}

// NewConfirmationHandler creates a new confirmation handler
func NewConfirmationHandler(
	sessionRepo domain.RentalSessionRepository,
	providerRepo domain.ProviderRepository,
) *ConfirmationHandler {
	return &ConfirmationHandler{
		sessionRepo:  sessionRepo,
		providerRepo: providerRepo,
		jobManager:   nil,           // Set via WithK8s for backward compatibility
		tenantOrch:   nil,           // Set via WithK8s for backward compatibility
		logger:       slog.Default(), // Use default logger
	}
}

// WithK8s sets the K8s JobManager and TenantOrchestrator for Pod creation
func (h *ConfirmationHandler) WithK8s(jobManager *k8s.JobManager) *ConfirmationHandler {
	h.jobManager = jobManager
	return h
}

// WithK8sFull sets all K8s dependencies for E2E mode Pod creation
func (h *ConfirmationHandler) WithK8sFull(jobManager *k8s.JobManager, tenantOrch *k8s.TenantOrchestrator, nodeRepo domain.NodeRepository) *ConfirmationHandler {
	h.jobManager = jobManager
	h.tenantOrch = tenantOrch
	h.nodeRepo = nodeRepo
	return h
}

// WithExecutorRouter sets the ExecutorRouter for provider-type-aware operations (Phase 3)
func (h *ConfirmationHandler) WithExecutorRouter(router *sessions.ExecutorRouter) *ConfirmationHandler {
	h.executorRouter = router
	return h
}

// ConfirmRental handles POST /api/v1/rentals/:id/confirm
// Accepts txHash for blockchain verification, returns 202 Accepted
// Idempotent: Same txHash for same session returns current state
func (h *ConfirmationHandler) ConfirmRental(c *gin.Context) {
	sessionID := c.Param("id")

	var userAddress string

	// Check if auth is disabled (E2E testing mode)
	if _, authDisabled := c.Get("auth_disabled"); authDisabled {
		if addr, exists := c.Get("user_address"); exists {
			userAddress = addr.(string)
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user_address required in auth_disabled mode"})
			return
		}
	} else {
		// Normal auth flow: Get provider ID from auth context
		providerID, exists := c.Get("provider_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		userAddress = provider.WalletAddress
	}

	// Parse and validate request
	var req ConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "txHash is required"})
		return
	}

	// Validate txHash format (0x + 64 hex chars)
	if !txHashRegex.MatchString(req.TxHash) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid txHash format",
			"code":  "CONFIRM_001",
		})
		return
	}

	// Load session and verify ownership
	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.UserAddress != userAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	// Check if session is in PENDING state
	if session.State != domain.RentalStatePending {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "session not in PENDING state",
			"code":  "CONFIRM_002",
		})
		return
	}

	// Idempotency check: Look for existing session with this txHash
	existingSession, err := h.sessionRepo.GetByTxHash(c.Request.Context(), req.TxHash)
	if err == nil && existingSession != nil {
		// TxHash already exists - check if it's the same session (idempotent)
		if existingSession.ID == sessionID {
			// Same session, same txHash - idempotent response
			c.JSON(http.StatusAccepted, h.buildConfirmResponse(existingSession))
			return
		}
		// Different session has this txHash - FLOW-03 violation
		c.JSON(http.StatusConflict, gin.H{
			"error": "txHash already used by another session",
			"code":  "CONFIRM_003",
		})
		return
	}

	// Set txHash on session
	if err := h.sessionRepo.SetTxHash(c.Request.Context(), sessionID, req.TxHash); err != nil {
		// Could be unique constraint violation if another request raced
		c.JSON(http.StatusConflict, gin.H{
			"error": "failed to set txHash",
			"code":  "CONFIRM_004",
		})
		return
	}

	// Reload session to get updated state
	session, _ = h.sessionRepo.GetByID(c.Request.Context(), sessionID)

	// Return 202 Accepted - verification will happen in background by ConfirmationWorker
	c.JSON(http.StatusAccepted, h.buildConfirmResponse(session))
}

// buildConfirmResponse creates a ConfirmResponse from a session
func (h *ConfirmationHandler) buildConfirmResponse(session *domain.RentalSession) ConfirmResponse {
	resp := ConfirmResponse{
		SessionID: session.ID,
		State:     string(session.State),
		Message:   "Transaction verification in progress. Poll session status for updates.",
	}

	// If session is RUNNING, include SSH credentials
	// Note: In the full implementation, SSH credentials would be stored
	// in a separate table or cache after node provisioning
	if session.State == domain.RentalStateRunning {
		resp.Message = "Session is running. Use the SSH credentials to connect."
		// SSH credentials would be populated from session data or cache
		// For now, they'll be empty until we add SSH credential storage
	}

	return resp
}

// GetSessionResponse represents the GET session response with full details
type GetSessionResponse struct {
	ID              string  `json:"id"`
	NodeID          string  `json:"nodeId"`
	ProviderAddress string  `json:"providerAddress"`
	State           string  `json:"state"`
	PricePerSecond  string  `json:"pricePerSecond"`
	RentalID        *uint64 `json:"rentalId,omitempty"`
	TxHash          *string `json:"txHash,omitempty"`
	StartTime       *string `json:"startTime,omitempty"`
	EndTime         *string `json:"endTime,omitempty"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
	// SSH credentials only if state is RUNNING
	SSHHost     string `json:"sshHost,omitempty"`
	SSHPort     int    `json:"sshPort,omitempty"`
	SSHUser     string `json:"sshUser,omitempty"`
	SSHPassword string `json:"sshPassword,omitempty"`
	SSHCommand  string `json:"sshCommand,omitempty"`
}

// GetSession handles GET /api/v1/rentals/:id
// Returns current session state including verification status and SSH credentials if RUNNING
func (h *ConfirmationHandler) GetSession(c *gin.Context) {
	sessionID := c.Param("id")

	var userAddress string

	// Check if auth is disabled (E2E testing mode)
	if _, authDisabled := c.Get("auth_disabled"); authDisabled {
		if addr, exists := c.Get("user_address"); exists {
			userAddress = addr.(string)
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user_address required in auth_disabled mode"})
			return
		}
	} else {
		// Normal auth flow: Get provider ID from auth context
		providerID, exists := c.Get("provider_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		userAddress = provider.WalletAddress
	}

	// Load session
	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// Verify ownership
	if session.UserAddress != userAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	// Build response
	resp := GetSessionResponse{
		ID:              session.ID,
		NodeID:          session.NodeID,
		ProviderAddress: session.ProviderAddress,
		State:           string(session.State),
		PricePerSecond:  cleanConfirmPriceString(session.PricePerSecond),
		RentalID:        session.RentalID,
		TxHash:          session.TxHash,
		CreatedAt:       session.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       session.UpdatedAt.Format(time.RFC3339),
	}

	if session.StartTime != nil {
		t := session.StartTime.Format(time.RFC3339)
		resp.StartTime = &t
	}

	if session.EndTime != nil {
		t := session.EndTime.Format(time.RFC3339)
		resp.EndTime = &t
	}

	// If RUNNING, include SSH credentials (Phase 3: provider-type aware)
	if session.State == domain.RentalStateRunning {
		if h.executorRouter != nil {
			sshInfo, err := h.executorRouter.GetSSHConnectionInfo(c.Request.Context(), session)
			if err != nil {
				h.logger.Debug("SSH info not available", "sessionId", session.ID, "error", err)
			} else {
				resp.SSHHost = sshInfo.Host
				resp.SSHPort = int(sshInfo.Port)
				resp.SSHUser = sshInfo.User
				if resp.SSHUser == "" {
					resp.SSHUser = "ubuntu"
				}
				resp.SSHPassword = sshInfo.Password
				resp.SSHCommand = fmt.Sprintf("ssh %s@%s -p %d", resp.SSHUser, sshInfo.Host, sshInfo.Port)
			}
		} else if h.jobManager != nil {
			sshInfo, err := h.jobManager.GetSSHConnectionInfo(
				c.Request.Context(),
				session.UserAddress,
				session.ID,
			)
			if err != nil {
				h.logger.Debug("SSH info not available", "sessionId", session.ID, "error", err)
			} else {
				resp.SSHHost = sshInfo.Host
				resp.SSHPort = int(sshInfo.Port)
				resp.SSHUser = "ubuntu"
				resp.SSHPassword = sshInfo.Password
				resp.SSHCommand = fmt.Sprintf("ssh ubuntu@%s -p %d", sshInfo.Host, sshInfo.Port)
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}
