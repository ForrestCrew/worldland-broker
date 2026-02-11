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
	"github.com/worldland/worldland-hub/internal/matching"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// RentalHandler handles rental-related HTTP requests
// V4: K8s only — no Docker/remote branching
type RentalHandler struct {
	matcher          *matching.ProviderMatcher
	sessionManager   *sessions.SessionManager
	sessionRepo      domain.RentalSessionRepository
	providerRepo     domain.ProviderRepository
	nodeRepo         domain.NodeRepository
	balanceValidator blockchain.BalanceValidatorInterface
	imageRepo        domain.ImageRepository // Optional, for preset image listing
	executor         domain.JobExecutor     // K8s executor for SSH info retrieval
}

// NewRentalHandler creates a new rental handler
func NewRentalHandler(
	matcher *matching.ProviderMatcher,
	sessionManager *sessions.SessionManager,
	sessionRepo domain.RentalSessionRepository,
	providerRepo domain.ProviderRepository,
	nodeRepo domain.NodeRepository,
	balanceValidator blockchain.BalanceValidatorInterface,
) *RentalHandler {
	return &RentalHandler{
		matcher:          matcher,
		sessionManager:   sessionManager,
		sessionRepo:      sessionRepo,
		providerRepo:     providerRepo,
		nodeRepo:         nodeRepo,
		balanceValidator: balanceValidator,
	}
}

// WithImageRepository sets the ImageRepository for preset image listing
func (h *RentalHandler) WithImageRepository(repo domain.ImageRepository) *RentalHandler {
	h.imageRepo = repo
	return h
}

// WithExecutor sets the K8s executor for SSH info retrieval
func (h *RentalHandler) WithExecutor(executor domain.JobExecutor) *RentalHandler {
	h.executor = executor
	return h
}

// FindProvidersRequest represents a provider search request
type FindProvidersRequest struct {
	GPUType           string `json:"gpuType"`
	GPUModel          string `json:"gpuModel"` // RunPod-style: filter by exact GPU model
	MinGPUCount       int    `json:"minGpuCount"`
	MinMemoryGB       int    `json:"minMemoryGb"`
	MinCPUCores       int    `json:"minCpuCores"`
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
	NodeID            string `json:"nodeId"`
	ProviderID        string `json:"providerId"`
	ProviderAddress   string `json:"providerAddress"` // Wallet address for smart contract
	GPUType           string `json:"gpuType"`
	GPUModel          string `json:"gpuModel"`        // NVML model name (e.g. "Tesla T4")
	VramGB            int    `json:"vramGb"`
	VramMB            int    `json:"vramMb"`
	TotalGPUs         int    `json:"totalGpus"`
	AvailableGPUs     int    `json:"availableGpus"`
	TotalCPUCores     int    `json:"totalCpuCores"`
	AvailableCPUCores int    `json:"availableCpuCores"`
	TotalMemoryGB     int    `json:"totalMemoryGb"`
	AvailableMemoryGB int    `json:"availableMemoryGb"`
	MaxStorageGB      int    `json:"maxStorageGb"`    // Max ephemeral storage available on node
	PricePerSecond    string `json:"pricePerSecond"`
	PricePerHour      string `json:"pricePerHour"` // Human-readable WLC/hr
	Region            string `json:"region"`
	Status            string `json:"status"`
}

// FindProviders handles POST /api/v1/rentals/providers
func (h *RentalHandler) FindProviders(c *gin.Context) {
	var req FindProvidersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 20
	}
	if req.SortBy == "" {
		req.SortBy = "price"
	}

	matchReq := matching.MatchRequest{
		GPUType:           req.GPUType,
		GPUModel:          req.GPUModel,
		MinGPUCount:       req.MinGPUCount,
		MinMemoryGB:       req.MinMemoryGB,
		MinCPUCores:       req.MinCPUCores,
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
	Image          string `json:"image,omitempty"`     // Optional: preset image UUID
	GPUCount       int    `json:"gpuCount,omitempty"`  // Default 1
	CPUCores       int    `json:"cpuCores,omitempty"`  // Default 4
	MemoryGB       int    `json:"memoryGB,omitempty"`  // Default 16
	StorageGB      int    `json:"storageGB,omitempty"` // Default 20
}

