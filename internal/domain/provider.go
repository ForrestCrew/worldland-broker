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
	ID            string         `json:"id"`
	WalletAddress string         `json:"walletAddress"`
	Status        ProviderStatus `json:"status"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}
