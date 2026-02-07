package domain

import "context"

// ProviderType represents the type of compute provider
type ProviderType string

const (
	// ProviderTypeDocker represents an individual provider running Docker via mTLS
	ProviderTypeDocker ProviderType = "docker"
	// ProviderTypeK8s represents a data center provider with a K8s cluster
	ProviderTypeK8s ProviderType = "k8s"
)

// JobSpec contains provider-agnostic parameters for creating a GPU container session
type JobSpec struct {
	SessionID     string
	UserAddress   string
	ProviderID    string
	NodeID        string // mTLS node identifier (Docker providers only)
	GPUCount      int
	GPUModel      string
	GPUDeviceID   string // NVIDIA GPU UUID (Docker providers only)
	Image         string
	CPURequest    string // e.g., "4"
	MemoryRequest string // e.g., "16Gi"
	CPULimit      string // e.g., "8"
	MemoryLimit   string // e.g., "32Gi"
}

// SSHConnectionInfo contains SSH connection details for a session
type SSHConnectionInfo struct {
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Password string `json:"password"`
	User     string `json:"user"`
}

// JobExecutor defines the interface for creating/deleting GPU sessions.
// Both K8s and Docker (remote mTLS) providers implement this interface.
type JobExecutor interface {
	// CreateGPUSession provisions a GPU container and returns the SSH password
	CreateGPUSession(ctx context.Context, spec JobSpec) (password string, err error)
	// DeleteGPUSession terminates a GPU container session (idempotent)
	DeleteGPUSession(ctx context.Context, session *RentalSession) error
	// GetSSHConnectionInfo retrieves SSH connection details for a running session
	GetSSHConnectionInfo(ctx context.Context, session *RentalSession) (*SSHConnectionInfo, error)
}

// SessionCleanup defines the interface for cleaning up session containers.
// Used by EventProcessor to delete containers on rental stop without knowing the provider type.
type SessionCleanup interface {
	// DeleteSessionContainer deletes the container for a session, routing to the correct executor
	DeleteSessionContainer(ctx context.Context, session *RentalSession) error
}
