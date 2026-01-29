package domain

import "time"

// Session represents a provider authentication session
type Session struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expiresAt"`
	CreatedAt  time.Time `json:"createdAt"`
}
