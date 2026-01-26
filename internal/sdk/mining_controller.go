package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// MiningController manages local mining operations.
type MiningController struct {
	config    *Config
	apiClient *APIClient
	logger    *slog.Logger

	// State
	mu           sync.RWMutex
	currentGPUs  int
	targetGPUs   int
	miningStatus string
	lastUpdate   time.Time

	// Auto-scaling
	autoScaleEnabled bool
	minGPUs          int
	maxGPUs          int
}

// NewMiningController creates a new MiningController.
func NewMiningController(config *Config, apiClient *APIClient) *MiningController {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return &MiningController{
		config:           config,
		apiClient:        apiClient,
		logger:           logger,
		targetGPUs:       config.MiningGPUCount,
		autoScaleEnabled: config.AutoScale,
		minGPUs:          config.MinMiningGPUs,
		maxGPUs:          config.MaxMiningGPUs,
	}
}

// Start begins the mining controller loop.
func (mc *MiningController) Start(ctx context.Context) error {
	mc.logger.Info("Starting mining controller",
		"autoScale", mc.autoScaleEnabled,
		"targetGPUs", mc.targetGPUs,
	)

	// Initial status fetch
	if err := mc.refreshStatus(ctx); err != nil {
		mc.logger.Warn("Failed to get initial mining status", "error", err)
	}

	// Start monitoring loop
	go mc.monitorLoop(ctx)

	return nil
}

func (mc *MiningController) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			mc.logger.Info("Mining controller stopping")
			return
		case <-ticker.C:
			if err := mc.refreshStatus(ctx); err != nil {
				mc.logger.Warn("Failed to refresh mining status", "error", err)
				continue
			}

			// Auto-adjust if enabled
			if mc.autoScaleEnabled {
				mc.autoAdjust(ctx)
			}
		}
	}
}

func (mc *MiningController) refreshStatus(ctx context.Context) error {
	status, err := mc.apiClient.GetMiningStatus(ctx)
	if err != nil {
		return err
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.currentGPUs = status.Resources.GPUCount
	mc.miningStatus = status.MiningStatus
	mc.lastUpdate = time.Now()

	return nil
}

func (mc *MiningController) autoAdjust(ctx context.Context) {
	mc.mu.RLock()
	current := mc.currentGPUs
	target := mc.targetGPUs
	status := mc.miningStatus
	mc.mu.RUnlock()

	if status != "running" {
		return
	}

	// Adjust to match target
	if current < target {
		diff := target - current
		mc.logger.Info("Auto-adjusting mining GPUs", "current", current, "target", target, "allocating", diff)

		if err := mc.AllocateGPU(ctx, diff, "auto-scale"); err != nil {
			mc.logger.Warn("Failed to allocate GPUs", "error", err)
		}
	} else if current > target {
		diff := current - target
		mc.logger.Info("Auto-adjusting mining GPUs", "current", current, "target", target, "releasing", diff)

		if err := mc.ReleaseGPU(ctx, diff); err != nil {
			mc.logger.Warn("Failed to release GPUs", "error", err)
		}
	}
}

// SetTargetGPUs sets the target number of mining GPUs.
func (mc *MiningController) SetTargetGPUs(count int) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if count < mc.minGPUs {
		count = mc.minGPUs
	}
	if count > mc.maxGPUs {
		count = mc.maxGPUs
	}

	mc.targetGPUs = count
	mc.logger.Info("Target mining GPUs updated", "target", count)
}

// AllocateGPU requests additional GPUs for mining.
func (mc *MiningController) AllocateGPU(ctx context.Context, count int, reason string) error {
	if err := mc.apiClient.AllocateMiningGPU(ctx, count, reason); err != nil {
		return err
	}

	mc.mu.Lock()
	mc.currentGPUs += count
	mc.mu.Unlock()

	mc.logger.Info("Allocated mining GPUs", "count", count, "reason", reason)
	return nil
}

// ReleaseGPU releases GPUs from mining.
func (mc *MiningController) ReleaseGPU(ctx context.Context, count int) error {
	if err := mc.apiClient.ReleaseMiningGPU(ctx, count); err != nil {
		return err
	}

	mc.mu.Lock()
	mc.currentGPUs -= count
	if mc.currentGPUs < 0 {
		mc.currentGPUs = 0
	}
	mc.mu.Unlock()

	mc.logger.Info("Released mining GPUs", "count", count)
	return nil
}

// StartMining starts the mining pod.
func (mc *MiningController) StartMining(ctx context.Context) error {
	req := &StartMiningRequest{
		Image:         mc.config.MiningImage,
		GPUCount:      mc.config.MiningGPUCount,
		CPUCores:      mc.config.MiningCPUCores,
		MemoryMB:      mc.config.MiningMemoryMB,
		WalletAddress: mc.config.WalletAddr,
		NetworkID:     mc.config.NetworkID,
		P2PPort:       mc.config.P2PPort,
	}

	if err := mc.apiClient.StartMining(ctx, req); err != nil {
		return err
	}

	mc.mu.Lock()
	mc.miningStatus = "running"
	mc.currentGPUs = mc.config.MiningGPUCount
	mc.mu.Unlock()

	mc.logger.Info("Mining started",
		"gpuCount", req.GPUCount,
		"wallet", req.WalletAddress,
	)

	return nil
}

// StopMining stops the mining pod.
func (mc *MiningController) StopMining(ctx context.Context) error {
	if err := mc.apiClient.StopMining(ctx); err != nil {
		return err
	}

	mc.mu.Lock()
	mc.miningStatus = "stopped"
	mc.currentGPUs = 0
	mc.mu.Unlock()

	mc.logger.Info("Mining stopped")
	return nil
}

// GetStatus returns the current mining status.
func (mc *MiningController) GetStatus() MiningControllerStatus {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	return MiningControllerStatus{
		CurrentGPUs:      mc.currentGPUs,
		TargetGPUs:       mc.targetGPUs,
		Status:           mc.miningStatus,
		AutoScaleEnabled: mc.autoScaleEnabled,
		MinGPUs:          mc.minGPUs,
		MaxGPUs:          mc.maxGPUs,
		LastUpdate:       mc.lastUpdate,
	}
}

// MiningControllerStatus represents the mining controller state.
type MiningControllerStatus struct {
	CurrentGPUs      int       `json:"current_gpus"`
	TargetGPUs       int       `json:"target_gpus"`
	Status           string    `json:"status"`
	AutoScaleEnabled bool      `json:"auto_scale_enabled"`
	MinGPUs          int       `json:"min_gpus"`
	MaxGPUs          int       `json:"max_gpus"`
	LastUpdate       time.Time `json:"last_update"`
}

// PrintStatus prints the current status in a formatted way.
func (mc *MiningController) PrintStatus() {
	status := mc.GetStatus()

	fmt.Println("\n[Mining Status]")
	fmt.Printf("  Status: %s\n", status.Status)
	fmt.Printf("  Current GPUs: %d\n", status.CurrentGPUs)
	fmt.Printf("  Target GPUs: %d\n", status.TargetGPUs)
	fmt.Printf("  Auto-Scale: %v (min: %d, max: %d)\n",
		status.AutoScaleEnabled, status.MinGPUs, status.MaxGPUs)
	fmt.Printf("  Last Update: %s\n", status.LastUpdate.Format(time.RFC3339))
}
