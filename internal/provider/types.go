// Package provider handles resource provider registration and management.
package provider

import (
	"time"
)

// RegistrationStatus represents the status of a provider registration.
type RegistrationStatus string

const (
	StatusPending   RegistrationStatus = "pending"
	StatusApproved  RegistrationStatus = "approved"
	StatusJoined    RegistrationStatus = "joined"
	StatusAvailable RegistrationStatus = "available"
	StatusBusy      RegistrationStatus = "busy"
	StatusOffline   RegistrationStatus = "offline"
	StatusRejected  RegistrationStatus = "rejected"
)

// GPUInfo contains information about a GPU device.
type GPUInfo struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`         // e.g., "Tesla T4", "RTX 4090"
	MemoryMB    int64  `json:"memory_mb"`    // GPU memory in MB
	UUID        string `json:"uuid"`         // Unique GPU identifier
	DriverVer   string `json:"driver_ver"`   // NVIDIA driver version
	CUDAVersion string `json:"cuda_version"` // CUDA version
	PCIBusID    string `json:"pci_bus_id"`   // PCI bus ID
}

// SystemSpec represents the hardware specifications of a provider node.
type SystemSpec struct {
	// Basic Info
	Hostname     string `json:"hostname"`
	OS           string `json:"os"` // e.g., "Ubuntu 22.04"
	KernelVer    string `json:"kernel_ver"`
	Architecture string `json:"architecture"` // e.g., "x86_64", "arm64"

	// CPU
	CPUModel   string `json:"cpu_model"`
	CPUCores   int    `json:"cpu_cores"`
	CPUThreads int    `json:"cpu_threads"`

	// Memory
	TotalMemoryMB int64 `json:"total_memory_mb"`

	// GPU
	GPUs      []GPUInfo `json:"gpus"`
	TotalGPUs int       `json:"total_gpus"`

	// Storage
	TotalDiskGB     int64 `json:"total_disk_gb"`
	AvailableDiskGB int64 `json:"available_disk_gb"`

	// Network
	PublicIP  string `json:"public_ip"`
	PrivateIP string `json:"private_ip"`
	Bandwidth string `json:"bandwidth,omitempty"` // e.g., "1Gbps"
}

// ProviderCapacity represents the total and available resources of a datacenter provider.
type ProviderCapacity struct {
	// === 하위 호환성 필드 (Legacy - 단일 GPU 타입용) ===
	GPUCount        int     `json:"gpu_count"`          // 총 GPU 개수 (레거시)
	CPUCores        int     `json:"cpu_cores"`          // 총 CPU 코어 (레거시)
	MemoryMB        int64   `json:"memory_mb"`          // 총 메모리 MB (레거시)
	GPUPricePerHour float64 `json:"gpu_price_per_hour"` // GPU 시간당 가격 (레거시)

	// === 총 보유량 (Total Resources) - 데이터센터 모델 ===
	TotalGPUs     map[string]int `json:"total_gpus,omitempty"`      // GPU 타입별 총 개수: {"RTX 4090": 100}
	TotalCPUCores int            `json:"total_cpu_cores,omitempty"` // 총 CPU 코어
	TotalMemoryMB int64          `json:"total_memory_mb,omitempty"` // 총 메모리 (MB)
	TotalDiskGB   int64          `json:"total_disk_gb,omitempty"`   // 총 스토리지 (GB)

	// === 채굴용 예약 (Worldland Mining) ===
	MiningGPUs     map[string]int `json:"mining_gpus,omitempty"`      // 채굴 할당 GPU: {"Tesla T4": 2}
	MiningCPUCores int            `json:"mining_cpu_cores,omitempty"` // 채굴 할당 CPU 코어
	MiningMemoryMB int64          `json:"mining_memory_mb,omitempty"` // 채굴 할당 메모리 (MB)
	MiningPodName  string         `json:"mining_pod_name,omitempty"`  // 채굴 Pod 이름
	MiningStatus   string         `json:"mining_status,omitempty"`    // "running" | "stopped" | "pending"

	// === 렌탈 중 (사용자에게 할당됨) ===
	InUseGPUs     map[string]int `json:"in_use_gpus,omitempty"`      // 렌탈 중인 GPU
	InUseCPUCores int            `json:"in_use_cpu_cores,omitempty"` // 렌탈 중인 CPU 코어
	InUseMemoryMB int64          `json:"in_use_memory_mb,omitempty"` // 렌탈 중인 메모리 (MB)

	// === 렌탈 가능 (Available Resources) ===
	AvailableGPUs     map[string]int `json:"available_gpus,omitempty"`      // 렌탈 가능 GPU
	AvailableCPUCores int            `json:"available_cpu_cores,omitempty"` // 렌탈 가능 CPU 코어
	AvailableMemoryMB int64          `json:"available_memory_mb,omitempty"` // 렌탈 가능 메모리 (MB)
	AvailableDiskGB   int64          `json:"available_disk_gb,omitempty"`   // 렌탈 가능 스토리지 (GB)

	// === 가격 정책 (Pricing in WLC) ===
	GPUPricesPerHour    map[string]float64 `json:"gpu_prices_per_hour,omitempty"`     // GPU 타입별 시간당 가격
	CPUPricePerHour     float64            `json:"cpu_price_per_hour,omitempty"`      // CPU 코어당 시간당 가격
	MemoryPricePerGBHr  float64            `json:"memory_price_per_gb_hr,omitempty"`  // 메모리 GB당 시간당 가격
	StoragePricePerGBHr float64            `json:"storage_price_per_gb_hr,omitempty"` // 스토리지 GB당 시간당 가격

	// === 가용 시간 ===
	AvailableFrom  time.Time `json:"available_from,omitempty"`
	AvailableUntil time.Time `json:"available_until,omitempty"`
}

// TotalGPUCount returns the total number of GPUs across all types.
func (c *ProviderCapacity) TotalGPUCount() int {
	total := 0
	for _, count := range c.TotalGPUs {
		total += count
	}
	return total
}

// AvailableGPUCount returns the available number of GPUs across all types.
func (c *ProviderCapacity) AvailableGPUCount() int {
	total := 0
	for _, count := range c.AvailableGPUs {
		total += count
	}
	// Fallback to legacy field if AvailableGPUs is empty
	if total == 0 && c.GPUCount > 0 {
		return c.GPUCount - c.InUseGPUCount()
	}
	return total
}

// MiningGPUCount returns the total number of GPUs reserved for mining.
func (c *ProviderCapacity) MiningGPUCount() int {
	total := 0
	for _, count := range c.MiningGPUs {
		total += count
	}
	return total
}

// InUseGPUCount returns the total number of GPUs currently in use for rentals.
func (c *ProviderCapacity) InUseGPUCount() int {
	total := 0
	for _, count := range c.InUseGPUs {
		total += count
	}
	return total
}

// GetAvailableCPUCores returns available CPU cores with legacy fallback.
func (c *ProviderCapacity) GetAvailableCPUCores() int {
	if c.AvailableCPUCores > 0 {
		return c.AvailableCPUCores
	}
	// Fallback to legacy field
	if c.CPUCores > 0 {
		return c.CPUCores - c.InUseCPUCores
	}
	return 0
}

// GetAvailableMemoryMB returns available memory in MB with legacy fallback.
func (c *ProviderCapacity) GetAvailableMemoryMB() int64 {
	if c.AvailableMemoryMB > 0 {
		return c.AvailableMemoryMB
	}
	// Fallback to legacy field
	if c.MemoryMB > 0 {
		return c.MemoryMB - c.InUseMemoryMB
	}
	return 0
}

// GetGPUPrice returns the price per hour for a specific GPU type.
func (c *ProviderCapacity) GetGPUPrice(gpuType string) float64 {
	if c.GPUPricesPerHour == nil {
		return 0
	}
	return c.GPUPricesPerHour[gpuType]
}

// DefaultGPUPrice returns the first GPU price found (for backwards compatibility).
func (c *ProviderCapacity) DefaultGPUPrice() float64 {
	for _, price := range c.GPUPricesPerHour {
		return price
	}
	return 0
}

// GPUTypeInfo represents information about a specific GPU type.
type GPUTypeInfo struct {
	Model      string  `json:"model"`          // "RTX 4090", "A100"
	MemoryMB   int64   `json:"memory_mb"`      // GPU 메모리
	Total      int     `json:"total"`          // 총 개수
	Available  int     `json:"available"`      // 사용 가능 개수
	PricePerHr float64 `json:"price_per_hour"` // 시간당 가격
}

// ResourceAllocation represents allocated resources for a job.
type ResourceAllocation struct {
	JobID       string    `json:"job_id"`
	ProviderID  string    `json:"provider_id"`
	GPUType     string    `json:"gpu_type"`
	GPUCount    int       `json:"gpu_count"`
	CPUCores    int       `json:"cpu_cores"`
	MemoryMB    int64     `json:"memory_mb"`
	StorageGB   int64     `json:"storage_gb"`
	PricePerHr  float64   `json:"price_per_hour"` // 총 시간당 가격
	AllocatedAt time.Time `json:"allocated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// MiningConfig represents configuration for Worldland mining container.
