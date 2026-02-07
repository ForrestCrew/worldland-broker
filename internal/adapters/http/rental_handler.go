package http

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"

	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/rental"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// RentalHandler handles rental-related HTTP requests
type RentalHandler struct {
	matcher          *matching.ProviderMatcher
	sessionManager   *sessions.SessionManager
	sessionRepo      domain.RentalSessionRepository
	providerRepo     domain.ProviderRepository
	nodeRepo         domain.NodeRepository
	nodeClient       rental.NodeClientInterface
	balanceValidator blockchain.BalanceValidatorInterface
	imageRepo        domain.ImageRepository     // Optional, for preset image listing (24-03)
	jobManager       *k8s.JobManager            // Optional, for K8s-based SSH info retrieval (legacy)
	executorRouter   *sessions.ExecutorRouter    // Phase 3: provider-type-aware SSH info
}

// NewRentalHandler creates a new rental handler
func NewRentalHandler(
	matcher *matching.ProviderMatcher,
	sessionManager *sessions.SessionManager,
	sessionRepo domain.RentalSessionRepository,
	providerRepo domain.ProviderRepository,
	nodeRepo domain.NodeRepository,
	nodeClient rental.NodeClientInterface,
	balanceValidator blockchain.BalanceValidatorInterface,
) *RentalHandler {
	return &RentalHandler{
		matcher:          matcher,
		sessionManager:   sessionManager,
		sessionRepo:      sessionRepo,
		providerRepo:     providerRepo,
		nodeRepo:         nodeRepo,
		nodeClient:       nodeClient,
		balanceValidator: balanceValidator,
	}
}

// WithImageRepository sets the ImageRepository for preset image listing (24-03)
func (h *RentalHandler) WithImageRepository(repo domain.ImageRepository) *RentalHandler {
	h.imageRepo = repo
	return h
}

// WithK8s sets the JobManager for K8s-based SSH info retrieval (legacy)
func (h *RentalHandler) WithK8s(jobManager *k8s.JobManager) *RentalHandler {
	h.jobManager = jobManager
	return h
}

// WithExecutorRouter sets the ExecutorRouter for provider-type-aware operations (Phase 3)
func (h *RentalHandler) WithExecutorRouter(router *sessions.ExecutorRouter) *RentalHandler {
	h.executorRouter = router
	return h
}

// FindProvidersRequest represents a provider search request
type FindProvidersRequest struct {
	GPUType           string `json:"gpuType"`
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
	NodeID          string `json:"nodeId"`
	ProviderID      string `json:"providerId"`
	ProviderAddress string `json:"providerAddress"` // Wallet address for smart contract
	GPUType         string `json:"gpuType"`
	VramGB          int    `json:"vramGb"`
	PricePerSecond  string `json:"pricePerSecond"`
	Region          string `json:"region"`
	Status          string `json:"status"`
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
		Providers:  h.convertNodes(c.Request.Context(), result.Nodes),
		TotalCount: result.TotalCount,
	}
	if len(result.Recommendations) > 0 {
		resp.Recommendations = h.convertNodes(c.Request.Context(), result.Recommendations)
	}

	c.JSON(http.StatusOK, resp)
}

