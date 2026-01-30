package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/rental"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// RentalHandler handles rental-related HTTP requests
type RentalHandler struct {
	matcher        *matching.ProviderMatcher
	sessionManager *sessions.SessionManager
	sessionRepo    domain.RentalSessionRepository
	providerRepo   domain.ProviderRepository
	nodeRepo       domain.NodeRepository
	nodeClient     rental.NodeClientInterface
}

// NewRentalHandler creates a new rental handler
func NewRentalHandler(
	matcher *matching.ProviderMatcher,
	sessionManager *sessions.SessionManager,
	sessionRepo domain.RentalSessionRepository,
	providerRepo domain.ProviderRepository,
	nodeRepo domain.NodeRepository,
	nodeClient rental.NodeClientInterface,
) *RentalHandler {
	return &RentalHandler{
		matcher:        matcher,
		sessionManager: sessionManager,
		sessionRepo:    sessionRepo,
		providerRepo:   providerRepo,
		nodeRepo:       nodeRepo,
		nodeClient:     nodeClient,
	}
}

// FindProvidersRequest represents a provider search request
type FindProvidersRequest struct {
	GPUType           string `json:"gpuType" binding:"required"`
	MinMemoryGB       int    `json:"minMemoryGb"`
	MaxPricePerSecond string `json:"maxPricePerSecond"`
	SortBy            string `json:"sortBy"` // "price" (default), "memory"
	Limit             int    `json:"limit"`
	Offset            int    `json:"offset"`
}

// FindProvidersResponse represents provider search results
type FindProvidersResponse struct {
	Providers       []*ProviderInfo `json:"providers"`
	TotalCount      int             `json:"totalCount"`
	Recommendations []*ProviderInfo `json:"recommendations,omitempty"`
}

// ProviderInfo represents a provider node in API response
type ProviderInfo struct {
	NodeID         string `json:"nodeId"`
	ProviderID     string `json:"providerId"`
	GPUType        string `json:"gpuType"`
	MemoryGB       int    `json:"memoryGb"`
	PricePerSecond string `json:"pricePerSecond"`
}

// FindProviders handles POST /api/v1/rentals/providers
// Returns available providers matching the search criteria
func (h *RentalHandler) FindProviders(c *gin.Context) {
	var req FindProvidersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 20
	}
	if req.SortBy == "" {
		req.SortBy = "price"
	}

	matchReq := matching.MatchRequest{
		GPUType:           req.GPUType,
		MinMemoryGB:       req.MinMemoryGB,
		MaxPricePerSecond: req.MaxPricePerSecond,
		SortBy:            req.SortBy,
		Limit:             req.Limit,
		Offset:            req.Offset,
	}

	result, err := h.matcher.FindProviders(c.Request.Context(), matchReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to find providers"})
		return
	}

	resp := FindProvidersResponse{
		Providers:  convertNodes(result.Nodes),
		TotalCount: result.TotalCount,
	}
	if len(result.Recommendations) > 0 {
		resp.Recommendations = convertNodes(result.Recommendations)
	}

	c.JSON(http.StatusOK, resp)
}

// CreateSessionRequest represents a rental session creation request
type CreateSessionRequest struct {
	NodeID         string `json:"nodeId" binding:"required"`
	PricePerSecond string `json:"pricePerSecond" binding:"required"`
}

// CreateSessionResponse represents the created session
type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Message   string `json:"message"`
}

