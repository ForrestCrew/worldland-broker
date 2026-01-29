package matching

import (
	"context"

	"github.com/worldland/worldland-hub/internal/domain"
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
	// TODO: Implement in GREEN phase
	return &MatchResult{}, nil
}
