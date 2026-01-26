package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/provider"
)

// MiningHandler handles mining-related API requests.
type MiningHandler struct {
	orchestrator *provider.Orchestrator
}

// NewMiningHandler creates a new MiningHandler.
func NewMiningHandler(orchestrator *provider.Orchestrator) *MiningHandler {
	return &MiningHandler{orchestrator: orchestrator}
}

// AllocateMiningGPURequest represents a request to allocate GPUs for mining.
type AllocateMiningGPURequest struct {
	GPUCount int    `json:"gpu_count" binding:"required,min=1"`
	GPUType  string `json:"gpu_type"` // Optional, defaults to first available type
	Reason   string `json:"reason"`   // Optional reason for allocation
}

// ReleaseMiningGPURequest represents a request to release GPUs from mining.
type ReleaseMiningGPURequest struct {
	GPUCount int    `json:"gpu_count" binding:"required,min=1"`
	GPUType  string `json:"gpu_type"` // Optional, defaults to first available type
}

// AllocateMiningGPU allocates additional GPUs for mining.
// POST /api/v1/providers/:id/mining/allocate
func (h *MiningHandler) AllocateMiningGPU(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id is required"})
		return
	}

	var req AllocateMiningGPURequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Default GPU type if not specified
	gpuType := req.GPUType
	if gpuType == "" {
		gpuType = "default"
	}

	// Allocate GPUs for mining
	err := h.orchestrator.AllocateMiningGPU(c.Request.Context(), providerID, gpuType, req.GPUCount)
	if err != nil {
		// Check if it's an insufficient resources error
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"error":      "insufficient_resources",
			"message":    err.Error(),
			"suggestion": "Wait for rental jobs to complete or reduce the requested GPU count",
		})
		return
	}

	// Get updated status
	status, _ := h.orchestrator.GetMiningStatus(c.Request.Context(), providerID)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Mining GPU allocation successful",
		"allocated":     req.GPUCount,
		"mining_status": status,
	})
}

// ReleaseMiningGPU releases GPUs from mining back to the rental pool.
// POST /api/v1/providers/:id/mining/release
func (h *MiningHandler) ReleaseMiningGPU(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id is required"})
		return
	}

	var req ReleaseMiningGPURequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Default GPU type if not specified
	gpuType := req.GPUType
	if gpuType == "" {
		gpuType = "default"
	}

	// Release GPUs from mining
	err := h.orchestrator.ReleaseMiningGPU(c.Request.Context(), providerID, gpuType, req.GPUCount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Get updated status
	status, _ := h.orchestrator.GetMiningStatus(c.Request.Context(), providerID)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Mining GPU released successfully",
		"released":      req.GPUCount,
		"mining_status": status,
	})
}

// GetMiningStatus returns the current mining status for a provider.
// GET /api/v1/providers/:id/mining
func (h *MiningHandler) GetMiningStatus(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id is required"})
		return
	}

	status, err := h.orchestrator.GetMiningStatus(c.Request.Context(), providerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Provider not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// StartMiningRequest represents a request to start mining.
type StartMiningRequest struct {
	Image         string            `json:"image"`          // Worldland node image
	GPUCount      int               `json:"gpu_count"`      // Initial GPU count (default: 1)
	CPUCores      int               `json:"cpu_cores"`      // CPU cores (default: 2)
	MemoryMB      int64             `json:"memory_mb"`      // Memory in MB (default: 4096)
	WalletAddress string            `json:"wallet_address"` // Required: coinbase address
	ExtraArgs     []string          `json:"extra_args"`     // Additional node arguments
	EnvVars       map[string]string `json:"env_vars"`       // Extra environment variables

	// Full Node settings
	NetworkID  int  `json:"network_id,omitempty"`  // Worldland network ID
	P2PPort    int  `json:"p2p_port,omitempty"`    // P2P listen port
	RPCEnabled bool `json:"rpc_enabled,omitempty"` // Enable RPC
	RPCPort    int  `json:"rpc_port,omitempty"`    // RPC port
}

// StartMining deploys a mining pod for a provider.
// POST /api/v1/providers/:id/mining/start
func (h *MiningHandler) StartMining(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id is required"})
		return
	}

	var req StartMiningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request",
			"details": err.Error(),
		})
		return
	}

	// Create mining config with defaults
	config := &provider.MiningConfig{
		Image:         req.Image,
		GPUCount:      req.GPUCount,
		CPUCores:      req.CPUCores,
		MemoryMB:      req.MemoryMB,
		WalletAddress: req.WalletAddress,
		ExtraArgs:     req.ExtraArgs,
		EnvVars:       req.EnvVars,
		NetworkID:     req.NetworkID,
		P2PPort:       req.P2PPort,
		RPCEnabled:    req.RPCEnabled,
		RPCPort:       req.RPCPort,
	}

	// Apply defaults
	if config.Image == "" {
		config.Image = "mingeyom/worldland-mio:latest"
	}
	if config.GPUCount <= 0 {
		config.GPUCount = 1
	}
	if config.CPUCores <= 0 {
		config.CPUCores = 2
	}
	if config.MemoryMB <= 0 {
		config.MemoryMB = 4096
	}
	if config.NetworkID == 0 {
		config.NetworkID = 10396 // Worldland chain ID
	}
	if config.P2PPort == 0 {
		config.P2PPort = 30303
	}

	// Deploy mining pod
	err := h.orchestrator.DeployMiningForProvider(c.Request.Context(), providerID, config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Get updated status
	status, _ := h.orchestrator.GetMiningStatus(c.Request.Context(), providerID)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Mining started successfully",
		"mining_status": status,
	})
}

// StopMining stops the mining pod for a provider.
// POST /api/v1/providers/:id/mining/stop
func (h *MiningHandler) StopMining(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_id is required"})
		return
	}

	err := h.orchestrator.StopMiningForProvider(c.Request.Context(), providerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Mining stopped successfully",
		"mining_status": map[string]interface{}{
			"status": "stopped",
		},
	})
}

// GetMiningMetrics returns aggregated mining metrics across all providers.
// GET /api/v1/mining/metrics
func (h *MiningHandler) GetMiningMetrics(c *gin.Context) {
	metrics := h.orchestrator.GetMiningMetrics()
	c.JSON(http.StatusOK, metrics)
}
