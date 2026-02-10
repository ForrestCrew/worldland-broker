package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/domain"
)

// Known GPU specs for basic validation (from research: prevent obvious fraud)
var knownGPUMaxMemory = map[string]int{
	"RTX 4090": 24,
	"RTX 4080": 16,
	"RTX 3090": 24,
	"RTX 3080": 10,
	"A100":     80,
	"H100":     80,
	"V100":     32,
	"L40":      48,
}

// NodeService handles node registration and management
type NodeService struct {
	nodeRepo     domain.NodeRepository
	providerRepo domain.ProviderRepository
}

// NewNodeService creates a new node service
func NewNodeService(nodeRepo domain.NodeRepository) *NodeService {
	return &NodeService{nodeRepo: nodeRepo}
}

// NewNodeServiceWithProvider creates a new node service with provider repository
// Required for auto-registration of nodes via mTLS
func NewNodeServiceWithProvider(nodeRepo domain.NodeRepository, providerRepo domain.ProviderRepository) *NodeService {
	return &NodeService{nodeRepo: nodeRepo, providerRepo: providerRepo}
}

// RegisterNodeInput contains node registration parameters
type RegisterNodeInput struct {
	ProviderID   string
	GPUUUID      string
	GPUType      string
	MemoryGB     int
	PricePerSec  string // Decimal string for wei precision
	APIEndpoint  string // Node's mTLS endpoint (auto-detected from request IP)
	GPUModel     string // NVML model name (e.g. "Tesla T4")
	VramMB       int    // GPU VRAM in MB
}

