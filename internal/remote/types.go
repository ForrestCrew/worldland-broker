package remote

import "time"

// GPUJobSpec contains parameters for creating a GPU container on a remote node
type GPUJobSpec struct {
	SessionID     string
	UserAddress   string
	ProviderID    string
	NodeID        string // mTLS node identifier (wallet address)
	GPUCount      int
	GPUModel      string
	GPUDeviceID   string // NVIDIA GPU UUID for Docker device request
	Image         string
	CPURequest    string // e.g., "4"
	MemoryRequest string // e.g., "16Gi"
	ExpiresAt     time.Time
}

// SSHConnectionInfo contains SSH connection details for a session
type SSHConnectionInfo struct {
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Password string `json:"password"`
	User     string `json:"user"`
}

// ContainerStateUpdate represents a state update from a node
type ContainerStateUpdate struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"` // "running", "stopped", "failed"
	SSHHost   string `json:"ssh_host,omitempty"`
	SSHPort   int32  `json:"ssh_port,omitempty"`
	Password  string `json:"password,omitempty"`
	Error     string `json:"error,omitempty"`
}

// StartRentalPayload is the mTLS command payload sent to nodes to start a rental container
type StartRentalPayload struct {
	SessionID   string `json:"session_id"`
	Image       string `json:"image"`
	GPUDeviceID string `json:"gpu_device_id"`
	GPUCount    int    `json:"gpu_count"`
	CPUCount    int    `json:"cpu_count"`
	MemoryMB    int    `json:"memory_mb"`
}

// StopRentalPayload is the mTLS command payload sent to nodes to stop a rental container
type StopRentalPayload struct {
	SessionID string `json:"session_id"`
}
