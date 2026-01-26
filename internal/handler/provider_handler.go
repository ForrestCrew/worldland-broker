// Package handler provides HTTP handlers for the API.
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/provider"
)

// ProviderHandler handles provider-related API requests.
type ProviderHandler struct {
	orchestrator *provider.Orchestrator
	nodeManager  *provider.NodeManager
}

// NewProviderHandler creates a new ProviderHandler.
func NewProviderHandler(orchestrator *provider.Orchestrator) *ProviderHandler {
	return &ProviderHandler{orchestrator: orchestrator}
}

// SetNodeManager sets the node manager for real-time GPU queries.
func (h *ProviderHandler) SetNodeManager(nm *provider.NodeManager) {
	h.nodeManager = nm
}

// ListProviders returns all registered providers.
// GET /api/v1/providers
func (h *ProviderHandler) ListProviders(c *gin.Context) {
	if h.orchestrator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Orchestrator not available",
		})
		return
	}

	providers := h.orchestrator.ListProviders()
	c.JSON(http.StatusOK, gin.H{
		"providers": providers,
		"count":     len(providers),
	})
}

// SearchProviders searches providers by filter criteria.
// GET /api/v1/providers/search?gpu=RTX4090&min_ram=64000&min_cpu=8&min_disk=100&max_price=1.0&status=available
func (h *ProviderHandler) SearchProviders(c *gin.Context) {
	if h.orchestrator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Orchestrator not available",
		})
		return
	}

	filter := &provider.SearchFilter{
		Status:          c.Query("status"),
		GPUModel:        c.Query("gpu"),
		MinMemoryMB:     parseInt64(c.Query("min_ram")),
		MinCPUCores:     parseInt(c.Query("min_cpu")),
		MinDiskGB:       parseInt64(c.Query("min_disk")),
		MaxPricePerHour: parseFloat64(c.Query("max_price")),
		Limit:           parseInt(c.Query("limit")),
		Offset:          parseInt(c.Query("offset")),
	}

	providers, err := h.orchestrator.Search(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"providers": providers,
		"count":     len(providers),
		"filter":    filter,
	})
}

// GetProvider returns a specific provider by ID.
// GET /api/v1/providers/:id
func (h *ProviderHandler) GetProvider(c *gin.Context) {
	if h.orchestrator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Orchestrator not available",
		})
		return
	}

	providerID := c.Param("id")
	providerState, exists := h.orchestrator.GetProviderState(providerID)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Provider not found",
		})
		return
	}

	c.JSON(http.StatusOK, providerState)
}

// GPUAvailabilityResponse represents the real-time GPU availability.
type GPUAvailabilityResponse struct {
	ProviderID    string  `json:"provider_id"`
	GPUType       string  `json:"gpu_type"`
	TotalGPUs     int     `json:"total_gpus"`
	AvailableGPUs int     `json:"available_gpus"`
	Source        string  `json:"source"` // "cluster" or "cache"
	ClusterOnline bool    `json:"cluster_online"`
	CanCreateJob  bool    `json:"can_create_job"`
	PricePerHour  float64 `json:"price_per_hour"`
}

// GetGPUAvailability returns real-time GPU availability for all providers.
// GET /api/v1/providers/gpu-availability
func (h *ProviderHandler) GetGPUAvailability(c *gin.Context) {
	if h.orchestrator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Orchestrator not available",
		})
		return
	}

	providers := h.orchestrator.ListProviders()
	gpuType := c.Query("gpu_type") // optional filter

	var results []GPUAvailabilityResponse

	for _, p := range providers {
		// Skip if GPU type filter doesn't match
		if gpuType != "" && len(p.Spec.GPUs) > 0 && p.Spec.GPUs[0].Name != gpuType {
			continue
		}

		// Skip non-active providers
		if p.Status != provider.StatusApproved && p.Status != provider.StatusJoined && p.Status != provider.StatusAvailable {
			continue
		}

		result := GPUAvailabilityResponse{
			ProviderID:   p.ProviderID,
			TotalGPUs:    p.Spec.TotalGPUs,
			PricePerHour: p.Capacity.GPUPricePerHour,
		}

		if len(p.Spec.GPUs) > 0 {
			result.GPUType = p.Spec.GPUs[0].Name
		}

		// Try to get real-time availability from cluster
		if h.nodeManager != nil && p.Spec.Hostname != "" {
			actualGPU, err := h.nodeManager.GetNodeAvailableGPU(c.Request.Context(), p.Spec.Hostname)
			if err == nil {
				result.AvailableGPUs = int(actualGPU)
				result.Source = "cluster"
				result.ClusterOnline = true
			} else {
				// Fallback to cached value
				result.AvailableGPUs = p.Capacity.AvailableGPUCount()
				result.Source = "cache"
				result.ClusterOnline = false
			}
		} else {
			// Use cached value
			result.AvailableGPUs = p.Capacity.AvailableGPUCount()
			result.Source = "cache"
			result.ClusterOnline = false
		}

		result.CanCreateJob = result.AvailableGPUs > 0
		results = append(results, result)
	}

	// Calculate totals
	totalGPUs := 0
	totalAvailable := 0
	for _, r := range results {
		totalGPUs += r.TotalGPUs
		totalAvailable += r.AvailableGPUs
	}

	c.JSON(http.StatusOK, gin.H{
		"providers":       results,
		"total_gpus":      totalGPUs,
		"total_available": totalAvailable,
		"count":           len(results),
	})
}

// Helper functions
func parseInt(s string) int {
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseFloat64(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