// RegisterNode creates a new node registration for a provider
// If a node with the same GPU UUID already exists, it reactivates the existing node
func (s *NodeService) RegisterNode(ctx context.Context, input RegisterNodeInput) (*domain.Node, error) {
	// Validate GPU specs (basic fraud prevention per research)
	maxMem, known := knownGPUMaxMemory[input.GPUType]
	if known && input.MemoryGB > maxMem {
		return nil, fmt.Errorf("invalid memory for %s: max is %dGB, got %dGB",
			input.GPUType, maxMem, input.MemoryGB)
	}

	// Validate memory is positive
	if input.MemoryGB <= 0 {
		return nil, fmt.Errorf("memory must be positive")
	}

	// Validate price is a valid positive number
	price, err := strconv.ParseFloat(input.PricePerSec, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid price format")
	}
	if price <= 0 {
		return nil, fmt.Errorf("price must be positive")
	}

	// Check for existing node with same GPU UUID (duplicate prevention)
	existingNode, err := s.nodeRepo.GetByGPUUUID(ctx, input.GPUUUID)
	if err == nil && existingNode != nil {
		// Node already exists - reactivate it with updated info
		existingNode.ProviderID = input.ProviderID
		existingNode.GPUType = input.GPUType
		existingNode.MemoryGB = input.MemoryGB
		existingNode.PricePerSecond = input.PricePerSec
		existingNode.APIEndpoint = input.APIEndpoint
		existingNode.Status = domain.NodeStatusActive // Active since node is connecting
		existingNode.UpdatedAt = time.Now()

		if err := s.nodeRepo.Update(ctx, existingNode); err != nil {
			return nil, fmt.Errorf("failed to reactivate existing node: %w", err)
		}
		return existingNode, nil
	}

	// Ensure provider exists (auto-create for E2E testing if providerRepo available)
	if s.providerRepo != nil {
		_, err := s.providerRepo.GetByID(ctx, input.ProviderID)
		if err != nil {
			// Provider doesn't exist - create one for E2E testing
			provider := &domain.Provider{
				ID:            input.ProviderID,
				WalletAddress: fmt.Sprintf("0x%040s", input.ProviderID[:8]), // Mock address
				Status:        domain.ProviderStatusActive,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}
			if err := s.providerRepo.Create(ctx, provider); err != nil {
				return nil, fmt.Errorf("failed to create provider: %w", err)
			}
		}
	}

	now := time.Now()
	node := &domain.Node{
		ID:             uuid.New().String(),
		ProviderID:     input.ProviderID,
		GPUUUID:        input.GPUUUID,
		GPUType:        input.GPUType,
		MemoryGB:       input.MemoryGB,
		PricePerSecond: input.PricePerSec,
		APIEndpoint:    input.APIEndpoint,
		Status:         domain.NodeStatusActive, // Active since node is connecting
		GPUModel:       input.GPUModel,
		VramMB:         input.VramMB,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.nodeRepo.Create(ctx, node); err != nil {
		return nil, fmt.Errorf("failed to create node: %w", err)
	}

	return node, nil
}

// UpdateNodePricing updates the price for a node (PROV-03)
func (s *NodeService) UpdateNodePricing(ctx context.Context, nodeID, providerID string, newPrice string) (*domain.Node, error) {
	// Get node and verify ownership
	node, err := s.nodeRepo.GetByID(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("node not found")
	}

	if node.ProviderID != providerID {
		return nil, fmt.Errorf("not authorized to update this node")
	}

	// Validate price is a valid positive number
	price, err := strconv.ParseFloat(newPrice, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid price format")
	}
	if price <= 0 {
		return nil, fmt.Errorf("price must be positive")
	}

	node.PricePerSecond = newPrice
	node.UpdatedAt = time.Now()

	if err := s.nodeRepo.Update(ctx, node); err != nil {
		return nil, fmt.Errorf("failed to update node: %w", err)
	}

	return node, nil
}

// GetProviderNodes returns all nodes for a provider
func (s *NodeService) GetProviderNodes(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return s.nodeRepo.GetByProvider(ctx, providerID)
}

// ListActiveNodes returns all active nodes (for discovery endpoints)
func (s *NodeService) ListActiveNodes(ctx context.Context) ([]*domain.Node, error) {
	return s.nodeRepo.ListActive(ctx)
}

// GetNode returns a single node by ID
func (s *NodeService) GetNode(ctx context.Context, nodeID string) (*domain.Node, error) {
	return s.nodeRepo.GetByID(ctx, nodeID)
}

// AutoRegisterNodeInput contains parameters for mTLS-based auto-registration
type AutoRegisterNodeInput struct {
	NodeID      string // From certificate CN
	GPUType     string // Default or from node metadata
	MemoryGB    int    // Default or from node metadata
	PricePerSec string // Default pricing
	APIEndpoint string // Node's mTLS endpoint for Hub-to-Node communication
}

// AutoRegisterNode registers or updates a node when it connects via mTLS
// This enables worldland-node to auto-register without HTTP API authentication
// If the node was already registered via HTTP API (SIWE auth), this updates it instead
func (s *NodeService) AutoRegisterNode(ctx context.Context, input AutoRegisterNodeInput) (*domain.Node, error) {
	// First, check if provider already exists by wallet address (HTTP registration path)
	// The nodeID from mTLS is the wallet address (certificate CN)
	if s.providerRepo != nil {
		walletAddress := input.NodeID
		existingProvider, err := s.providerRepo.GetByWallet(ctx, walletAddress)
		if err == nil && existingProvider != nil {
			// Provider already registered via HTTP/SIWE - find their nodes and update
			nodes, err := s.nodeRepo.GetByProvider(ctx, existingProvider.ID)
			if err == nil && len(nodes) > 0 {
				// Update all provider's nodes to active status
				for _, node := range nodes {
					node.Status = domain.NodeStatusActive
					node.UpdatedAt = time.Now()
					s.nodeRepo.Update(ctx, node)
				}
				return nodes[0], nil
			}
			// Provider exists but no nodes yet (SIWE registered, node not yet registered via HTTP)
			// Don't create a ghost node - the node will register itself via POST /api/v1/nodes
			return nil, nil
		}
	}

	// Generate deterministic UUID from node ID for consistent lookups
	nodeUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(input.NodeID)).String()

	// Check if node already exists by deterministic UUID
	existingNode, err := s.nodeRepo.GetByID(ctx, nodeUUID)
	if err == nil && existingNode != nil {
		existingNode.Status = domain.NodeStatusActive
		existingNode.APIEndpoint = input.APIEndpoint
		existingNode.UpdatedAt = time.Now()
		if err := s.nodeRepo.Update(ctx, existingNode); err != nil {
			return nil, fmt.Errorf("failed to update existing node: %w", err)
		}
		return existingNode, nil
	}

	// Generate deterministic provider UUID
	providerUUID := nodeUUID

	// Ensure provider exists (required by foreign key constraint)
	if s.providerRepo != nil {
		_, err := s.providerRepo.GetByID(ctx, providerUUID)
		if err != nil {
			mockWalletAddress := input.NodeID
			if !strings.HasPrefix(mockWalletAddress, "0x") {
				mockWalletAddress = fmt.Sprintf("0x%040s", input.NodeID)
				if len(mockWalletAddress) > 42 {
					mockWalletAddress = mockWalletAddress[:42]
				}
			}

			provider := &domain.Provider{
				ID:            providerUUID,
				WalletAddress: mockWalletAddress,
				Status:        domain.ProviderStatusActive,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}
			if err := s.providerRepo.Create(ctx, provider); err != nil {
				// If duplicate wallet, provider was registered via HTTP - look it up
				existing, lookupErr := s.providerRepo.GetByWallet(ctx, mockWalletAddress)
				if lookupErr != nil || existing == nil {
					return nil, fmt.Errorf("failed to create provider for auto-registration: %w", err)
				}
				providerUUID = existing.ID
			}
		}
	}

	// Create new node registration
	now := time.Now()
	node := &domain.Node{
		ID:             nodeUUID,
		ProviderID:     providerUUID,
		GPUUUID:        fmt.Sprintf("GPU-%s", input.NodeID),
		GPUType:        input.GPUType,
		MemoryGB:       input.MemoryGB,
		PricePerSecond: input.PricePerSec,
		APIEndpoint:    input.APIEndpoint,
		Status:         domain.NodeStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.nodeRepo.Create(ctx, node); err != nil {
		return nil, fmt.Errorf("failed to auto-register node: %w", err)
	}

	return node, nil
}

// HeartbeatPayload represents the payload from an SDK heartbeat message
type HeartbeatPayload struct {
	GPUMetrics []struct {
		UUID        string `json:"uuid"`
		Name        string `json:"name"`
		MemoryTotal uint64 `json:"memory_total_mb"`
		GPUUtil     uint32 `json:"gpu_util_percent"`
		Temperature uint32 `json:"temperature_c"`
	} `json:"gpu_metrics"`
	Mode string `json:"mode"` // "master" or "worker"
}

// ProcessHeartbeat updates node status and metrics from an SDK heartbeat
func (s *NodeService) ProcessHeartbeat(ctx context.Context, nodeID string, payload HeartbeatPayload) {
	// Look up all nodes for this provider (nodeID is wallet address from mTLS CN)
	if s.providerRepo == nil {
		return
	}

	provider, err := s.providerRepo.GetByWallet(ctx, nodeID)
	if err != nil || provider == nil {
		return
	}

	nodes, err := s.nodeRepo.GetByProvider(ctx, provider.ID)
	if err != nil || len(nodes) == 0 {
		return
	}

	// Extract GPU model from heartbeat metrics
	var hbGPUModel string
	var hbVramMB int
	if len(payload.GPUMetrics) > 0 {
		hbGPUModel = payload.GPUMetrics[0].Name
		hbVramMB = int(payload.GPUMetrics[0].MemoryTotal)
	}

	// Update heartbeat timestamp for all provider nodes
	now := time.Now()
	for _, node := range nodes {
		if node.Status != domain.NodeStatusActive {
			node.Status = domain.NodeStatusActive
		}
		node.UpdatedAt = now

		// Update GPU model/VRAM if heartbeat has better data
		if hbGPUModel != "" && (node.GPUModel == "" || strings.HasPrefix(node.GPUModel, "GPU x")) {
			node.GPUModel = hbGPUModel
			if hbVramMB > 0 {
				node.VramMB = hbVramMB
			}
		}

		s.nodeRepo.Update(ctx, node)
	}
}

// MarkNodeOffline marks a node as offline when it disconnects
// nodeID can be a direct node ID or a wallet address (from mTLS CN)
func (s *NodeService) MarkNodeOffline(ctx context.Context, nodeID string) error {
	// Try direct lookup first
	node, err := s.nodeRepo.GetByID(ctx, nodeID)
	if err != nil {
		// nodeID might be a wallet address - look up provider's nodes
		if s.providerRepo != nil {
			provider, err := s.providerRepo.GetByWallet(ctx, nodeID)
			if err == nil && provider != nil {
				nodes, err := s.nodeRepo.GetByProvider(ctx, provider.ID)
				if err == nil {
					for _, n := range nodes {
						n.Status = domain.NodeStatusOffline
						n.UpdatedAt = time.Now()
						s.nodeRepo.Update(ctx, n)
					}
				}
			}
		}
		return nil
	}

	node.Status = domain.NodeStatusOffline
	node.UpdatedAt = time.Now()
	return s.nodeRepo.Update(ctx, node)
}