type MiningConfig struct {
	// 컨테이너 설정
	Image    string `json:"image"`     // "worldland/miner:v1.0"
	GPUCount int    `json:"gpu_count"` // 초기 채굴 GPU 개수
	CPUCores int    `json:"cpu_cores"` // 채굴 CPU 코어
	MemoryMB int64  `json:"memory_mb"` // 채굴 메모리 (MB)

	// 블록체인 설정
	WalletAddress string   `json:"wallet_address"` // 채굴 보상 지갑 (Coinbase)
	ExtraArgs     []string `json:"extra_args"`     // 추가 인자

	// Full Node 설정
	Bootnodes   []string `json:"bootnodes,omitempty"`     // P2P seed nodes
	NetworkID   int      `json:"network_id,omitempty"`    // Worldland network ID
	NodeDataDir string   `json:"node_data_dir,omitempty"` // Node data directory
	P2PPort     int      `json:"p2p_port,omitempty"`      // P2P listen port
	RPCEnabled  bool     `json:"rpc_enabled,omitempty"`   // Enable RPC
	RPCPort     int      `json:"rpc_port,omitempty"`      // RPC port
	PublicIP    string   `json:"public_ip,omitempty"`     // Public IP for NAT (auto-detected)

	// 환경 변수
	EnvVars map[string]string `json:"env_vars,omitempty"`
}

