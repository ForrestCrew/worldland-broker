package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nubro999/worldland-gpu/internal/messaging"
	"github.com/nubro999/worldland-gpu/internal/provider"
)

// Daemon runs the provider in resident mode.
type Daemon struct {
	config           *Config
	apiClient        *APIClient
	miningController *MiningController
	logger           *slog.Logger

	// Provider info
	providerID string
	nodeName   string
	spec       *provider.SystemSpec

	// State
	startTime time.Time
	healthy   bool
}

// NewDaemon creates a new Daemon instance.
func NewDaemon(config *Config, providerID, nodeName string, spec *provider.SystemSpec) *Daemon {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	apiClient := NewAPIClient(config.MasterURL, providerID)
	miningController := NewMiningController(config, apiClient)

	return &Daemon{
		config:           config,
		apiClient:        apiClient,
		miningController: miningController,
		logger:           logger,
		providerID:       providerID,
		nodeName:         nodeName,
		spec:             spec,
		startTime:        time.Now(),
		healthy:          true,
	}
}

// Run starts the daemon and blocks until interrupted.
func (d *Daemon) Run(ctx context.Context) error {
	d.printBanner()

	// Setup cancellation
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start mining controller
	if d.config.EnableMining {
		if err := d.miningController.Start(ctx); err != nil {
			d.logger.Warn("Failed to start mining controller", "error", err)
		}
	}

	// Start heartbeat loop
	go d.heartbeatLoop(ctx)

	// Start health check loop
	go d.healthCheckLoop(ctx)

	// Wait for shutdown
	<-ctx.Done()

	d.logger.Info("Daemon shutting down...")
	return nil
}

func (d *Daemon) printBanner() {
	fmt.Println("\n====================================================")
	fmt.Println("  🎉 Provider Setup Complete!")
	fmt.Println("====================================================")
	fmt.Println()
	fmt.Println("Your provider is now:")
	if d.config.EnableMining {
		fmt.Println("  - Mining Worldland blocks")
	}
	fmt.Println("  - Ready to accept GPU rental jobs")
	fmt.Printf("  - Sending heartbeats every %v\n", d.config.HeartbeatInterval)
	fmt.Println()
	fmt.Printf("Provider ID: %s\n", d.providerID)
	fmt.Printf("Node Name: %s\n", d.nodeName)
	fmt.Printf("Wallet: %s\n", d.config.WalletAddr)
	fmt.Println()
	fmt.Printf("Dashboard: https://dashboard.worldland.io/providers/%s\n", d.providerID)
	fmt.Println()
	fmt.Println("To manage your provider:")
	fmt.Println("  $ worldland-provider status")
	fmt.Println("  $ worldland-provider logs")
	fmt.Println("  $ worldland-provider mining set-gpu 2")
	fmt.Println()
	fmt.Println("Daemon started. Press Ctrl+C to stop.")
	fmt.Println("====================================================")
}

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	// Connect to Redis
	redisHost, redisPort := parseRedisAddr(d.config.RedisAddr)
	redisClient, err := messaging.GetClient(&messaging.Config{
		Host: redisHost,
		Port: redisPort,
	})
	if err != nil {
		d.logger.Error("Failed to connect to Redis for heartbeat", "error", err)
		return
	}

	producer := messaging.NewProducer(redisClient)
	ticker := time.NewTicker(d.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := provider.HeartbeatMessage{
				ProviderID:  d.providerID,
				NodeName:    d.nodeName,
				Status:      provider.StatusAvailable,
				GPUUsage:    d.getGPUUsage(),
				CPUUsage:    d.getCPUUsage(),
				MemoryUsage: d.getMemoryUsage(),
				ActiveJobs:  0, // TODO: Get from kubelet
				Timestamp:   time.Now(),
			}

			if _, err := producer.Publish(ctx, provider.StreamNames.Heartbeat, hb); err != nil {
				d.logger.Error("Failed to send heartbeat", "error", err)
				d.healthy = false
			} else {
				d.logger.Debug("Heartbeat sent")
				d.healthy = true
			}
		}
	}
}

func (d *Daemon) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check API server health
			if err := d.apiClient.HealthCheck(ctx); err != nil {
				d.logger.Warn("API server health check failed", "error", err)
				d.healthy = false
			} else {
				d.healthy = true
			}

			// Check kubelet health
			// TODO: Implement kubelet health check
		}
	}
}

func (d *Daemon) getGPUUsage() []float64 {
	// TODO: Implement nvidia-smi GPU utilization query
	return []float64{}
}

func (d *Daemon) getCPUUsage() float64 {
	// TODO: Implement CPU usage calculation
	return 0.0
}

func (d *Daemon) getMemoryUsage() float64 {
	// TODO: Implement memory usage calculation
	return 0.0
}

// GetStatus returns the current daemon status.
func (d *Daemon) GetStatus() *ProviderStatus {
	miningStatus := d.miningController.GetStatus()

	return &ProviderStatus{
		ProviderID:    d.providerID,
		NodeName:      d.nodeName,
		Status:        provider.StatusAvailable,
		Uptime:        time.Since(d.startTime),
		TotalGPUs:     d.spec.TotalGPUs,
		MiningGPUs:    miningStatus.CurrentGPUs,
		AvailableGPUs: d.spec.TotalGPUs - miningStatus.CurrentGPUs,
		MiningStatus:  miningStatus.Status,
		LastHeartbeat: time.Now(),
		Healthy:       d.healthy,
	}
}

// PrintStatus prints the current status.
func (d *Daemon) PrintStatus() {
	status := d.GetStatus()

	fmt.Println("\n[Provider Status]")
	fmt.Printf("  Provider ID: %s\n", status.ProviderID)
	fmt.Printf("  Node Name: %s\n", status.NodeName)
	fmt.Printf("  Status: %s\n", status.Status)
	fmt.Printf("  Uptime: %s\n", status.Uptime.Round(time.Second))
	fmt.Printf("  Healthy: %v\n", status.Healthy)
	fmt.Println()
	fmt.Println("[Resources]")
	fmt.Printf("  Total GPUs: %d\n", status.TotalGPUs)
	fmt.Printf("  Mining: %d\n", status.MiningGPUs)
	fmt.Printf("  Available: %d\n", status.AvailableGPUs)
	fmt.Println()

	d.miningController.PrintStatus()
}

// GetMiningController returns the mining controller.
func (d *Daemon) GetMiningController() *MiningController {
	return d.miningController
}
