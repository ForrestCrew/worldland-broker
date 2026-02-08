package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/worldland/worldland-hub/internal/mining"
	"github.com/worldland/worldland-hub/internal/remote"
)

// MiningHandler handles mining-related HTTP requests for K8s and SDK providers
type MiningHandler struct {
	miningManager    *mining.K8sMiningManager
	gpuPool          *mining.GPUPool
	remoteJobManager *remote.JobManager // For SDK node mining status via heartbeat
	logger           *slog.Logger
}

// NewMiningHandler creates a new mining handler
func NewMiningHandler(
	miningManager *mining.K8sMiningManager,
	gpuPool *mining.GPUPool,
	logger *slog.Logger,
) *MiningHandler {
	return &MiningHandler{
		miningManager: miningManager,
		gpuPool:       gpuPool,
		logger:        logger,
	}
}

// WithRemoteJobManager sets the remote job manager for SDK mining status
func (h *MiningHandler) WithRemoteJobManager(rjm *remote.JobManager) *MiningHandler {
	h.remoteJobManager = rjm
	return h
}

// StartMiningRequest represents a mining start request
type StartMiningRequest struct {
	GPUCount int    `json:"gpuCount"`
	Image    string `json:"image,omitempty"`
}

// StartMining handles POST /api/v1/providers/:id/mining/start
func (h *MiningHandler) StartMining(c *gin.Context) {
	providerID := c.Param("id")

	var req StartMiningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Use defaults
		req.GPUCount = 1
	}

	config := mining.MiningConfig{
		GPUCount: req.GPUCount,
		Image:    req.Image,
	}

	if err := h.miningManager.DeployMiningPod(c.Request.Context(), providerID, config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to start mining",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Mining started",
		"gpuCount": req.GPUCount,
	})
}

// StopMining handles POST /api/v1/providers/:id/mining/stop
func (h *MiningHandler) StopMining(c *gin.Context) {
	providerID := c.Param("id")

	if err := h.miningManager.DeleteMiningPod(c.Request.Context(), providerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to stop mining",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mining stopped"})
}

// GetMiningStatus handles GET /api/v1/providers/:id/mining
func (h *MiningHandler) GetMiningStatus(c *gin.Context) {
	providerID := c.Param("id")

	resp := gin.H{}

	// K8s mining status
	status, err := h.miningManager.GetMiningStatus(c.Request.Context(), providerID)
	if err == nil {
		resp["mining"] = status
	}

	// GPU pool allocation
	allocation := h.gpuPool.GetAllocation(providerID)
	resp["allocation"] = allocation

	// SDK node mining status (from heartbeat)
	if h.remoteJobManager != nil {
		allStatus := h.remoteJobManager.GetAllMiningStatus()
		sdkNodes := make([]gin.H, 0)
		for nodeID, ms := range allStatus {
			sdkNodes = append(sdkNodes, gin.H{
				"nodeId":      nodeID,
				"state":       ms.State,
				"containerId": ms.ContainerID,
				"gpuCount":    ms.GPUCount,
				"startedAt":   ms.StartedAt,
				"lastSeen":    ms.LastSeen,
			})
		}
		resp["sdkNodes"] = sdkNodes
	}

	c.JSON(http.StatusOK, resp)
}

// AllocateGPURequest represents a GPU allocation request
type AllocateGPURequest struct {
	GPUCount int `json:"gpuCount" binding:"required"`
}

// AllocateMiningGPU handles POST /api/v1/providers/:id/mining/allocate
func (h *MiningHandler) AllocateMiningGPU(c *gin.Context) {
	providerID := c.Param("id")

	var req AllocateGPURequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gpuCount required"})
		return
	}

	if err := h.gpuPool.AllocateMining(providerID, req.GPUCount); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to allocate GPUs for mining",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "GPUs allocated for mining",
		"allocation": h.gpuPool.GetAllocation(providerID),
	})
}

// ReleaseMiningGPU handles POST /api/v1/providers/:id/mining/release
func (h *MiningHandler) ReleaseMiningGPU(c *gin.Context) {
	providerID := c.Param("id")

	var req AllocateGPURequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gpuCount required"})
		return
	}

	if err := h.gpuPool.ReleaseMining(providerID, req.GPUCount); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to release mining GPUs",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Mining GPUs released",
		"allocation": h.gpuPool.GetAllocation(providerID),
	})
}
