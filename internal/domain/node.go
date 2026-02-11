package domain

import "time"

// NodeStatus represents the operational status of a GPU node
type NodeStatus string

const (
	NodeStatusPending NodeStatus = "pending"
	NodeStatusActive  NodeStatus = "active"
	NodeStatusOffline NodeStatus = "offline"
)

// Node represents a GPU node registered by a provider
type Node struct {
	ID                string     `json:"id"`
	ProviderID        string     `json:"providerId"`
	GPUUUID           string     `json:"gpuUuid"`
	GPUType           string     `json:"gpuType"`
	MemoryGB          int        `json:"memoryGb"`
	PricePerSecond    string     `json:"pricePerSecond"` // Decimal string for precision
	APIEndpoint       string     `json:"apiEndpoint"`    // mTLS HTTPS endpoint for Hub-to-Node communication
	Status            NodeStatus `json:"status"`
	CertificateExpiry *time.Time `json:"certificateExpiry,omitempty"`
	// Capacity tracking (V4: populated from K8s cluster discovery)
	TotalGPUs         int    `json:"totalGpus"`
	AvailableGPUs     int    `json:"availableGpus"`
	TotalCPUCores     int    `json:"totalCpuCores"`
	TotalMemoryGB     int    `json:"totalMemoryGb"`
	AvailableCPUCores int    `json:"availableCpuCores"`
	AvailableMemoryGB int    `json:"availableMemoryGb"`
	K8sNodeName   string `json:"k8sNodeName,omitempty"`
	// GPU details (RunPod-style marketplace)
	GPUModel      string `json:"gpuModel"`      // "Tesla T4" (NVML name)
	VramMB        int    `json:"vramMb"`         // GPU VRAM in MB (15360)
	DriverVersion string `json:"driverVersion"`  // NVIDIA driver version
	ExternalIP    string `json:"externalIp,omitempty"`    // External IP for SSH access
	MaxStorageGB  int    `json:"maxStorageGb,omitempty"` // Max ephemeral storage in GB (from K8s allocatable)
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// GPUTypeGroup represents aggregated GPU info for marketplace display
type GPUTypeGroup struct {
	GPUModel       string
	VramMB         int
	TotalGPUs      int
	AvailableGPUs  int
	TotalNodes     int
	MinPricePerSec string
	MaxPricePerSec string
	AvgCPUCores    int
	AvgMemoryGB    int
}
