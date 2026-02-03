package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/monitoring"
)

// MonitoringHandler handles monitoring HTTP requests
type MonitoringHandler struct {
	service *monitoring.MonitoringService
	logger  *slog.Logger
}

// NewMonitoringHandler creates a new monitoring handler
func NewMonitoringHandler(service *monitoring.MonitoringService, logger *slog.Logger) *MonitoringHandler {
	return &MonitoringHandler{
		service: service,
		logger:  logger,
	}
}

// GetProviderStats handles GET /api/v1/monitoring/provider/stats
// Returns dashboard data for the authenticated provider
func (h *MonitoringHandler) GetProviderStats(c *gin.Context) {
	// Get authenticated provider from context (set by auth middleware)
	providerID, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	stats, err := h.service.GetProviderStats(c.Request.Context(), providerID.(string))
	if err != nil {
		h.logger.Error("failed to get provider stats", "providerId", providerID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get provider stats"})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetTenantUsage handles GET /api/v1/monitoring/tenant/:address
// Returns quota and usage for specified tenant
func (h *MonitoringHandler) GetTenantUsage(c *gin.Context) {
	address := c.Param("address")

	// Get authenticated provider - verify caller is authenticated
	_, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	usage, err := h.service.GetTenantUsage(c.Request.Context(), address)
	if err != nil {
		h.logger.Error("failed to get tenant usage", "address", address, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get tenant usage"})
		return
	}

	c.JSON(http.StatusOK, usage)
}

// GetAllSessions handles GET /api/v1/monitoring/sessions
// Returns all active GPU rental sessions
func (h *MonitoringHandler) GetAllSessions(c *gin.Context) {
	sessions, err := h.service.GetAllActiveSessions(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to get all sessions", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get sessions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// GetSessionMetrics handles GET /api/v1/monitoring/sessions/:id
// Returns metrics for a specific session
func (h *MonitoringHandler) GetSessionMetrics(c *gin.Context) {
	sessionID := c.Param("id")

	metrics, err := h.service.GetSessionMetrics(c.Request.Context(), sessionID)
	if err != nil {
		h.logger.Error("failed to get session metrics", "sessionId", sessionID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session metrics"})
		return
	}

	if metrics == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	c.JSON(http.StatusOK, metrics)
}