// DefaultMiningConfig returns a default mining configuration.
func DefaultMiningConfig(walletAddress string) *MiningConfig {
	return &MiningConfig{
		Image:         "mingeyom/worldland-mio:latest",
		GPUCount:      1,
		CPUCores:      2,
		MemoryMB:      4096,
		WalletAddress: walletAddress,
		NetworkID:     10396, // Worldland chain ID
		P2PPort:       30303,
		RPCEnabled:    true,
		RPCPort:       8545,
	}
}

// RegistrationRequest is sent by provider agent to register with the cluster.
type RegistrationRequest struct {
	// Provider identification
	ProviderID string `json:"provider_id"` // Unique provider ID (wallet address or UUID)
	WalletAddr string `json:"wallet_addr"` // WorldLand wallet address for payments

	// Hardware info
	Spec     SystemSpec       `json:"spec"`
	Capacity ProviderCapacity `json:"capacity"`

	// Mining configuration (optional)
	MiningConfig *MiningConfig `json:"mining_config,omitempty"`

	// Agent info
	AgentVersion string `json:"agent_version"`

	// Timestamp
	Timestamp time.Time `json:"timestamp"`
}

// RegistrationResponse is sent back to the provider agent.
type RegistrationResponse struct {
	Success bool               `json:"success"`
	Status  RegistrationStatus `json:"status"`
	Message string             `json:"message,omitempty"`

	// Join information (only if approved)
	JoinToken   string `json:"join_token,omitempty"`
	JoinCommand string `json:"join_command,omitempty"`
	MasterIP    string `json:"master_ip,omitempty"`
	MasterPort  int    `json:"master_port,omitempty"`
	CAHash      string `json:"ca_hash,omitempty"`

	// Assigned node info (after join)
	NodeName string            `json:"node_name,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// CapacityUpdate is sent by provider agent to update available resources.
type CapacityUpdate struct {
	ProviderID string           `json:"provider_id"`
	NodeName   string           `json:"node_name"`
	Capacity   ProviderCapacity `json:"capacity"`
	Timestamp  time.Time        `json:"timestamp"`
}

// HeartbeatMessage is sent periodically by provider agents.
type HeartbeatMessage struct {
	ProviderID string             `json:"provider_id"`
	NodeName   string             `json:"node_name"`
	Status     RegistrationStatus `json:"status"`

	// Current utilization
	GPUUsage    []float64 `json:"gpu_usage"`    // Per-GPU usage percentage
	CPUUsage    float64   `json:"cpu_usage"`    // Overall CPU usage %
	MemoryUsage float64   `json:"memory_usage"` // Memory usage %

	// Running jobs
	ActiveJobs int `json:"active_jobs"`

	Timestamp time.Time `json:"timestamp"`
}

// NodeLabels defines standard labels for provider nodes.
var NodeLabels = struct {
	ProviderID       string
	RentalType       string
	GPUModel         string
	BlockchainNode   string
	AvailabilityZone string
}{
	ProviderID:       "worldland.io/provider-id",
	RentalType:       "worldland.io/rental-type",
	GPUModel:         "worldland.io/gpu-model",
	BlockchainNode:   "worldland.io/blockchain-provider",
	AvailabilityZone: "topology.kubernetes.io/zone",
}

// NodeTaints defines standard taints for provider nodes.
var NodeTaints = struct {
	DedicatedRental string
	GPUFull         string
	Maintenance     string
}{
	DedicatedRental: "worldland.io/dedicated-rental",
	GPUFull:         "worldland.io/gpu-full",
	Maintenance:     "worldland.io/maintenance",
}

// StreamNames defines Redis stream names for provider communication.
var StreamNames = struct {
	Registration   string
	CapacityUpdate string
	Heartbeat      string
	Commands       string
}{
	Registration:   "provider:registration",
	CapacityUpdate: "provider:capacity",
	Heartbeat:      "provider:heartbeat",
	Commands:       "provider:commands",
}
