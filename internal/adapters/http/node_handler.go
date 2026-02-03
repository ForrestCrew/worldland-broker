package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/services"
)

// NodeHandler handles node HTTP requests
type NodeHandler struct {
	nodeService *services.NodeService
}

// NewNodeHandler creates a new node handler
func NewNodeHandler(nodeService *services.NodeService) *NodeHandler {
	return &NodeHandler{nodeService: nodeService}
}

// RegisterNodeRequest is the request body for node registration
type RegisterNodeRequest struct {
	GPUUUID     string `json:"gpu_uuid" binding:"required"`
	GPUType     string `json:"gpu_type" binding:"required"`
	MemoryGB    int    `json:"memory_gb" binding:"required,min=1"`
	PricePerSec string `json:"price_per_sec" binding:"required"`
}

// RegisterNode creates a new GPU node registration
// POST /api/v1/nodes
func (h *NodeHandler) RegisterNode(c *gin.Context) {
	providerID := c.GetString("provider_id")
	if providerID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
		return
	}

	var req RegisterNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	input := services.RegisterNodeInput{
		ProviderID:  providerID,
		GPUUUID:     req.GPUUUID,
		GPUType:     req.GPUType,
		MemoryGB:    req.MemoryGB,
		PricePerSec: req.PricePerSec,
	}

	node, err := h.nodeService.RegisterNode(c.Request.Context(), input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"node_id": node.ID,
		"status":  node.Status,
		"message": "Node registered successfully. mTLS certificate will be issued.",
	})
}

// UpdatePriceRequest is the request body for price updates
type UpdatePriceRequest struct {
	PricePerSec string `json:"price_per_sec" binding:"required"`
}

// UpdateNodePrice updates the pricing for a node (PROV-03)
// PATCH /api/v1/nodes/:id/price
func (h *NodeHandler) UpdateNodePrice(c *gin.Context) {
	providerID := c.GetString("provider_id")
	nodeID := c.Param("id")

	var req UpdatePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	node, err := h.nodeService.UpdateNodePricing(c.Request.Context(), nodeID, providerID, req.PricePerSec)
	if err != nil {
		if err.Error() == "not authorized to update this node" {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		if err.Error() == "node not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"node_id":       node.ID,
		"price_per_sec": node.PricePerSecond,
		"message":       "Price updated. New rentals will use updated price.",
	})
}

// ListNodes returns all nodes for the authenticated provider
// GET /api/v1/nodes
// In AUTH_DISABLED mode, returns all active nodes for E2E testing discovery
func (h *NodeHandler) ListNodes(c *gin.Context) {
	var nodes []*domain.Node
	var err error

	// In auth_disabled mode, return all active nodes for E2E testing
	if _, authDisabled := c.Get("auth_disabled"); authDisabled {
		nodes, err = h.nodeService.ListActiveNodes(c.Request.Context())
	} else {
		providerID := c.GetString("provider_id")
		nodes, err = h.nodeService.GetProviderNodes(c.Request.Context(), providerID)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list nodes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": nodes,
		"count": len(nodes),
	})
}

// GetNode returns a single node by ID
// GET /api/v1/nodes/:id
func (h *NodeHandler) GetNode(c *gin.Context) {
	nodeID := c.Param("id")

	node, err := h.nodeService.GetNode(c.Request.Context(), nodeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Node not found"})
		return
	}

	c.JSON(http.StatusOK, node)
}