// CreateSessionResponse represents the created session
type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Message   string `json:"message"`
}

// CreateSession handles POST /api/v1/rentals
func (h *RentalHandler) CreateSession(c *gin.Context) {
	var userAddress string

	if _, authDisabled := c.Get("auth_disabled"); authDisabled {
		if addr, exists := c.Get("user_address"); exists {
			userAddress = addr.(string)
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user_address required in auth_disabled mode"})
			return
		}
	} else {
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

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Active session quota pre-check: reject if user already has a RUNNING or PENDING session
	if h.sessionRepo != nil {
		activeSessions, err := h.sessionRepo.FindByUserAndState(c.Request.Context(), userAddress, domain.RentalStateRunning)
		if err == nil && len(activeSessions) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error": "You already have an active rental. Please stop it before starting a new one.",
				"code":  "QUOTA_001",
			})
			return
		}
		pendingSessions, err := h.sessionRepo.FindByUserAndState(c.Request.Context(), userAddress, domain.RentalStatePending)
		if err == nil && len(pendingSessions) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error": "You already have an active rental. Please stop it before starting a new one.",
				"code":  "QUOTA_001",
			})
			return
		}
	}

	// Resource bounds validation (proxy job_handler.go pattern)
	if req.GPUCount < 0 || req.GPUCount > 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "GPU 수량은 0~8 범위입니다", "code": "RES_001"})
		return
	}
	if req.CPUCores < 0 || req.CPUCores > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CPU 코어는 0~64 범위입니다", "code": "RES_002"})
		return
	}
	if req.MemoryGB < 0 || req.MemoryGB > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "메모리는 0~256GB 범위입니다", "code": "RES_003"})
		return
	}
	if req.StorageGB < 0 || req.StorageGB > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "스토리지는 0~500GB 범위입니다", "code": "RES_004"})
		return
	}

	// Node capacity pre-validation
	// For user-specified values (> 0): reject if exceeds node capacity
	// For defaults (0): will be capped in SessionManager, no rejection here
	if h.nodeRepo != nil {
		node, err := h.nodeRepo.GetByID(c.Request.Context(), req.NodeID)
		if err == nil && node != nil {
			if req.GPUCount > 0 && node.TotalGPUs > 0 && req.GPUCount > node.AvailableGPUs {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "GPU 부족",
					"code":      "RES_005",
					"requested": req.GPUCount,
					"available": node.AvailableGPUs,
				})
				return
			}

			availCPU := node.AvailableCPUCores
			if availCPU == 0 {
				availCPU = node.TotalCPUCores // Fallback if not yet tracked
			}
			if req.CPUCores > 0 && availCPU > 0 && req.CPUCores > availCPU {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "CPU 부족",
					"code":      "RES_006",
					"requested": req.CPUCores,
					"available": availCPU,
				})
				return
			}

			availMem := node.AvailableMemoryGB
			if availMem == 0 {
				availMem = node.TotalMemoryGB // Fallback if not yet tracked
			}
			if req.MemoryGB > 0 && availMem > 0 && req.MemoryGB > availMem {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":     "메모리 부족",
					"code":      "RES_007",
					"requested": req.MemoryGB,
					"available": availMem,
				})
				return
			}
		}
	}

	// Validate on-chain deposit balance
	if h.balanceValidator != nil {
		pricePerSecond := new(big.Int)
		_, ok := pricePerSecond.SetString(req.PricePerSecond, 10)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid pricePerSecond format",
				"code":  "VAL_001",
			})
			return
		}

		minDuration := big.NewInt(3600)
		requiredAmount := new(big.Int).Mul(pricePerSecond, minDuration)

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
				"error": "예치금이 부족합니다",
				"code":  "BAL_002",
				"details": gin.H{
					"required": requiredAmount.String(),
					"current":  currentBalance.String(),
				},
			})
			return
		}
	}

	resources := &sessions.ResourceSpec{
		GPUCount:  req.GPUCount,
		CPUCores:  req.CPUCores,
		MemoryGB:  req.MemoryGB,
		StorageGB: req.StorageGB,
	}

	session, err := h.sessionManager.CreateSession(
		c.Request.Context(),
		userAddress,
		req.NodeID,
		req.PricePerSecond,
		req.Image,
		resources,
	)
	if err != nil {
		if errors.Is(err, sessions.ErrInvalidImage) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "유효하지 않은 이미지 형식입니다",
				"code":  "IMG_001",
			})
			return
		}
		if errors.Is(err, sessions.ErrImageNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "이미지를 찾을 수 없습니다",
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
	PricePerHour    string  `json:"pricePerHour"`
	RentalID        *uint64 `json:"rentalId,omitempty"`
	StartTime       *string `json:"startTime,omitempty"`
	EndTime         *string `json:"endTime,omitempty"`
	CreatedAt       string  `json:"createdAt"`
	// Settlement info
	SettlementAmount        string `json:"settlementAmount,omitempty"`
	SettlementAmountDisplay string `json:"settlementAmountDisplay,omitempty"`
	// Node info
	GPUType  string `json:"gpuType,omitempty"`
	MemoryGB int    `json:"memoryGb,omitempty"`
	// Requested resources
	DockerImage string `json:"dockerImage,omitempty"`
	GPUCount    int    `json:"gpuCount,omitempty"`
	CPUCores    int    `json:"cpuCores,omitempty"`
	MemoryGBReq int   `json:"memoryGbReq,omitempty"`
	StorageGB   int    `json:"storageGb,omitempty"`
	// SSH connection info (for RUNNING sessions)
	SSHHost     string `json:"sshHost,omitempty"`
	SSHPort     int    `json:"sshPort,omitempty"`
	SSHUser     string `json:"sshUser,omitempty"`
	SSHPassword string `json:"sshPassword,omitempty"`
	// Container status (for PENDING sessions: "Pending"|"Creating"|"Starting"|"Running"|"Failed")
	ContainerStatus string `json:"containerStatus,omitempty"`
}

