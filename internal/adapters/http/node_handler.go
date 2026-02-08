package http

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/services"
)

// cleanNodePriceString removes decimal points from price strings for BigInt compatibility
func cleanNodePriceString(price string) string {
	if idx := strings.Index(price, "."); idx != -1 {
		return price[:idx]
	}
	return price
}

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

	// Auto-detect node mTLS endpoint from request IP
	// SDK nodes call this API from their own IP, port 8444 is standard mTLS port
	apiEndpoint := fmt.Sprintf("https://%s:8444", c.ClientIP())

	input := services.RegisterNodeInput{
		ProviderID:  providerID,
		GPUUUID:     req.GPUUUID,
		GPUType:     req.GPUType,
		MemoryGB:    req.MemoryGB,
		PricePerSec: req.PricePerSec,
		APIEndpoint: apiEndpoint,
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

	fmt.Printf("[DEBUG UpdateNodePrice] providerID=%s nodeID=%s\n", providerID, nodeID)

	var req UpdatePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fmt.Printf("[DEBUG UpdateNodePrice] bind error: %v\n", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fmt.Printf("[DEBUG UpdateNodePrice] price_per_sec=%s\n", req.PricePerSec)

	node, err := h.nodeService.UpdateNodePricing(c.Request.Context(), nodeID, providerID, req.PricePerSec)
	if err != nil {
		fmt.Printf("[DEBUG UpdateNodePrice] service error: %v\n", err)
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

// NodeResponse is a cleaned node response for API compatibility
type NodeResponse struct {
	ID                string            `json:"id"`
	ProviderID        string            `json:"providerId"`
	GPUUUID           string            `json:"gpuUuid"`
	GPUType           string            `json:"gpuType"`
	MemoryGB          int               `json:"memoryGb"`
	PricePerSecond    string            `json:"pricePerSecond"`
	PricePerHour      string            `json:"pricePerHour"`
	APIEndpoint       string            `json:"apiEndpoint"`
	Status            domain.NodeStatus `json:"status"`
	CertificateExpiry *string           `json:"certificateExpiry,omitempty"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
}

// toNodeResponse converts a domain.Node to NodeResponse with cleaned price
func toNodeResponse(n *domain.Node) *NodeResponse {
	resp := &NodeResponse{
		ID:             n.ID,
		ProviderID:     n.ProviderID,
		GPUUUID:        n.GPUUUID,
		GPUType:        n.GPUType,
		MemoryGB:       n.MemoryGB,
		PricePerSecond: cleanNodePriceString(n.PricePerSecond),
		PricePerHour:   FormatWeiPerSecToPerHour(n.PricePerSecond),
		APIEndpoint:    n.APIEndpoint,
		Status:         n.Status,
		CreatedAt:      n.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      n.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if n.CertificateExpiry != nil {
		expiry := n.CertificateExpiry.Format("2006-01-02T15:04:05Z07:00")
		resp.CertificateExpiry = &expiry
	}
	return resp
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

	// Convert to cleaned response format
	result := make([]*NodeResponse, len(nodes))
	for i, n := range nodes {
		result[i] = toNodeResponse(n)
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": result,
		"count": len(result),
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

	c.JSON(http.StatusOK, toNodeResponse(node))
}
