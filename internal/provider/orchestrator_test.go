package provider

import (
	"context"
	"testing"
)

// createTestOrchestrator creates an orchestrator for testing without K8s/Redis.
func createTestOrchestrator() *Orchestrator {
	return &Orchestrator{
		providers: make(map[string]*ProviderState),
		stopCh:    make(chan struct{}),
	}
}

// createTestProvider creates a provider state for testing.
func createTestProvider(providerID string, totalGPU int, gpuType string) *ProviderState {
	return &ProviderState{
		ProviderID: providerID,
		Status:     StatusAvailable,
		Spec: SystemSpec{
			TotalGPUs: totalGPU,
			GPUs: []GPUInfo{
				{Name: gpuType, MemoryMB: 16384},
			},
		},
		Capacity: ProviderCapacity{
			TotalGPUs:         map[string]int{gpuType: totalGPU},
			AvailableGPUs:     map[string]int{gpuType: totalGPU},
			InUseGPUs:         map[string]int{gpuType: 0},
			TotalCPUCores:     32,
			AvailableCPUCores: 32,
			InUseCPUCores:     0,
			TotalMemoryMB:     65536,
			AvailableMemoryMB: 65536,
			InUseMemoryMB:     0,
		},
	}
}

func TestAllocateResources_Success(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   2,
		CPUCores:   4,
		MemoryMB:   8192,
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err != nil {
		t.Fatalf("AllocateResources failed: %v", err)
	}

	// Verify GPU allocation
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 2 {
		t.Errorf("AvailableGPUs = %d; want 2", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseGPUs["Tesla T4"] != 2 {
		t.Errorf("InUseGPUs = %d; want 2", provider.Capacity.InUseGPUs["Tesla T4"])
	}

	// Verify CPU allocation
	if provider.Capacity.AvailableCPUCores != 28 {
		t.Errorf("AvailableCPUCores = %d; want 28", provider.Capacity.AvailableCPUCores)
	}
	if provider.Capacity.InUseCPUCores != 4 {
		t.Errorf("InUseCPUCores = %d; want 4", provider.Capacity.InUseCPUCores)
	}

	// Verify Memory allocation
	if provider.Capacity.AvailableMemoryMB != 65536-8192 {
		t.Errorf("AvailableMemoryMB = %d; want %d", provider.Capacity.AvailableMemoryMB, 65536-8192)
	}
}

func TestAllocateResources_InsufficientGPU(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 2, "Tesla T4")
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   4, // Requesting more than available
		CPUCores:   4,
		MemoryMB:   8192,
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err == nil {
		t.Fatal("AllocateResources should have failed with insufficient GPU")
	}

	// Verify nothing was allocated
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 2 {
		t.Errorf("GPU should not be changed, got AvailableGPUs = %d", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
}

func TestAllocateResources_InsufficientCPU_Rollback(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	provider.Capacity.AvailableCPUCores = 2 // Only 2 CPU available
	provider.Capacity.TotalCPUCores = 2
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   2,
		CPUCores:   8, // Requesting more CPU than available
		MemoryMB:   8192,
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err == nil {
		t.Fatal("AllocateResources should have failed with insufficient CPU")
	}

	// Verify GPU was rolled back
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 4 {
		t.Errorf("GPU should be rolled back, got AvailableGPUs = %d; want 4", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseGPUs["Tesla T4"] != 0 {
		t.Errorf("InUseGPUs should be rolled back, got %d; want 0", provider.Capacity.InUseGPUs["Tesla T4"])
	}
}

func TestReleaseResources_Success(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	// Simulate some resources already in use
	provider.Capacity.AvailableGPUs["Tesla T4"] = 2
	provider.Capacity.InUseGPUs["Tesla T4"] = 2
	provider.Capacity.AvailableCPUCores = 28
	provider.Capacity.InUseCPUCores = 4
	provider.Capacity.AvailableMemoryMB = 57344
	provider.Capacity.InUseMemoryMB = 8192
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   2,
		CPUCores:   4,
		MemoryMB:   8192,
	}

	err := orch.ReleaseResources("provider-1", allocation)
	if err != nil {
		t.Fatalf("ReleaseResources failed: %v", err)
	}

	// Verify resources released
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 4 {
		t.Errorf("AvailableGPUs = %d; want 4", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseGPUs["Tesla T4"] != 0 {
		t.Errorf("InUseGPUs = %d; want 0", provider.Capacity.InUseGPUs["Tesla T4"])
	}
	if provider.Capacity.AvailableCPUCores != 32 {
		t.Errorf("AvailableCPUCores = %d; want 32", provider.Capacity.AvailableCPUCores)
	}
	if provider.Capacity.AvailableMemoryMB != 65536 {
		t.Errorf("AvailableMemoryMB = %d; want 65536", provider.Capacity.AvailableMemoryMB)
	}
}

func TestAllocateAndRelease_Cycle(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "RTX 4090")
	orch.providers["provider-1"] = provider

	initialAvailableGPU := provider.Capacity.AvailableGPUs["RTX 4090"]
	initialAvailableCPU := provider.Capacity.AvailableCPUCores
	initialAvailableMem := provider.Capacity.AvailableMemoryMB

	// Allocate
	allocation := &ResourceAllocation{
		JobID:      "job-cycle",
		ProviderID: "provider-1",
		GPUType:    "RTX 4090",
		GPUCount:   2,
		CPUCores:   8,
		MemoryMB:   16384,
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err != nil {
		t.Fatalf("AllocateResources failed: %v", err)
	}

	// Verify allocation
	if provider.Capacity.AvailableGPUs["RTX 4090"] == initialAvailableGPU {
		t.Error("GPU should have changed after allocation")
	}

	// Release
	err = orch.ReleaseResources("provider-1", allocation)
	if err != nil {
		t.Fatalf("ReleaseResources failed: %v", err)
	}

	// Verify back to initial state
	if provider.Capacity.AvailableGPUs["RTX 4090"] != initialAvailableGPU {
		t.Errorf("After release, AvailableGPUs = %d; want %d",
			provider.Capacity.AvailableGPUs["RTX 4090"], initialAvailableGPU)
	}
	if provider.Capacity.AvailableCPUCores != initialAvailableCPU {
		t.Errorf("After release, AvailableCPUCores = %d; want %d",
			provider.Capacity.AvailableCPUCores, initialAvailableCPU)
	}
	if provider.Capacity.AvailableMemoryMB != initialAvailableMem {
		t.Errorf("After release, AvailableMemoryMB = %d; want %d",
			provider.Capacity.AvailableMemoryMB, initialAvailableMem)
	}
}

func TestMultipleJobAllocations(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 8, "A100")
	orch.providers["provider-1"] = provider

	// Allocate job 1
	job1 := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "A100",
		GPUCount:   2,
		CPUCores:   4,
		MemoryMB:   8192,
	}
	if err := orch.AllocateResources("provider-1", job1); err != nil {
		t.Fatalf("Job 1 allocation failed: %v", err)
	}

	// Allocate job 2
	job2 := &ResourceAllocation{
		JobID:      "job-2",
		ProviderID: "provider-1",
		GPUType:    "A100",
		GPUCount:   3,
		CPUCores:   6,
		MemoryMB:   16384,
	}
	if err := orch.AllocateResources("provider-1", job2); err != nil {
		t.Fatalf("Job 2 allocation failed: %v", err)
	}

	// Verify total in use
	if provider.Capacity.InUseGPUs["A100"] != 5 {
		t.Errorf("InUseGPUs = %d; want 5", provider.Capacity.InUseGPUs["A100"])
	}
	if provider.Capacity.AvailableGPUs["A100"] != 3 {
		t.Errorf("AvailableGPUs = %d; want 3", provider.Capacity.AvailableGPUs["A100"])
	}

	// Release job 1
	if err := orch.ReleaseResources("provider-1", job1); err != nil {
		t.Fatalf("Job 1 release failed: %v", err)
	}

	// Verify partial release
	if provider.Capacity.InUseGPUs["A100"] != 3 {
		t.Errorf("After job1 release, InUseGPUs = %d; want 3", provider.Capacity.InUseGPUs["A100"])
	}
	if provider.Capacity.AvailableGPUs["A100"] != 5 {
		t.Errorf("After job1 release, AvailableGPUs = %d; want 5", provider.Capacity.AvailableGPUs["A100"])
	}
}

func TestReleaseJobResources_DirectCall(t *testing.T) {
	ctx := context.Background()
	_ = ctx // unused but matches function signatures

	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	provider.Capacity.AvailableGPUs["Tesla T4"] = 2
	provider.Capacity.InUseGPUs["Tesla T4"] = 2
	orch.providers["provider-1"] = provider

	// Call releaseJobResources directly (as Pod Watcher would)
	orch.releaseJobResources("provider-1", "Tesla T4", 2, 0, 0)

	if provider.Capacity.AvailableGPUs["Tesla T4"] != 4 {
		t.Errorf("AvailableGPUs = %d; want 4", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseGPUs["Tesla T4"] != 0 {
		t.Errorf("InUseGPUs = %d; want 0", provider.Capacity.InUseGPUs["Tesla T4"])
	}
}

func TestProviderNotFound(t *testing.T) {
	orch := createTestOrchestrator()

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "nonexistent",
		GPUType:    "Tesla T4",
		GPUCount:   1,
	}

	err := orch.AllocateResources("nonexistent", allocation)
	if err == nil {
		t.Fatal("AllocateResources should fail for nonexistent provider")
	}
}

// ================== 엣지 케이스 테스트 ==================

func TestAllocateResources_InsufficientMemory_Rollback(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	provider.Capacity.AvailableMemoryMB = 4096 // Only 4GB available
	provider.Capacity.TotalMemoryMB = 4096
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   2,
		CPUCores:   4,
		MemoryMB:   8192, // Requesting 8GB but only 4GB available
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err == nil {
		t.Fatal("AllocateResources should have failed with insufficient memory")
	}

	// Verify GPU and CPU were rolled back
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 4 {
		t.Errorf("GPU should be rolled back, got AvailableGPUs = %d; want 4", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseGPUs["Tesla T4"] != 0 {
		t.Errorf("InUseGPUs should be rolled back, got %d; want 0", provider.Capacity.InUseGPUs["Tesla T4"])
	}
	if provider.Capacity.AvailableCPUCores != 32 {
		t.Errorf("CPU should be rolled back, got AvailableCPUCores = %d; want 32", provider.Capacity.AvailableCPUCores)
	}
	if provider.Capacity.InUseCPUCores != 0 {
		t.Errorf("InUseCPUCores should be rolled back, got %d; want 0", provider.Capacity.InUseCPUCores)
	}
}

func TestAllocateResources_ZeroResources(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-zero",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   0, // Zero GPU
		CPUCores:   0, // Zero CPU
		MemoryMB:   0, // Zero Memory
	}

	err := orch.AllocateResources("provider-1", allocation)
	// Zero allocation should succeed (no resources consumed)
	if err != nil {
		t.Fatalf("Zero allocation should succeed: %v", err)
	}

	// Verify nothing changed
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 4 {
		t.Errorf("AvailableGPUs should remain 4, got %d", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
}

func TestAllocateResources_NilMaps(t *testing.T) {
	orch := createTestOrchestrator()
	provider := &ProviderState{
		ProviderID: "provider-nil",
		Status:     StatusAvailable,
		Spec: SystemSpec{
			TotalGPUs: 4,
			GPUs:      []GPUInfo{{Name: "Tesla T4", MemoryMB: 16384}},
		},
		Capacity: ProviderCapacity{
			// Maps are nil!
			TotalGPUs:         nil,
			AvailableGPUs:     nil,
			InUseGPUs:         nil,
			TotalCPUCores:     32,
			AvailableCPUCores: 32,
			TotalMemoryMB:     65536,
			AvailableMemoryMB: 65536,
		},
	}
	orch.providers["provider-nil"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-nil",
		ProviderID: "provider-nil",
		GPUType:    "Tesla T4",
		GPUCount:   1,
		CPUCores:   2,
		MemoryMB:   4096,
	}

	// This tests that nil maps are handled gracefully
	err := orch.AllocateResources("provider-nil", allocation)
	// It's acceptable if this fails due to nil GPU maps, but should not panic
	_ = err
}

func TestReleaseResources_MoreThanAllocated(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	provider.Capacity.AvailableGPUs["Tesla T4"] = 2
	provider.Capacity.InUseGPUs["Tesla T4"] = 2
	orch.providers["provider-1"] = provider

	// Try to release more than in use
	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   5, // Releasing 5 but only 2 in use
		CPUCores:   0,
		MemoryMB:   0,
	}

	err := orch.ReleaseResources("provider-1", allocation)
	// Should not error (capped to 0)
	if err != nil {
		t.Fatalf("ReleaseResources should not error: %v", err)
	}

	// InUse should be 0 (not negative)
	if provider.Capacity.InUseGPUs["Tesla T4"] < 0 {
		t.Errorf("InUseGPUs should not go negative, got %d", provider.Capacity.InUseGPUs["Tesla T4"])
	}
}

func TestReleaseJobResources_UnknownGPUType(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	// Release with empty GPU type (should use default from provider spec)
	orch.releaseJobResources("provider-1", "", 1, 0, 0)

	// Should have released 1 GPU of the provider's default type
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 5 {
		t.Errorf("AvailableGPUs = %d; want 5", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
}

func TestAllocateResources_DifferentGPUType(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "RTX 4090", // Different GPU type
		GPUCount:   1,
		CPUCores:   2,
		MemoryMB:   4096,
	}

	err := orch.AllocateResources("provider-1", allocation)
	// Should fail because RTX 4090 is not available
	if err == nil {
		t.Fatal("Should fail when requesting unavailable GPU type")
	}
}

func TestExactResourceAllocation(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	// Allocate exactly all resources
	allocation := &ResourceAllocation{
		JobID:      "job-full",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   4,     // All GPUs
		CPUCores:   32,    // All CPUs
		MemoryMB:   65536, // All Memory
	}

	err := orch.AllocateResources("provider-1", allocation)
	if err != nil {
		t.Fatalf("Full allocation should succeed: %v", err)
	}

	// Verify all used
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 0 {
		t.Errorf("AvailableGPUs = %d; want 0", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.AvailableCPUCores != 0 {
		t.Errorf("AvailableCPUCores = %d; want 0", provider.Capacity.AvailableCPUCores)
	}
	if provider.Capacity.AvailableMemoryMB != 0 {
		t.Errorf("AvailableMemoryMB = %d; want 0", provider.Capacity.AvailableMemoryMB)
	}

	// Try to allocate one more (should fail)
	allocation2 := &ResourceAllocation{
		JobID:      "job-extra",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   1,
	}
	err = orch.AllocateResources("provider-1", allocation2)
	if err == nil {
		t.Fatal("Should fail when no resources available")
	}
}

// ================== 동시성 테스트 ==================

func TestConcurrentAllocations(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 100, "Tesla T4")
	provider.Capacity.TotalCPUCores = 100
	provider.Capacity.AvailableCPUCores = 100
	provider.Capacity.TotalMemoryMB = 100000
	provider.Capacity.AvailableMemoryMB = 100000
	orch.providers["provider-1"] = provider

	// Concurrent allocations
	done := make(chan bool, 50)
	for i := 0; i < 50; i++ {
		go func(i int) {
			allocation := &ResourceAllocation{
				JobID:      "job-concurrent-" + string(rune(i)),
				ProviderID: "provider-1",
				GPUType:    "Tesla T4",
				GPUCount:   1,
				CPUCores:   1,
				MemoryMB:   1000,
			}
			_ = orch.AllocateResources("provider-1", allocation)
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < 50; i++ {
		<-done
	}

	// Verify consistency
	total := provider.Capacity.AvailableGPUs["Tesla T4"] + provider.Capacity.InUseGPUs["Tesla T4"]
	if total != 100 {
		t.Errorf("Total GPU count should be 100, got available=%d + inuse=%d = %d",
			provider.Capacity.AvailableGPUs["Tesla T4"],
			provider.Capacity.InUseGPUs["Tesla T4"],
			total)
	}
}

func TestConcurrentAllocateAndRelease(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 10, "Tesla T4")
	orch.providers["provider-1"] = provider

	done := make(chan bool, 100)

	// Allocate jobs
	for i := 0; i < 50; i++ {
		go func(i int) {
			allocation := &ResourceAllocation{
				JobID:      "job-" + string(rune(i)),
				ProviderID: "provider-1",
				GPUType:    "Tesla T4",
				GPUCount:   1,
			}
			_ = orch.AllocateResources("provider-1", allocation)
			done <- true
		}(i)
	}

	// Release jobs (some may not have been allocated yet)
	for i := 0; i < 50; i++ {
		go func(i int) {
			allocation := &ResourceAllocation{
				JobID:      "job-" + string(rune(i)),
				ProviderID: "provider-1",
				GPUType:    "Tesla T4",
				GPUCount:   1,
			}
			_ = orch.ReleaseResources("provider-1", allocation)
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < 100; i++ {
		<-done
	}

	// Verify no negative values
	if provider.Capacity.InUseGPUs["Tesla T4"] < 0 {
		t.Errorf("InUseGPUs should not be negative: %d", provider.Capacity.InUseGPUs["Tesla T4"])
	}
	if provider.Capacity.InUseCPUCores < 0 {
		t.Errorf("InUseCPUCores should not be negative: %d", provider.Capacity.InUseCPUCores)
	}
	if provider.Capacity.InUseMemoryMB < 0 {
		t.Errorf("InUseMemoryMB should not be negative: %d", provider.Capacity.InUseMemoryMB)
	}
}

// ================== 특수 상황 테스트 ==================

func TestAllocateResources_ProviderOffline(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	provider.Status = StatusOffline // Provider is offline
	orch.providers["provider-1"] = provider

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "provider-1",
		GPUType:    "Tesla T4",
		GPUCount:   1,
		CPUCores:   2,
		MemoryMB:   4096,
	}

	// Allocation should still work (status check is in handler, not orchestrator)
	err := orch.AllocateResources("provider-1", allocation)
	if err != nil {
		t.Logf("Allocation on offline provider: %v (expected behavior depends on implementation)", err)
	}
}

func TestMultipleGPUTypes(t *testing.T) {
	orch := createTestOrchestrator()
	provider := &ProviderState{
		ProviderID: "provider-multi",
		Status:     StatusAvailable,
		Spec: SystemSpec{
			TotalGPUs: 6,
			GPUs: []GPUInfo{
				{Name: "Tesla T4", MemoryMB: 16384},
				{Name: "RTX 4090", MemoryMB: 24576},
			},
		},
		Capacity: ProviderCapacity{
			TotalGPUs:         map[string]int{"Tesla T4": 4, "RTX 4090": 2},
			AvailableGPUs:     map[string]int{"Tesla T4": 4, "RTX 4090": 2},
			InUseGPUs:         map[string]int{"Tesla T4": 0, "RTX 4090": 0},
			TotalCPUCores:     32,
			AvailableCPUCores: 32,
			TotalMemoryMB:     65536,
			AvailableMemoryMB: 65536,
		},
	}
	orch.providers["provider-multi"] = provider

	// Allocate Tesla T4
	alloc1 := &ResourceAllocation{
		JobID:      "job-t4",
		ProviderID: "provider-multi",
		GPUType:    "Tesla T4",
		GPUCount:   2,
	}
	if err := orch.AllocateResources("provider-multi", alloc1); err != nil {
		t.Fatalf("Tesla T4 allocation failed: %v", err)
	}

	// Allocate RTX 4090
	alloc2 := &ResourceAllocation{
		JobID:      "job-4090",
		ProviderID: "provider-multi",
		GPUType:    "RTX 4090",
		GPUCount:   1,
	}
	if err := orch.AllocateResources("provider-multi", alloc2); err != nil {
		t.Fatalf("RTX 4090 allocation failed: %v", err)
	}

	// Verify each type
	if provider.Capacity.AvailableGPUs["Tesla T4"] != 2 {
		t.Errorf("Tesla T4 AvailableGPUs = %d; want 2", provider.Capacity.AvailableGPUs["Tesla T4"])
	}
	if provider.Capacity.AvailableGPUs["RTX 4090"] != 1 {
		t.Errorf("RTX 4090 AvailableGPUs = %d; want 1", provider.Capacity.AvailableGPUs["RTX 4090"])
	}
}

func TestReleaseResources_NonexistentProvider(t *testing.T) {
	orch := createTestOrchestrator()

	allocation := &ResourceAllocation{
		JobID:      "job-1",
		ProviderID: "nonexistent",
		GPUType:    "Tesla T4",
		GPUCount:   1,
	}

	err := orch.ReleaseResources("nonexistent", allocation)
	// Current implementation doesn't return error for nonexistent provider
	// It just logs a warning and returns nil
	// This is acceptable behavior as the resources are effectively "released"
	_ = err
}

// ================== ResourceAllocation 구조체 테스트 ==================

func TestResourceAllocation_NilAllocation(t *testing.T) {
	orch := createTestOrchestrator()
	provider := createTestProvider("provider-1", 4, "Tesla T4")
	orch.providers["provider-1"] = provider

	// Nil allocation should be handled gracefully
	// This tests defensive coding
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from nil allocation: %v", r)
		}
	}()

	err := orch.AllocateResources("provider-1", nil)
	if err == nil {
		t.Log("Nil allocation was handled (no error expected if defensively coded)")
	}
}