// ListSessions handles GET /api/v1/rentals
func (h *RentalHandler) ListSessions(c *gin.Context) {
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
	userAddress := provider.WalletAddress

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")
	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	rentalSessions, err := h.sessionRepo.ListByUser(c.Request.Context(), userAddress, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	sessionInfos := h.convertSessionsWithDetails(c.Request.Context(), rentalSessions, userAddress)

	c.JSON(http.StatusOK, ListSessionsResponse{
		Sessions:   sessionInfos,
		TotalCount: len(rentalSessions),
	})
}

// CancelSession handles DELETE /api/v1/rentals/:id
func (h *RentalHandler) CancelSession(c *gin.Context) {
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
	userAddress := provider.WalletAddress

	sessionID := c.Param("id")

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

	// K8s executor-based SSH info retrieval
	if h.executor != nil {
		sshInfo, err := h.executor.GetSSHConnectionInfo(c.Request.Context(), session)
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

	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error":   "no executor configured",
		"message": "K8s executor is not available",
	})
}

// HandleStopRental handles POST /api/v1/rentals/:id/stop
func (h *RentalHandler) HandleStopRental(c *gin.Context) {
	sessionID := c.Param("id")

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

	// Delete K8s Pod via executor
	if h.executor != nil {
		if err := h.executor.DeleteGPUSession(c.Request.Context(), session); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "failed to stop GPU session",
				"details": err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"sessionId": sessionID,
		"message":   "Rental stop initiated. Container will be stopped shortly.",
	})
}

