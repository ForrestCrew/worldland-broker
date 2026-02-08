package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
)

// ProviderHandler handles provider-related HTTP requests
type ProviderHandler struct {
	providerRepo    domain.ProviderRepository
	nodeRepo        domain.NodeRepository
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

// WithNodeRepo sets the node repository for GPU auto-discovery
func (h *ProviderHandler) WithNodeRepo(nodeRepo domain.NodeRepository) *ProviderHandler {
	h.nodeRepo = nodeRepo
	return h
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

		// Auto-discover GPU nodes from cluster and register them
		if h.nodeRepo != nil {
			client := h.clusterRegistry.GetClient(existing.ID)
			if client != nil {
				h.autoRegisterK8sGPUNodes(c.Request.Context(), existing.ID, client)
			}
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

	// Auto-discover GPU nodes from cluster and register them
	if h.nodeRepo != nil {
		client := h.clusterRegistry.GetClient(providerID)
		if client != nil {
			h.autoRegisterK8sGPUNodes(c.Request.Context(), providerID, client)
		}
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

// K8sNodeInfo represents a K8s cluster node's information
type K8sNodeInfo struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Roles      []string `json:"roles"`
	KubeletVer string   `json:"kubeletVersion"`
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	CPUs       string   `json:"cpus"`
	Memory     string   `json:"memory"`
}

// ProviderInfoResponse represents the provider info response
type ProviderInfoResponse struct {
	ID            string        `json:"id"`
	WalletAddress string        `json:"walletAddress"`
	ProviderType  string        `json:"providerType"`
	Status        string        `json:"status"`
	ClusterHost   *string       `json:"clusterHost,omitempty"`
	K8sNodes      []K8sNodeInfo `json:"k8sNodes,omitempty"`
	CreatedAt     string        `json:"createdAt"`
	UpdatedAt     string        `json:"updatedAt"`
}

// GetMyProvider returns the current authenticated provider's info
// GET /api/v1/providers/me
// For K8s providers, includes cluster node list queried from K8s API
func (h *ProviderHandler) GetMyProvider(c *gin.Context) {
	providerID := c.GetString("provider_id")
	if providerID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
		return
	}

	provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Provider not found"})
		return
	}

	resp := ProviderInfoResponse{
		ID:            provider.ID,
		WalletAddress: provider.WalletAddress,
		ProviderType:  string(provider.ProviderType),
		Status:        string(provider.Status),
		ClusterHost:   provider.ClusterHost,
		CreatedAt:     provider.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     provider.UpdatedAt.Format(time.RFC3339),
	}

	// If K8s provider, query cluster for node info
	if provider.ProviderType == domain.ProviderTypeK8s && h.clusterRegistry != nil {
		client := h.clusterRegistry.GetClient(providerID)
		if client != nil {
			k8sNodes, err := h.queryK8sNodes(c.Request.Context(), client)
			if err != nil {
				h.logger.Warn("failed to query K8s nodes", "error", err)
			} else {
				resp.K8sNodes = k8sNodes
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// autoRegisterK8sGPUNodes discovers GPU nodes in a K8s cluster and registers them
// as marketplace nodes. Nodes without GPUs are registered with cluster CPU/Memory info.
// K8s nodes have no APIEndpoint, which distinguishes them from Docker/mTLS nodes.
func (h *ProviderHandler) autoRegisterK8sGPUNodes(ctx context.Context, providerID string, client *k8s.ClusterClient) {
	nodeList, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		h.logger.Error("failed to list K8s nodes for GPU discovery", "error", err)
		return
	}

	registered := 0
	for _, n := range nodeList.Items {
		// Check if node is Ready
		isReady := false
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				isReady = true
				break
			}
		}
		if !isReady {
			continue
		}

		// Check for GPU resources
		gpuQty, hasGPU := n.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]
		gpuCount := int64(0)
		if hasGPU {
			gpuCount = gpuQty.Value()
		}

		// Determine GPU type from labels
		gpuType := "CPU Node"
		if hasGPU && gpuCount > 0 {
			// Try common GPU label conventions
			if model, ok := n.Labels["nvidia.com/gpu.product"]; ok {
				gpuType = model
			} else if model, ok := n.Labels["accelerator"]; ok {
				gpuType = model
			} else {
				gpuType = fmt.Sprintf("GPU x%d", gpuCount)
			}
		}

		// Calculate memory in GB
		memQty := n.Status.Capacity.Memory()
		memGB := int(memQty.Value() / (1024 * 1024 * 1024))
		if memGB <= 0 {
			memGB = 1
		}

		// Generate deterministic node ID from cluster + node name
		nodeID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(providerID+"/"+n.Name)).String()
		gpuUUID := fmt.Sprintf("k8s-%s-%s", providerID[:8], n.Name)

		// Check if already registered
		existing, _ := h.nodeRepo.GetByID(ctx, nodeID)
		if existing != nil {
			// Update status to active
			existing.Status = domain.NodeStatusActive
			existing.GPUType = gpuType
			existing.MemoryGB = memGB
			existing.UpdatedAt = time.Now()
			h.nodeRepo.Update(ctx, existing)
			registered++
			continue
		}

		// Register new K8s node (no APIEndpoint = K8s managed)
		now := time.Now()
		node := &domain.Node{
			ID:             nodeID,
			ProviderID:     providerID,
			GPUUUID:        gpuUUID,
			GPUType:        gpuType,
			MemoryGB:       memGB,
			PricePerSecond: "1000000000", // Default 1 Gwei/sec
			Status:         domain.NodeStatusActive,
			CreatedAt:      now,
			UpdatedAt:      now,
			// APIEndpoint intentionally empty → K8s routing
		}

		if err := h.nodeRepo.Create(ctx, node); err != nil {
			h.logger.Warn("failed to register K8s GPU node",
				"nodeName", n.Name, "error", err)
			continue
		}
		registered++
	}

	h.logger.Info("K8s GPU nodes registered",
		"providerID", providerID,
		"registered", registered,
		"total", len(nodeList.Items),
	)
}

// queryK8sNodes queries the K8s API for node information
func (h *ProviderHandler) queryK8sNodes(ctx context.Context, client *k8s.ClusterClient) ([]K8sNodeInfo, error) {
	nodeList, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var nodes []K8sNodeInfo
	for _, n := range nodeList.Items {
		// Determine node status
		status := "NotReady"
		for _, cond := range n.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				status = "Ready"
				break
			}
		}

		// Determine roles
		var roles []string
		for label := range n.Labels {
			if label == "node-role.kubernetes.io/control-plane" || label == "node-role.kubernetes.io/master" {
				roles = append(roles, "control-plane")
			}
			if label == "node-role.kubernetes.io/worker" {
				roles = append(roles, "worker")
			}
		}
		if len(roles) == 0 {
			roles = []string{"worker"}
		}

		nodes = append(nodes, K8sNodeInfo{
			Name:       n.Name,
			Status:     status,
			Roles:      roles,
			KubeletVer: n.Status.NodeInfo.KubeletVersion,
			OS:         n.Status.NodeInfo.OSImage,
			Arch:       n.Status.NodeInfo.Architecture,
			CPUs:       n.Status.Capacity.Cpu().String(),
			Memory:     n.Status.Capacity.Memory().String(),
		})
	}

	return nodes, nil
}
