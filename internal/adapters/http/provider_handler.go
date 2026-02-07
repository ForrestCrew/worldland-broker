package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
)

// ProviderHandler handles provider-related HTTP requests
type ProviderHandler struct {
	providerRepo    domain.ProviderRepository
	clusterRegistry *k8s.ExternalClusterRegistry
	logger          *slog.Logger
}

// NewProviderHandler creates a new provider handler
func NewProviderHandler(
	providerRepo domain.ProviderRepository,
	clusterRegistry *k8s.ExternalClusterRegistry,
	logger *slog.Logger,
) *ProviderHandler {
	return &ProviderHandler{
		providerRepo:    providerRepo,
		clusterRegistry: clusterRegistry,
		logger:          logger,
	}
}

// RegisterK8sProviderRequest represents a K8s provider registration request
type RegisterK8sProviderRequest struct {
	WalletAddress string `json:"walletAddress" binding:"required"`
	Kubeconfig    string `json:"kubeconfig" binding:"required"` // Base64 or raw kubeconfig YAML
}

// RegisterK8sProviderResponse represents the registration response
type RegisterK8sProviderResponse struct {
	ProviderID  string `json:"providerId"`
	ClusterHost string `json:"clusterHost"`
	Message     string `json:"message"`
}

// RegisterK8sProvider handles POST /api/v1/providers/k8s
// Registers a new K8s data center provider with kubeconfig
func (h *ProviderHandler) RegisterK8sProvider(c *gin.Context) {
	var req RegisterK8sProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if provider already exists
	existing, _ := h.providerRepo.GetByWallet(c.Request.Context(), req.WalletAddress)
	if existing != nil {
		// Update existing provider to K8s type
		existing.ProviderType = domain.ProviderTypeK8s
		existing.KubeconfigData = &req.Kubeconfig
		existing.UpdatedAt = time.Now()

		// Validate kubeconfig by trying to register cluster
		if err := h.clusterRegistry.RegisterCluster(existing.ID, []byte(req.Kubeconfig)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid kubeconfig",
				"details": err.Error(),
			})
			return
		}

		// Extract cluster host from registered client
		client := h.clusterRegistry.GetClient(existing.ID)
		if client != nil {
			existing.ClusterHost = &client.ExternalHost
		}

		if err := h.providerRepo.Update(c.Request.Context(), existing); err != nil {
			h.clusterRegistry.RemoveCluster(existing.ID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update provider"})
			return
		}

		clusterHost := ""
		if existing.ClusterHost != nil {
			clusterHost = *existing.ClusterHost
		}

		c.JSON(http.StatusOK, RegisterK8sProviderResponse{
			ProviderID:  existing.ID,
			ClusterHost: clusterHost,
			Message:     "K8s provider updated successfully",
		})
		return
	}

	// Create new K8s provider
	providerID := uuid.New().String()

	// Validate kubeconfig by trying to register cluster
	if err := h.clusterRegistry.RegisterCluster(providerID, []byte(req.Kubeconfig)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid kubeconfig",
			"details": err.Error(),
		})
		return
	}

	// Extract cluster host
	var clusterHost string
	client := h.clusterRegistry.GetClient(providerID)
	if client != nil {
		clusterHost = client.ExternalHost
	}

	now := time.Now()
	provider := &domain.Provider{
		ID:             providerID,
		WalletAddress:  req.WalletAddress,
		Status:         domain.ProviderStatusActive,
		ProviderType:   domain.ProviderTypeK8s,
		KubeconfigData: &req.Kubeconfig,
		ClusterHost:    &clusterHost,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.providerRepo.Create(c.Request.Context(), provider); err != nil {
		h.clusterRegistry.RemoveCluster(providerID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create provider"})
		return
	}

	h.logger.Info("K8s provider registered",
		"providerID", providerID,
		"wallet", req.WalletAddress,
		"clusterHost", clusterHost,
	)

	c.JSON(http.StatusCreated, RegisterK8sProviderResponse{
		ProviderID:  providerID,
		ClusterHost: clusterHost,
		Message:     "K8s provider registered successfully",
	})
}