// convertNodes converts domain.Node to ProviderInfo with provider wallet address lookup
func (h *RentalHandler) convertNodes(ctx context.Context, nodes []*domain.Node) []*ProviderInfo {
	if nodes == nil {
		return []*ProviderInfo{}
	}
	result := make([]*ProviderInfo, len(nodes))
	for i, n := range nodes {
		// Use real GPU VRAM if available, fallback to system memory
		vramGB := n.VramMB / 1024
		if vramGB == 0 {
			vramGB = n.MemoryGB // Legacy fallback
		}
		gpuModel := n.GPUModel
		if gpuModel == "" {
			gpuModel = n.GPUType // Fallback to GPUType
		}

		info := &ProviderInfo{
			NodeID:            n.ID,
			ProviderID:        n.ProviderID,
			GPUType:           n.GPUType,
			GPUModel:          gpuModel,
			VramGB:            vramGB,
			VramMB:            n.VramMB,
			TotalGPUs:         n.TotalGPUs,
			AvailableGPUs:     n.AvailableGPUs,
			TotalCPUCores:     n.TotalCPUCores,
			AvailableCPUCores: n.AvailableCPUCores,
			TotalMemoryGB:     n.TotalMemoryGB,
			AvailableMemoryGB: n.AvailableMemoryGB,
			MaxStorageGB:      n.MaxStorageGB,
			PricePerSecond:    cleanPriceString(n.PricePerSecond),
			PricePerHour:      FormatWeiPerSecToPerHour(n.PricePerSecond),
			Region:            "asia",
			Status:            "available",
		}

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

// ImageInfo represents a base image in API response
type ImageInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DockerImage string `json:"dockerImage"`
	Category    string `json:"category"`
	GPURequired bool   `json:"gpuRequired"`
	Description string `json:"description,omitempty"`
}

// ListImagesResponse represents the list of available preset images
type ListImagesResponse struct {
	Images       []*ImageInfo `json:"images"`
	DefaultImage string       `json:"defaultImage"`
}

// ListImages handles GET /api/v1/images
func (h *RentalHandler) ListImages(c *gin.Context) {
	if h.imageRepo == nil {
		c.JSON(http.StatusOK, ListImagesResponse{
			Images:       []*ImageInfo{},
			DefaultImage: domain.DefaultImage,
		})
		return
	}

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

// GPUTypeResponse represents a GPU type group in API response
type GPUTypeResponse struct {
	GPUModel      string `json:"gpuModel"`
	VramGB        int    `json:"vramGb"`
	VramMB        int    `json:"vramMb"`
	TotalGPUs     int    `json:"totalGpus"`
	AvailableGPUs int    `json:"availableGpus"`
	TotalNodes    int    `json:"totalNodes"`
	PricePerHour  string `json:"pricePerHour"`
	AvgCPUCores   int    `json:"avgCpuCores"`
	AvgMemoryGB   int    `json:"avgMemoryGb"`
	Availability  string `json:"availability"` // "high"|"medium"|"low"|"none"
}

// ListGPUTypes handles GET /api/v1/gpu-types
// Returns GPU types grouped for RunPod-style marketplace display
func (h *RentalHandler) ListGPUTypes(c *gin.Context) {
	groups, err := h.nodeRepo.ListActiveGroupedByGPU(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list GPU types"})
		return
	}

	response := make([]*GPUTypeResponse, len(groups))
	for i, g := range groups {
		// Calculate availability level
		availability := "none"
		switch {
		case g.AvailableGPUs > 5:
			availability = "high"
		case g.AvailableGPUs > 2:
			availability = "medium"
		case g.AvailableGPUs > 0:
			availability = "low"
		}

		response[i] = &GPUTypeResponse{
			GPUModel:      g.GPUModel,
			VramGB:        g.VramMB / 1024,
			VramMB:        g.VramMB,
			TotalGPUs:     g.TotalGPUs,
			AvailableGPUs: g.AvailableGPUs,
			TotalNodes:    g.TotalNodes,
			PricePerHour:  FormatWeiPerSecToPerHour(g.MinPricePerSec),
			AvgCPUCores:   g.AvgCPUCores,
			AvgMemoryGB:   g.AvgMemoryGB,
			Availability:  availability,
		}
	}

	c.JSON(http.StatusOK, gin.H{"gpuTypes": response})
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

// HandleExtendSession handles POST /api/v1/rentals/:id/extend
func (h *RentalHandler) HandleExtendSession(c *gin.Context) {
	sessionID := c.Param("id")

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

	session, err := h.sessionRepo.GetByID(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.UserAddress != provider.WalletAddress {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	var req ExtendSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "extensionMinutes required"})
		return
	}

	result, err := h.sessionManager.ExtendSession(
		c.Request.Context(),
		sessionID,
		req.ExtensionMinutes,
		req.IdempotencyKey,
		provider.WalletAddress,
	)
	if err != nil {
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
func (h *RentalHandler) convertSessionsWithDetails(ctx context.Context, rentalSessions []*domain.RentalSession, userAddress string) []*SessionInfo {
	if rentalSessions == nil {
		return []*SessionInfo{}
	}
	result := make([]*SessionInfo, len(rentalSessions))
	for i, s := range rentalSessions {
		info := &SessionInfo{
			ID:              s.ID,
			NodeID:          s.NodeID,
			ProviderAddress: s.ProviderAddress,
			State:           string(s.State),
			PricePerSecond:  cleanPriceString(s.PricePerSecond),
			PricePerHour:    FormatWeiPerSecToPerHour(s.PricePerSecond),
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

		if s.SettledAmount != "" {
			info.SettlementAmount = cleanPriceString(s.SettledAmount)
			info.SettlementAmountDisplay = FormatWeiToDisplay(s.SettledAmount)
		}

		// Requested resources
		info.DockerImage = s.DockerImage
		info.GPUCount = s.GPUCount
		info.CPUCores = s.CPUCores
		info.MemoryGBReq = s.MemoryGB
		info.StorageGB = s.StorageGB

		if h.nodeRepo != nil && s.NodeID != "" {
			node, err := h.nodeRepo.GetByID(ctx, s.NodeID)
			if err == nil && node != nil {
				info.GPUType = node.GPUType
				info.MemoryGB = node.MemoryGB
			}
		}

		// Get SSH connection info and real container status for RUNNING sessions
		if s.State == domain.RentalStateRunning && h.executor != nil {
			// Check actual Pod status (Pod may still be Creating even though DB says RUNNING)
			status, err := h.executor.GetPodStatus(ctx, s)
			if err == nil {
				info.ContainerStatus = status
			} else {
				info.ContainerStatus = "Running"
			}

			sshInfo, err := h.executor.GetSSHConnectionInfo(ctx, s)
			if err == nil && sshInfo != nil {
				info.SSHHost = sshInfo.Host
				info.SSHPort = int(sshInfo.Port)
				info.SSHUser = sshInfo.User
				if info.SSHUser == "" {
					info.SSHUser = "user"
				}
				info.SSHPassword = sshInfo.Password
			}
		}

		// Get container status for PENDING sessions with tx_hash (Pod being provisioned)
		if s.State == domain.RentalStatePending && s.TxHash != nil && h.executor != nil {
			status, err := h.executor.GetPodStatus(ctx, s)
			if err == nil {
				info.ContainerStatus = status
			}
		}

		result[i] = info
	}
	return result
}

func convertSessions(rentalSessions []*domain.RentalSession) []*SessionInfo {
	if rentalSessions == nil {
		return []*SessionInfo{}
	}
	result := make([]*SessionInfo, len(rentalSessions))
	for i, s := range rentalSessions {
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
