package mining

import (
	"fmt"
	"sync"
)

// GPUAllocation tracks how GPUs are allocated for a provider
type GPUAllocation struct {
	Total     int `json:"total"`
	Mining    int `json:"mining"`
	Rental    int `json:"rental"`
	Available int `json:"available"`
}

// GPUPool manages GPU allocation between mining and rental for a provider
type GPUPool struct {
	allocations map[string]*GPUAllocation // providerID -> allocation
	mu          sync.RWMutex
}

// NewGPUPool creates a new GPU pool manager
func NewGPUPool() *GPUPool {
	return &GPUPool{
		allocations: make(map[string]*GPUAllocation),
	}
}

// SetTotal sets the total GPU count for a provider
func (p *GPUPool) SetTotal(providerID string, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		alloc = &GPUAllocation{}
		p.allocations[providerID] = alloc
	}
	alloc.Total = total
	alloc.Available = total - alloc.Mining - alloc.Rental
}

// GetAllocation returns the current GPU allocation for a provider
func (p *GPUPool) GetAllocation(providerID string) *GPUAllocation {
	p.mu.RLock()
	defer p.mu.RUnlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		return &GPUAllocation{}
	}
	return &GPUAllocation{
		Total:     alloc.Total,
		Mining:    alloc.Mining,
		Rental:    alloc.Rental,
		Available: alloc.Available,
	}
}

// AllocateMining allocates GPUs for mining
func (p *GPUPool) AllocateMining(providerID string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		return fmt.Errorf("provider %s not registered in GPU pool", providerID)
	}

	if count > alloc.Available {
		return fmt.Errorf("insufficient GPUs: requested %d, available %d", count, alloc.Available)
	}

	alloc.Mining += count
	alloc.Available -= count
	return nil
}

// ReleaseMining releases GPUs from mining back to available
func (p *GPUPool) ReleaseMining(providerID string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		return fmt.Errorf("provider %s not registered in GPU pool", providerID)
	}

	if count > alloc.Mining {
		count = alloc.Mining
	}

	alloc.Mining -= count
	alloc.Available += count
	return nil
}

// AllocateRental allocates GPUs for rental
func (p *GPUPool) AllocateRental(providerID string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		return fmt.Errorf("provider %s not registered in GPU pool", providerID)
	}

	if count > alloc.Available {
		return fmt.Errorf("insufficient GPUs: requested %d, available %d", count, alloc.Available)
	}

	alloc.Rental += count
	alloc.Available -= count
	return nil
}

// ReleaseRental releases GPUs from rental back to available
func (p *GPUPool) ReleaseRental(providerID string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[providerID]
	if !ok {
		return fmt.Errorf("provider %s not registered in GPU pool", providerID)
	}

	if count > alloc.Rental {
		count = alloc.Rental
	}

	alloc.Rental -= count
	alloc.Available += count
	return nil
}