// CreateSessionRequest represents a rental session creation request
type CreateSessionRequest struct {
	NodeID         string `json:"nodeId" binding:"required"`
	PricePerSecond string `json:"pricePerSecond" binding:"required"`
	Image          string `json:"image,omitempty"` // Optional: preset UUID or custom docker image URL (24-03)
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
	var userAddress string

	// Check if auth is disabled (E2E testing mode)
	if _, authDisabled := c.Get("auth_disabled"); authDisabled {
		// Use user_address directly from middleware
		if addr, exists := c.Get("user_address"); exists {
			userAddress = addr.(string)
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user_address required in auth_disabled mode"})
			return
		}
	} else {
		// Normal auth flow: Get provider ID from auth context (set by auth middleware)
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
		userAddress = provider.WalletAddress
	}

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate on-chain deposit balance (DEBT-02)
	if h.balanceValidator != nil {
		// Parse price from request
		pricePerSecond := new(big.Int)
		_, ok := pricePerSecond.SetString(req.PricePerSecond, 10)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid pricePerSecond format",
				"code":  "VAL_001",
			})
			return
		}

		// Calculate minimum required deposit (1 hour of rental)
		minDuration := big.NewInt(3600) // 1 hour in seconds
		requiredAmount := new(big.Int).Mul(pricePerSecond, minDuration)

		// Validate on-chain balance
		userAddr := common.HexToAddress(userAddress)
		hasSufficient, currentBalance, err := h.balanceValidator.ValidateDepositBalance(
			c.Request.Context(),
			userAddr,
			requiredAmount,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to validate balance",
				"code":  "BAL_001",
			})
			return
		}

		if !hasSufficient {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "예치금이 부족합니다", // Korean: Insufficient deposit
				"code":  "BAL_002",
				"details": gin.H{
					"required": requiredAmount.String(),
					"current":  currentBalance.String(),
				},
			})
			return
		}
	}

	// SessionManager.CreateSession looks up the Node by ID to get ProviderAddress,
	// then creates a PENDING session with UserAddress, ProviderAddress, NodeID, PricePerSecond, DockerImage
	session, err := h.sessionManager.CreateSession(
		c.Request.Context(),
		userAddress,
		req.NodeID,
		req.PricePerSecond,
		req.Image, // Pass image parameter: empty string uses default, UUID resolves preset, custom URL validated
	)
	if err != nil {
		// Handle image-related errors (24-03)
		if errors.Is(err, sessions.ErrInvalidImage) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "유효하지 않은 이미지 형식입니다", // Korean: Invalid image format
				"code":  "IMG_001",
			})
			return
		}
		if errors.Is(err, sessions.ErrImageNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "이미지를 찾을 수 없습니다", // Korean: Image not found
				"code":  "IMG_002",
			})
			return
		}
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
	// Settlement info (for STOPPED sessions)
	SettlementAmount string `json:"settlementAmount,omitempty"`
	// Node info
	GPUType  string `json:"gpuType,omitempty"`
	MemoryGB int    `json:"memoryGb,omitempty"`
	// SSH connection info (for RUNNING sessions)
	SSHHost     string `json:"sshHost,omitempty"`
	SSHPort     int    `json:"sshPort,omitempty"`
	SSHUser     string `json:"sshUser,omitempty"`
	SSHPassword string `json:"sshPassword,omitempty"`
}

