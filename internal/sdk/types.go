// Package sdk provides the Worldland Provider SDK for GPU providers.
package sdk

import (
	"time"

	"github.com/nubro999/worldland-gpu/internal/provider"
)

// Config holds the SDK configuration.
type Config struct {
	// Master cluster connection
	MasterURL string `json:"master_url"` // https://master.worldland.io
	Token     string `json:"token"`      // Bootstrap token for kubeadm join
	RedisAddr string `json:"redis_addr"` // Redis address for messaging

	// Provider identification
	ProviderID string `json:"provider_id"` // Generated or provided
	WalletAddr string `json:"wallet_addr"` // Worldland wallet address

	// Mining configuration
	EnableMining   bool   `json:"enable_mining"`
	MiningGPUCount int    `json:"mining_gpu_count"`
	MiningCPUCores int    `json:"mining_cpu_cores"`
	MiningMemoryMB int64  `json:"mining_memory_mb"`
	MiningImage    string `json:"mining_image"`

	// Full Node settings
	Bootnodes   []string `json:"bootnodes"`     // P2P seed nodes
	NetworkID   int      `json:"network_id"`    // Worldland network ID
	NodeDataDir string   `json:"node_data_dir"` // Node data directory
	P2PPort     int      `json:"p2p_port"`      // P2P listen port
	RPCEnabled  bool     `json:"rpc_enabled"`   // Enable RPC
	RPCPort     int      `json:"rpc_port"`      // RPC port

	// Behavior settings
	AutoJoin          bool          `json:"auto_join"`
	AutoScale         bool          `json:"auto_scale"` // Auto-adjust mining GPUs
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
	MinMiningGPUs     int           `json:"min_mining_gpus"`
	MaxMiningGPUs     int           `json:"max_mining_gpus"`

	// Debug
	Verbose bool `json:"verbose"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MasterURL:         "https://master.worldland.io",
		RedisAddr:         "localhost:6379",
		EnableMining:      true,
		MiningGPUCount:    1,
		MiningCPUCores:    2,
		MiningMemoryMB:    4096,
		MiningImage:       "mingeyom/worldland-mio:latest",
		Bootnodes:         []string{}, // No bootnodes needed
		NetworkID:         10396,      // Worldland chain ID
		NodeDataDir:       "/data/worldland",
		P2PPort:           30303,
		RPCEnabled:        true,
		RPCPort:           8545,
		AutoJoin:          true,
		AutoScale:         false,
		HeartbeatInterval: 30 * time.Second,
		MinMiningGPUs:     1,
		MaxMiningGPUs:     4,
		Verbose:           false,
	}
}

// ProviderStatus represents the current provider status.
type ProviderStatus struct {
	ProviderID string                      `json:"provider_id"`
	NodeName   string                      `json:"node_name"`
	Status     provider.RegistrationStatus `json:"status"`
	Uptime     time.Duration               `json:"uptime"`

	// Resources
	TotalGPUs     int            `json:"total_gpus"`
	MiningGPUs    int            `json:"mining_gpus"`
	RentedGPUs    int            `json:"rented_gpus"`
	AvailableGPUs int            `json:"available_gpus"`
	GPUTypes      map[string]int `json:"gpu_types"`

	// Mining
	MiningStatus   string    `json:"mining_status"`
	MiningPodName  string    `json:"mining_pod_name"`
	Hashrate       string    `json:"hashrate,omitempty"`
	GPUUtilization []float64 `json:"gpu_utilization,omitempty"`

	// Health
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Healthy       bool      `json:"healthy"`
}

// ValidationResult holds the result of system validation.
type ValidationResult struct {
	Valid    bool              `json:"valid"`
	Checks   []ValidationCheck `json:"checks"`
	Errors   []string          `json:"errors"`
	Warnings []string          `json:"warnings"`
}

// ValidationCheck represents a single validation check.
type ValidationCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// InstallStep represents an installation step.
type InstallStep struct {
	Name     string        `json:"name"`
	Status   string        `json:"status"` // pending, running, done, failed
	Message  string        `json:"message"`
	Duration time.Duration `json:"duration,omitempty"`
}

// InstallProgress tracks overall installation progress.
type InstallProgress struct {
	CurrentStep int           `json:"current_step"`
	TotalSteps  int           `json:"total_steps"`
	Steps       []InstallStep `json:"steps"`
	StartTime   time.Time     `json:"start_time"`
}
