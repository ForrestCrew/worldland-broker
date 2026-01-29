package matching

import (
	"context"
	"math/big"
	"sort"
	"strings"

	"github.com/worldland/worldland-hub/internal/domain"
)

// Default values
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Sort options
const (
	SortByPrice  = "price"
	SortByMemory = "memory"
)

// NodeLister defines the interface for listing nodes (dependency injection)
type NodeLister interface {
	ListActive(ctx context.Context) ([]*domain.Node, error)
}

// MatchRequest represents a request to find matching providers
type MatchRequest struct {
	GPUType           string // Required - GPU type to match (e.g., "RTX 4090")
	MinMemoryGB       int    // Optional - minimum VRAM requirement
	MaxPricePerSecond string // Optional - maximum price filter
	SortBy            string // "price" (default), "memory"
	Limit             int    // Max results (default 20)
	Offset            int    // Pagination offset
}

// MatchResult represents the result of a provider match query
type MatchResult struct {
	Nodes           []*domain.Node // Matching nodes
	TotalCount      int            // Total matching (for pagination)
	Recommendations []*domain.Node // Similar GPUs if no exact match
}

// ProviderMatcher provides user-driven provider selection with filtering and sorting
type ProviderMatcher struct {
	lister NodeLister
}

// NewProviderMatcher creates a new ProviderMatcher with the given node lister
func NewProviderMatcher(lister NodeLister) *ProviderMatcher {
	return &ProviderMatcher{lister: lister}
}

// FindProviders finds matching providers based on the request criteria
func (m *ProviderMatcher) FindProviders(ctx context.Context, req MatchRequest) (*MatchResult, error) {
	// Get all active nodes
	nodes, err := m.lister.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by GPU type (exact match)
	var matched []*domain.Node
	for _, node := range nodes {
		if node.GPUType == req.GPUType {
			matched = append(matched, node)
		}
	}

	// Apply additional filters
	matched = m.applyFilters(matched, req)

	// Sort results
	m.sortNodes(matched, req.SortBy)

	// Store total count before pagination
	totalCount := len(matched)

	// Apply pagination
	paginatedNodes := paginate(matched, req.Limit, req.Offset)

	result := &MatchResult{
		Nodes:      paginatedNodes,
		TotalCount: totalCount,
	}

	// Generate recommendations if no exact match
	if len(matched) == 0 {
		result.Recommendations = m.getSimilarGPUs(nodes, req.GPUType)
	}

	return result, nil
}

// paginate applies limit and offset to a slice of nodes
func paginate(nodes []*domain.Node, limit, offset int) []*domain.Node {
	// Apply limit defaults
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	// Apply offset defaults
	if offset < 0 {
		offset = 0
	}

	// Return empty if offset exceeds length
	if offset >= len(nodes) {
		return nil
	}

	// Calculate end index
	end := offset + limit
	if end > len(nodes) {
		end = len(nodes)
	}

	return nodes[offset:end]
}

// applyFilters applies MinMemoryGB and MaxPricePerSecond filters
func (m *ProviderMatcher) applyFilters(nodes []*domain.Node, req MatchRequest) []*domain.Node {
	var filtered []*domain.Node

	var maxPrice *big.Int
	if req.MaxPricePerSecond != "" {
		maxPrice = new(big.Int)
		maxPrice.SetString(req.MaxPricePerSecond, 10)
	}

	for _, node := range nodes {
		// Filter by minimum memory
		if req.MinMemoryGB > 0 && node.MemoryGB < req.MinMemoryGB {
			continue
		}

		// Filter by maximum price
		if maxPrice != nil {
			nodePrice := new(big.Int)
			nodePrice.SetString(node.PricePerSecond, 10)
			if nodePrice.Cmp(maxPrice) > 0 {
				continue
			}
		}

		filtered = append(filtered, node)
	}

	return filtered
}

// sortNodes sorts nodes by the specified criteria
func (m *ProviderMatcher) sortNodes(nodes []*domain.Node, sortBy string) {
	switch sortBy {
	case SortByMemory:
		// Sort by memory descending (highest memory first)
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].MemoryGB > nodes[j].MemoryGB
		})
	default:
		// Default: sort by price ascending (lowest price first)
		sort.Slice(nodes, func(i, j int) bool {
			priceI := new(big.Int)
			priceJ := new(big.Int)
			priceI.SetString(nodes[i].PricePerSecond, 10)
			priceJ.SetString(nodes[j].PricePerSecond, 10)
			return priceI.Cmp(priceJ) < 0
		})
	}
}

// getSimilarGPUs returns nodes with similar GPU types when no exact match found
func (m *ProviderMatcher) getSimilarGPUs(nodes []*domain.Node, requestedGPU string) []*domain.Node {
	vendor := getGPUVendor(requestedGPU)
	family := getGPUFamily(requestedGPU)

	var recommendations []*domain.Node
	seenTypes := make(map[string]bool)

	// First pass: same vendor and same family
	for _, node := range nodes {
		if seenTypes[node.GPUType] {
			continue
		}
		nodeVendor := getGPUVendor(node.GPUType)
		nodeFamily := getGPUFamily(node.GPUType)

		if nodeVendor == vendor && nodeFamily == family {
			recommendations = append(recommendations, node)
			seenTypes[node.GPUType] = true
		}
	}

	// Second pass: same vendor, different family (if not enough recommendations)
	if len(recommendations) < 3 {
		for _, node := range nodes {
			if seenTypes[node.GPUType] {
				continue
			}
			nodeVendor := getGPUVendor(node.GPUType)

			if nodeVendor == vendor {
				recommendations = append(recommendations, node)
				seenTypes[node.GPUType] = true
				if len(recommendations) >= 3 {
					break
				}
			}
		}
	}

	// Limit to 3 recommendations
	if len(recommendations) > 3 {
		recommendations = recommendations[:3]
	}

	return recommendations
}

// getGPUVendor extracts vendor from GPU type string
func getGPUVendor(gpuType string) string {
	upper := strings.ToUpper(gpuType)
	switch {
	case strings.Contains(upper, "RTX"), strings.Contains(upper, "GTX"),
		strings.Contains(upper, "A100"), strings.Contains(upper, "H100"),
		strings.Contains(upper, "V100"), strings.Contains(upper, "TITAN"):
		return "NVIDIA"
	case strings.Contains(upper, "RX"), strings.Contains(upper, "MI"):
		return "AMD"
	default:
		return "UNKNOWN"
	}
}

// getGPUFamily extracts generation family from GPU type string
func getGPUFamily(gpuType string) string {
	upper := strings.ToUpper(gpuType)
	switch {
	// NVIDIA RTX 40 series
	case strings.Contains(upper, "RTX 40"), strings.Contains(upper, "RTX40"):
		return "RTX40"
	// NVIDIA RTX 30 series
	case strings.Contains(upper, "RTX 30"), strings.Contains(upper, "RTX30"):
		return "RTX30"
	// NVIDIA RTX 20 series
	case strings.Contains(upper, "RTX 20"), strings.Contains(upper, "RTX20"):
		return "RTX20"
	// NVIDIA datacenter A-series
	case strings.Contains(upper, "A100"):
		return "A-SERIES"
	// NVIDIA datacenter H-series
	case strings.Contains(upper, "H100"):
		return "H-SERIES"
	// AMD MI series
	case strings.Contains(upper, "MI"):
		return "MI-SERIES"
	default:
		return "OTHER"
	}
}
