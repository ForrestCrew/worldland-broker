package domain

import (
	"context"
	"time"
)

// ProviderType represents the type of compute provider
type ProviderType string

const (
	// ProviderTypeK8s represents a data center provider with a K8s cluster
	ProviderTypeK8s ProviderType = "k8s"
)

// JobSpec contains parameters for creating a GPU container session on K8s
type JobSpec struct {
	SessionID   string
	UserAddress string
	ProviderID  string
	NodeID      string // DB node ID → used to resolve K8s node name for scheduling
	GPUCount    int
	GPUModel    string
	Image       string
	CPUCores    int // e.g., 4 (cores)
	MemoryGB    int // e.g., 16 (GB)
	StorageGB   int // e.g., 50 (GB)
	ExpiresAt   time.Time
}

// SSHConnectionInfo contains SSH connection details for a session
type SSHConnectionInfo struct {
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Password string `json:"password"`
	User     string `json:"user"`
}

// JobExecutor defines the interface for creating/deleting GPU sessions.
// Only K8s providers implement this interface (V4).
type JobExecutor interface {
	// CreateGPUSession provisions a GPU container and returns the SSH password
	CreateGPUSession(ctx context.Context, spec JobSpec) (password string, err error)
	// DeleteGPUSession terminates a GPU container session (idempotent)
	DeleteGPUSession(ctx context.Context, session *RentalSession) error
	// GetSSHConnectionInfo retrieves SSH connection details for a running session
	GetSSHConnectionInfo(ctx context.Context, session *RentalSession) (*SSHConnectionInfo, error)
	// GetPodStatus returns the container status string for a session ("Pending"|"Creating"|"Running"|"Failed")
	GetPodStatus(ctx context.Context, session *RentalSession) (string, error)
}

// SessionCleanup defines the interface for cleaning up session containers.
// Used by EventProcessor to delete containers on rental stop.
type SessionCleanup interface {
	// DeleteSessionContainer deletes the container for a session
	DeleteSessionContainer(ctx context.Context, session *RentalSession) error
}