// CreateSession handles POST /api/v1/rentals
// Creates a new rental session in PENDING state
func (h *RentalHandler) CreateSession(c *gin.Context) {
	// Get provider ID from auth context (set by auth middleware)
	providerID, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Look up provider to get wallet address (userAddress)
	provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userAddress := provider.WalletAddress

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// SessionManager.CreateSession looks up the Node by ID to get ProviderAddress,
	// then creates a PENDING session with UserAddress, ProviderAddress, NodeID, PricePerSecond
	session, err := h.sessionManager.CreateSession(
		c.Request.Context(),
		userAddress,
		req.NodeID,
		req.PricePerSecond,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.JSON(http.StatusCreated, CreateSessionResponse{
		SessionID: session.ID,
		State:     string(session.State),
		Message:   "Session created. Submit blockchain Start transaction to begin rental.",
	})
}

// ListSessionsResponse represents user's sessions
type ListSessionsResponse struct {
	Sessions   []*SessionInfo `json:"sessions"`
	TotalCount int            `json:"totalCount"`
}

// SessionInfo represents a session in API response
type SessionInfo struct {
	ID              string  `json:"id"`
	NodeID          string  `json:"nodeId"`
	ProviderAddress string  `json:"providerAddress"`
	State           string  `json:"state"`
	PricePerSecond  string  `json:"pricePerSecond"`
	RentalID        *uint64 `json:"rentalId,omitempty"`
	StartTime       *string `json:"startTime,omitempty"`
	EndTime         *string `json:"endTime,omitempty"`
	CreatedAt       string  `json:"createdAt"`
}

// ListSessions handles GET /api/v1/rentals
// Returns user's rental sessions
func (h *RentalHandler) ListSessions(c *gin.Context) {
	// Get provider ID from auth context
	providerID, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Look up provider to get wallet address
	provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userAddress := provider.WalletAddress

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")
	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	sessions, err := h.sessionRepo.ListByUser(c.Request.Context(), userAddress, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	c.JSON(http.StatusOK, ListSessionsResponse{
		Sessions:   convertSessions(sessions),
		TotalCount: len(sessions), // TODO: Add proper count query
	})
}

// CancelSession handles DELETE /api/v1/rentals/:id
// Cancels a PENDING session
func (h *RentalHandler) CancelSession(c *gin.Context) {
	// Get provider ID from auth context
	providerID, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Look up provider to get wallet address
	provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userAddress := provider.WalletAddress

	sessionID := c.Param("id")

	// Verify session belongs to user
	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.UserAddress != userAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	err = h.sessionManager.TransitionToCancelled(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrInvalidTransition) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "can only cancel PENDING sessions"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session cancelled"})
}

// StartRentalRequest represents a request to start a rental
type StartRentalRequest struct {
	SSHPublicKey string `json:"sshPublicKey" binding:"required"`
}

// StartRentalResponse represents the response with connection info
type StartRentalResponse struct {
	SessionID  string `json:"sessionId"`
	SSHHost    string `json:"sshHost"`
	SSHPort    int    `json:"sshPort"`
	SSHUser    string `json:"sshUser"`
	SSHCommand string `json:"sshCommand"`
	Message    string `json:"message"`
}

// HandleStartRental handles POST /api/v1/rentals/:id/start
// Calls Node to start the GPU container and returns SSH connection info
func (h *RentalHandler) HandleStartRental(c *gin.Context) {
	sessionID := c.Param("id")

	// Get authenticated user
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

	// Load session and verify ownership
	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.UserAddress != provider.WalletAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	if session.State != domain.RentalStatePending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session not in PENDING state"})
		return
	}

	// Parse request for SSH key
	var req StartRentalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sshPublicKey required"})
		return
	}

	// Lookup Node to get URL and GPU info
	node, err := h.nodeRepo.GetByID(c.Request.Context(), session.NodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "node not found"})
		return
	}

	if node.APIEndpoint == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "node API endpoint not configured"})
		return
	}

	// Call Node to start container
	nodeReq := rental.StartRentalRequest{
		SessionID:    sessionID,
		GPUDeviceID:  node.GPUUUID,
		SSHPublicKey: req.SSHPublicKey,
		Image:        "nvidia/cuda:12.1-runtime-ubuntu22.04", // Default image
		MemoryBytes:  8 * 1024 * 1024 * 1024,                 // 8GB default
		CPUCount:     4,                                      // 4 CPUs default
	}

	nodeResp, err := h.nodeClient.StartRental(c.Request.Context(), node.APIEndpoint, nodeReq)
	if err != nil {
		if errors.Is(err, rental.ErrNodeUnreachable) {
			c.JSON(http.StatusBadGateway, gin.H{"error": "node unreachable", "details": err.Error()})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to start rental on node", "details": err.Error()})
		return
	}

	// Return connection info to user
	c.JSON(http.StatusOK, StartRentalResponse{
		SessionID:  sessionID,
		SSHHost:    nodeResp.SSHHost,
		SSHPort:    nodeResp.SSHPort,
		SSHUser:    nodeResp.SSHUser,
		SSHCommand: nodeResp.SSHCommand,
		Message:    "Rental started. SSH into the container using the provided command.",
	})
}

// StopRentalRequest represents a request to stop a rental
type StopRentalRequest struct {
	// Empty for now, may add reason field later
}

// StopRentalResponse represents the response after stopping
type StopRentalResponse struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// HandleStopRental handles POST /api/v1/rentals/:id/stop
// Calls Node to stop the GPU container
func (h *RentalHandler) HandleStopRental(c *gin.Context) {
	sessionID := c.Param("id")

	// Get authenticated user
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

	// Load session and verify ownership
	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.UserAddress != provider.WalletAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	if session.State != domain.RentalStateRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session not in RUNNING state"})
		return
	}

	// Lookup Node to get URL
	node, err := h.nodeRepo.GetByID(c.Request.Context(), session.NodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "node not found"})
		return
	}

	if node.APIEndpoint == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "node API endpoint not configured"})
		return
	}

	// Call Node to stop container
	nodeReq := rental.StopRentalRequest{
		SessionID: sessionID,
	}

	_, err = h.nodeClient.StopRental(c.Request.Context(), node.APIEndpoint, nodeReq)
	if err != nil {
		if errors.Is(err, rental.ErrNodeUnreachable) {
			c.JSON(http.StatusBadGateway, gin.H{"error": "node unreachable", "details": err.Error()})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to stop rental on node", "details": err.Error()})
		return
	}

	// Return success
	c.JSON(http.StatusOK, StopRentalResponse{
		SessionID: sessionID,
		Message:   "Rental stop initiated. Container will be stopped shortly.",
	})
}

// Helper functions for conversion
func convertNodes(nodes []*domain.Node) []*ProviderInfo {
	if nodes == nil {
		return []*ProviderInfo{}
	}
	result := make([]*ProviderInfo, len(nodes))
	for i, n := range nodes {
		result[i] = &ProviderInfo{
			NodeID:         n.ID,
			ProviderID:     n.ProviderID,
			GPUType:        n.GPUType,
			MemoryGB:       n.MemoryGB,
			PricePerSecond: n.PricePerSecond,
		}
	}
	return result
}

func convertSessions(sessions []*domain.RentalSession) []*SessionInfo {
	if sessions == nil {
		return []*SessionInfo{}
	}
	result := make([]*SessionInfo, len(sessions))
	for i, s := range sessions {
		info := &SessionInfo{
			ID:              s.ID,
			NodeID:          s.NodeID,
			ProviderAddress: s.ProviderAddress,
			State:           string(s.State),
			PricePerSecond:  s.PricePerSecond,
			CreatedAt:       s.CreatedAt.Format(time.RFC3339),
		}
		if s.RentalID != nil {
			info.RentalID = s.RentalID
		}
		if s.StartTime != nil {
			t := s.StartTime.Format(time.RFC3339)
			info.StartTime = &t
		}
		if s.EndTime != nil {
			t := s.EndTime.Format(time.RFC3339)
			info.EndTime = &t
		}
		result[i] = info
	}
	return result
}
