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
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}
