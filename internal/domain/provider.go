package domain

import "time"

// ProviderStatus represents the operational status of a provider
type ProviderStatus string

const (
	ProviderStatusActive    ProviderStatus = "active"
	ProviderStatusSuspended ProviderStatus = "suspended"
)

// Provider represents a GPU provider in the system
type Provider struct {
	ID             string         `json:"id"`
	WalletAddress  string         `json:"walletAddress"`
	Status         ProviderStatus `json:"status"`
	ProviderType   ProviderType   `json:"providerType"`           // "k8s" (V4: K8s only)
	KubeconfigData *string        `json:"kubeconfigData,omitempty"` // Encrypted kubeconfig for K8s providers
	ClusterHost    *string        `json:"clusterHost,omitempty"`    // K8s API server address (display only)
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}
