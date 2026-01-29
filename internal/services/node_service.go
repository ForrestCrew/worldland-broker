package services

import (
	"context"
	"fmt"
	"strconv"
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
	nodeRepo domain.NodeRepository
}

// NewNodeService creates a new node service
func NewNodeService(nodeRepo domain.NodeRepository) *NodeService {
	return &NodeService{nodeRepo: nodeRepo}
}

// RegisterNodeInput contains node registration parameters
type RegisterNodeInput struct {
	ProviderID   string
	GPUUUID      string
	GPUType      string
	MemoryGB     int
	PricePerSec  string // Decimal string for wei precision
}

// RegisterNode creates a new node registration for a provider
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

	now := time.Now()
	node := &domain.Node{
		ID:             uuid.New().String(),
		ProviderID:     input.ProviderID,
		GPUUUID:        input.GPUUUID,
		GPUType:        input.GPUType,
		MemoryGB:       input.MemoryGB,
		PricePerSecond: input.PricePerSec,
		Status:         domain.NodeStatusPending,
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

// GetNode returns a single node by ID
func (s *NodeService) GetNode(ctx context.Context, nodeID string) (*domain.Node, error) {
	return s.nodeRepo.GetByID(ctx, nodeID)
}
