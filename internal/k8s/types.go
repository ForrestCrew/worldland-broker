package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Label constants from ADR-K8S-002
const (
	LabelSessionID  = "worldland.io/session-id"
	LabelProviderID = "worldland.io/provider-id"
	LabelGPURental  = "worldland.io/gpu-rental"
	LabelGPUModel   = "worldland.io/gpu-model"
	LabelRentalType = "worldland.io/rental-type" // Node label for GPU rental scheduling
)

// Annotation constants
const (
	AnnotationExpiresAt   = "worldland.io/expires-at"
	AnnotationUserAddress = "worldland.io/user-address"
	AnnotationGPUModel    = "worldland.io/gpu-model"
	AnnotationPricePerHr  = "worldland.io/price-per-hour"
	AnnotationPublicIP    = "worldland.io/public-ip"
	AnnotationStorageGB   = "worldland.io/storage-gb"
)

// Taint constants (from proxy: provider/types.go)
const (
	TaintDedicatedRental = "worldland.io/dedicated-rental"
	TaintGPUFull         = "worldland.io/gpu-full"
)

// GPUJobSpec contains parameters for creating a GPU Pod
// V4: Uses int-based resource specs with QoS Guaranteed (requests == limits)
type GPUJobSpec struct {
	SessionID    string
	UserAddress  string
	ProviderID   string
	GPUCount     int
	GPUModel     string
	Image        string
	CPUCores     int       // e.g., 4 (cores)
	MemoryGB     int       // e.g., 16 (GB)
	StorageGB    int       // e.g., 50 (GB)
	ExpiresAt    time.Time
	NodeHostname string // K8s node hostname for scheduling (from proxy: buildNodeSelector)
	PricePerHour float64
}

// SSHConnectionInfo contains SSH connection details for a session
type SSHConnectionInfo struct {
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Password string `json:"password"`
}

// TenantNamespace generates a K8s-safe namespace name from user address
// Uses SHA256 hash to ensure consistent, lowercase, max 63 character names
// Same address always produces same namespace (case-insensitive)
func TenantNamespace(userAddress string) string {
	// Normalize to lowercase for case-insensitive hashing
	normalized := strings.ToLower(userAddress)

	// Hash the address
	hash := sha256.Sum256([]byte(normalized))

	// Take first 16 hex characters (8 bytes) for namespace suffix
	// Format: tenant-{hash} = 7 + 16 = 23 chars (well under 63 limit)
	return fmt.Sprintf("tenant-%s", hex.EncodeToString(hash[:])[:16])
}

// PodName generates a pod name from session ID
func PodName(sessionID string) string {
	return fmt.Sprintf("session-%s", sessionID)
}

// SSHServiceName generates a service name from session ID
func SSHServiceName(sessionID string) string {
	return fmt.Sprintf("ssh-%s", sessionID)
}

// Note: SSHSecretName is defined in password.go