// ListSessions handles GET /api/v1/rentals
// Returns user's rental sessions with node info and SSH connection details
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

	// Convert sessions with node info and SSH details
	sessionInfos := h.convertSessionsWithDetails(c.Request.Context(), sessions, userAddress)

	c.JSON(http.StatusOK, ListSessionsResponse{
		Sessions:   sessionInfos,
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
	SessionID   string `json:"sessionId"`
	SSHHost     string `json:"sshHost"`
	SSHPort     int    `json:"sshPort"`
	SSHUser     string `json:"sshUser"`
	SSHPassword string `json:"sshPassword"`
	SSHCommand  string `json:"sshCommand"`
	Message     string `json:"message"`
}

// HandleStartRental handles POST /api/v1/rentals/:id/start
// Returns SSH connection info for the running container
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

	// Session must be RUNNING (Pod provisioned by ConfirmationWorker)
	if session.State != domain.RentalStateRunning {
		if session.State == domain.RentalStatePending {
			c.JSON(http.StatusAccepted, gin.H{
				"error":   "session still pending",
				"message": "Pod is being provisioned. Please wait and retry.",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("session in %s state, expected RUNNING", session.State)})
		return
	}

	// Phase 3: ExecutorRouter-based SSH info retrieval (provider-type aware)
	if h.executorRouter != nil {
		sshInfo, err := h.executorRouter.GetSSHConnectionInfo(c.Request.Context(), session)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "container not ready",
				"message": "Container is starting. Please retry in a few seconds.",
				"details": err.Error(),
			})
			return
		}

		user := sshInfo.User
		if user == "" {
			user = "user"
		}
		sshCommand := fmt.Sprintf("ssh %s@%s -p %d", user, sshInfo.Host, sshInfo.Port)

		c.JSON(http.StatusOK, StartRentalResponse{
			SessionID:   sessionID,
			SSHHost:     sshInfo.Host,
			SSHPort:     int(sshInfo.Port),
			SSHUser:     user,
			SSHPassword: sshInfo.Password,
			SSHCommand:  sshCommand,
			Message:     "Container ready",
		})
		return
	}

	// Legacy: K8s-based SSH info retrieval
	if h.jobManager != nil {
		sshInfo, err := h.jobManager.GetSSHConnectionInfo(c.Request.Context(), session.UserAddress, sessionID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "pod not ready",
				"message": "Container is starting. Please retry in a few seconds.",
				"details": err.Error(),
			})
			return
		}

		sshCommand := fmt.Sprintf("ssh user@%s -p %d", sshInfo.Host, sshInfo.Port)

		c.JSON(http.StatusOK, StartRentalResponse{
			SessionID:   sessionID,
			SSHHost:     sshInfo.Host,
			SSHPort:     int(sshInfo.Port),
			SSHUser:     "user",
			SSHPassword: sshInfo.Password,
			SSHCommand:  sshCommand,
			Message:     "Container ready",
		})
		return
	}

	// Legacy Node API path (fallback when K8s is not enabled)
	node, err := h.nodeRepo.GetByID(c.Request.Context(), session.NodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "node not found"})
		return
	}

	if node.APIEndpoint == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "node API endpoint not configured and K8s not enabled"})
		return
	}

	// Parse request for SSH key (legacy path)
	var req StartRentalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sshPublicKey required"})
		return
	}

	// Call Node to start container
	nodeReq := rental.StartRentalRequest{
		SessionID:    sessionID,
		GPUDeviceID:  node.GPUUUID,
		SSHPublicKey: req.SSHPublicKey,
		Image:        "nvidia/cuda:12.1.1-runtime-ubuntu22.04",
		MemoryBytes:  8 * 1024 * 1024 * 1024,
		CPUCount:     4,
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

	c.JSON(http.StatusOK, StartRentalResponse{
		SessionID:   sessionID,
		SSHHost:     nodeResp.SSHHost,
		SSHPort:     nodeResp.SSHPort,
		SSHUser:     nodeResp.SSHUser,
		SSHPassword: "", // Legacy Node API doesn't provide password
		SSHCommand:  nodeResp.SSHCommand,
		Message:     "Rental started. SSH into the container using the provided command.",
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

// convertNodes converts domain.Node to ProviderInfo with provider wallet address lookup
func (h *RentalHandler) convertNodes(ctx context.Context, nodes []*domain.Node) []*ProviderInfo {
	if nodes == nil {
		return []*ProviderInfo{}
	}
	result := make([]*ProviderInfo, len(nodes))
	for i, n := range nodes {
		info := &ProviderInfo{
			NodeID:         n.ID,
			ProviderID:     n.ProviderID,
			GPUType:        n.GPUType,
			VramGB:         n.MemoryGB,
			PricePerSecond: cleanPriceString(n.PricePerSecond),
			Region:         "asia", // Default region for now
			Status:         "available",
		}

		// Lookup provider to get wallet address
		if h.providerRepo != nil && n.ProviderID != "" {
			provider, err := h.providerRepo.GetByID(ctx, n.ProviderID)
			if err == nil && provider != nil {
				info.ProviderAddress = provider.WalletAddress
			}
		}

		result[i] = info
	}
	return result
}

// cleanPriceString removes decimal points from price strings for BigInt compatibility
func cleanPriceString(price string) string {
	if idx := strings.Index(price, "."); idx != -1 {
		return price[:idx]
	}
	return price
}

// ImageInfo represents a base image in API response (24-03)
type ImageInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DockerImage string `json:"dockerImage"`
	Category    string `json:"category"`
	GPURequired bool   `json:"gpuRequired"`
	Description string `json:"description,omitempty"`
}

// ListImagesResponse represents the list of available preset images (24-03)
type ListImagesResponse struct {
	Images       []*ImageInfo `json:"images"`
	DefaultImage string       `json:"defaultImage"` // Used when no image specified
}

// ListImages handles GET /api/v1/images
// Returns available preset GPU container images
func (h *RentalHandler) ListImages(c *gin.Context) {
	// Check if image repository is configured
	if h.imageRepo == nil {
		c.JSON(http.StatusOK, ListImagesResponse{
			Images:       []*ImageInfo{},
			DefaultImage: domain.DefaultImage,
		})
		return
	}

	// Optional category filter
	category := c.Query("category")

	var images []*domain.BaseImage
	var err error

	if category != "" && domain.IsValidCategory(category) {
		images, err = h.imageRepo.ListByCategory(c.Request.Context(), domain.ImageCategory(category))
	} else {
		images, err = h.imageRepo.List(c.Request.Context())
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list images"})
		return
	}

	c.JSON(http.StatusOK, ListImagesResponse{
		Images:       convertImages(images),
		DefaultImage: domain.DefaultImage,
	})
}

func convertImages(images []*domain.BaseImage) []*ImageInfo {
	if images == nil {
		return []*ImageInfo{}
	}
	result := make([]*ImageInfo, len(images))
	for i, img := range images {
		result[i] = &ImageInfo{
			ID:          img.ID,
			Name:        img.Name,
			DockerImage: img.DockerImage,
			Category:    string(img.Category),
			GPURequired: img.GPURequired,
			Description: img.Description,
		}
	}
	return result
}

// ExtendSessionRequest represents a request to extend a session
type ExtendSessionRequest struct {
	ExtensionMinutes int    `json:"extensionMinutes" binding:"required"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

// ExtendSessionResponse represents the extension result
type ExtendSessionResponse struct {
	Success          bool   `json:"success"`
	NewExpiration    string `json:"newExpiration"`
	ExtensionMinutes int    `json:"extensionMinutes"`
	ExtensionCost    string `json:"extensionCost"`
	ExtensionCount   int    `json:"extensionCount"`
	Message          string `json:"message"`
}

// ExtendSessionErrorResponse for insufficient balance case
type ExtendSessionErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details struct {
		Required  string `json:"required,omitempty"`
		Current   string `json:"current,omitempty"`
		Shortfall string `json:"shortfall,omitempty"`
	} `json:"details,omitempty"`
}

// HandleExtendSession handles POST /api/v1/rentals/:id/extend
// Extends a running session by the specified duration
func (h *RentalHandler) HandleExtendSession(c *gin.Context) {
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

	// Parse request
	var req ExtendSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "extensionMinutes required"})
		return
	}

	// Call session manager
	result, err := h.sessionManager.ExtendSession(
		c.Request.Context(),
		sessionID,
		req.ExtensionMinutes,
		req.IdempotencyKey,
		provider.WalletAddress,
	)
	if err != nil {
		// Handle specific errors
		if errors.Is(err, sessions.ErrSessionNotRunning) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Session is not running",
				"code":  "EXT_001",
			})
			return
		}
		if errors.Is(err, sessions.ErrInsufficientBalance) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "Insufficient balance for extension",
				"code":    "EXT_002",
				"message": err.Error(),
			})
			return
		}
		if errors.Is(err, sessions.ErrExtensionLimitReached) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Maximum extension limit reached",
				"code":  "EXT_003",
			})
			return
		}
		if errors.Is(err, sessions.ErrMinimumDuration) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Extension must be at least 30 minutes",
				"code":  "EXT_004",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend session"})
		return
	}

	c.JSON(http.StatusOK, ExtendSessionResponse{
		Success:          true,
		NewExpiration:    result.NewExpiration.Format(time.RFC3339),
		ExtensionMinutes: result.ExtensionMinutes,
		ExtensionCost:    result.ExtensionCost.String(),
		ExtensionCount:   result.ExtensionCount,
		Message:          "Session extended successfully",
	})
}

// convertSessionsWithDetails converts sessions with node info and SSH details
func (h *RentalHandler) convertSessionsWithDetails(ctx context.Context, sessions []*domain.RentalSession, userAddress string) []*SessionInfo {
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
			PricePerSecond:  cleanPriceString(s.PricePerSecond),
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

		// Include settlement amount for completed sessions
		// DEBUG: Log settlement amount
		fmt.Printf("[DEBUG] Session %s: SettledAmount='%s', State=%s\n", s.ID, s.SettledAmount, s.State)
		if s.SettledAmount != "" {
			info.SettlementAmount = cleanPriceString(s.SettledAmount)
		}

		// Lookup node info (GPUType, MemoryGB)
		if h.nodeRepo != nil && s.NodeID != "" {
			node, err := h.nodeRepo.GetByID(ctx, s.NodeID)
			if err == nil && node != nil {
				info.GPUType = node.GPUType
				info.MemoryGB = node.MemoryGB
			}
		}

		// Get SSH connection info for RUNNING sessions (Phase 3: provider-type aware)
		if s.State == domain.RentalStateRunning {
			if h.executorRouter != nil {
				sshInfo, err := h.executorRouter.GetSSHConnectionInfo(ctx, s)
				if err == nil && sshInfo != nil {
					info.SSHHost = sshInfo.Host
					info.SSHPort = int(sshInfo.Port)
					info.SSHUser = sshInfo.User
					if info.SSHUser == "" {
						info.SSHUser = "user"
					}
					info.SSHPassword = sshInfo.Password
				}
			} else if h.jobManager != nil {
				sshInfo, err := h.jobManager.GetSSHConnectionInfo(ctx, userAddress, s.ID)
				if err == nil && sshInfo != nil {
					info.SSHHost = sshInfo.Host
					info.SSHPort = int(sshInfo.Port)
					info.SSHUser = "user"
					info.SSHPassword = sshInfo.Password
				}
			}
		}

		result[i] = info
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
