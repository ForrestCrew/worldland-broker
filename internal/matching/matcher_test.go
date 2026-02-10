package matching

import (
	"context"
	"fmt"
	"testing"

	"github.com/worldland/worldland-hub/internal/domain"
)

// MockNodeLister implements NodeLister for testing
type MockNodeLister struct {
	nodes []*domain.Node
}

func (m *MockNodeLister) ListActive(ctx context.Context) ([]*domain.Node, error) {
	var active []*domain.Node
	for _, n := range m.nodes {
		if n.Status == domain.NodeStatusActive {
			active = append(active, n)
		}
	}
	return active, nil
}

// Helper to create test nodes
func createTestNode(id, gpuType string, memoryGB int, pricePerSecond string, status domain.NodeStatus) *domain.Node {
	return &domain.Node{
		ID:             id,
		ProviderID:     "provider-" + id,
		GPUType:        gpuType,
		MemoryGB:       memoryGB,
		PricePerSecond: pricePerSecond,
		Status:         status,
		TotalGPUs:      1,
		AvailableGPUs:  1,
	}
}

func TestFindProviders_ExactGPUMatch_SortedByPrice(t *testing.T) {
	// Setup: 3 nodes with RTX 4090 at different prices
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4090", 24, "300", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 4090", 24, "100", domain.NodeStatusActive),
			createTestNode("node-3", "RTX 4090", 24, "200", domain.NodeStatusActive),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: returned in price ascending order
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(result.Nodes))
	}

	// Verify price ordering: 100, 200, 300
	expectedPrices := []string{"100", "200", "300"}
	for i, node := range result.Nodes {
		if node.PricePerSecond != expectedPrices[i] {
			t.Errorf("node[%d] expected price %s, got %s", i, expectedPrices[i], node.PricePerSecond)
		}
	}

	if result.TotalCount != 3 {
		t.Errorf("expected TotalCount 3, got %d", result.TotalCount)
	}
}

func TestFindProviders_FilterByMinMemory(t *testing.T) {
	// Setup: nodes with 8GB, 16GB, 24GB
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4090", 8, "100", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 4090", 16, "200", domain.NodeStatusActive),
			createTestNode("node-3", "RTX 4090", 24, "300", domain.NodeStatusActive),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType:     "RTX 4090",
		MinMemoryGB: 16,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: only 16GB and 24GB nodes
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}

	for _, node := range result.Nodes {
		if node.MemoryGB < 16 {
			t.Errorf("expected MemoryGB >= 16, got %d", node.MemoryGB)
		}
	}
}

func TestFindProviders_FilterByMaxPrice(t *testing.T) {
	// Setup: nodes at 100, 200, 300 wei/s
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4090", 24, "100", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 4090", 24, "200", domain.NodeStatusActive),
			createTestNode("node-3", "RTX 4090", 24, "300", domain.NodeStatusActive),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType:           "RTX 4090",
		MaxPricePerSecond: "200",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: only 100, 200 nodes
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}

	for _, node := range result.Nodes {
		// Prices should be <= 200
		if node.PricePerSecond == "300" {
			t.Error("expected node with price 300 to be filtered out")
		}
	}
}

func TestFindProviders_ExcludesOfflineNodes(t *testing.T) {
	// Setup: active and offline nodes
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4090", 24, "100", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 4090", 24, "200", domain.NodeStatusOffline),
			createTestNode("node-3", "RTX 4090", 24, "300", domain.NodeStatusPending),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: only active nodes returned
	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(result.Nodes))
	}

	if result.Nodes[0].Status != domain.NodeStatusActive {
		t.Errorf("expected active status, got %s", result.Nodes[0].Status)
	}
}

func TestFindProviders_NoMatch_ReturnsSimilarRecommendations(t *testing.T) {
	// Setup: RTX 4080 and RTX 3090 nodes, but request RTX 4090
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4080", 16, "150", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 3090", 24, "120", domain.NodeStatusActive),
			createTestNode("node-3", "A100", 80, "500", domain.NodeStatusActive),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: empty Nodes (no exact match)
	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 nodes for exact match, got %d", len(result.Nodes))
	}

	// Expect: Recommendations includes similar GPUs (RTX 4080 is same generation tier)
	if len(result.Recommendations) == 0 {
		t.Error("expected recommendations when no exact match found")
	}

	// RTX 4080 should be in recommendations (same generation as RTX 4090)
	foundRTX4080 := false
	for _, node := range result.Recommendations {
		if node.GPUType == "RTX 4080" {
			foundRTX4080 = true
			break
		}
	}
	if !foundRTX4080 {
		t.Error("expected RTX 4080 in recommendations for RTX 4090 request")
	}
}

func TestFindProviders_Pagination(t *testing.T) {
	// Setup: 10 matching nodes
	mock := &MockNodeLister{
		nodes: make([]*domain.Node, 10),
	}
	for i := 0; i < 10; i++ {
		mock.nodes[i] = createTestNode(
			"node-"+string(rune('0'+i)),
			"RTX 4090",
			24,
			string(rune('0'+i))+"00", // prices: 000, 100, 200, ...
			domain.NodeStatusActive,
		)
	}
	// Fix prices to be proper numeric strings
	for i := 0; i < 10; i++ {
		mock.nodes[i].PricePerSecond = fmt.Sprintf("%d", (i+1)*100)
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
		Limit:   3,
		Offset:  3,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: nodes 4, 5, 6 returned (0-indexed: 3, 4, 5)
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes with pagination, got %d", len(result.Nodes))
	}

	// TotalCount should reflect total matching count (before pagination)
	if result.TotalCount != 10 {
		t.Errorf("expected TotalCount 10, got %d", result.TotalCount)
	}
}

func TestFindProviders_SortByMemory(t *testing.T) {
	// Setup: nodes with different memory sizes
	mock := &MockNodeLister{
		nodes: []*domain.Node{
			createTestNode("node-1", "RTX 4090", 8, "100", domain.NodeStatusActive),
			createTestNode("node-2", "RTX 4090", 24, "200", domain.NodeStatusActive),
			createTestNode("node-3", "RTX 4090", 16, "150", domain.NodeStatusActive),
		},
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
		SortBy:  "memory",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: sorted by memory descending (highest first for memory)
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(result.Nodes))
	}

	expectedMemory := []int{24, 16, 8}
	for i, node := range result.Nodes {
		if node.MemoryGB != expectedMemory[i] {
			t.Errorf("node[%d] expected memory %d, got %d", i, expectedMemory[i], node.MemoryGB)
		}
	}
}

func TestFindProviders_DefaultLimit(t *testing.T) {
	// Setup: 30 matching nodes
	mock := &MockNodeLister{
		nodes: make([]*domain.Node, 30),
	}
	for i := 0; i < 30; i++ {
		mock.nodes[i] = createTestNode(
			fmt.Sprintf("node-%d", i),
			"RTX 4090",
			24,
			fmt.Sprintf("%d", (i+1)*10),
			domain.NodeStatusActive,
		)
	}

	matcher := NewProviderMatcher(mock)
	result, err := matcher.FindProviders(context.Background(), MatchRequest{
		GPUType: "RTX 4090",
		// No limit specified - should default to 20
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: default limit of 20
	if len(result.Nodes) != 20 {
		t.Errorf("expected default limit 20 nodes, got %d", len(result.Nodes))
	}

	if result.TotalCount != 30 {
		t.Errorf("expected TotalCount 30, got %d", result.TotalCount)
	}
}
