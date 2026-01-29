package domain

import (
	"context"
	"time"
)

// ProviderRepository defines the interface for provider persistence
type ProviderRepository interface {
	Create(ctx context.Context, provider *Provider) error
	GetByID(ctx context.Context, id string) (*Provider, error)
	GetByWallet(ctx context.Context, walletAddress string) (*Provider, error)
	Update(ctx context.Context, provider *Provider) error
}

// NodeRepository defines the interface for node persistence
type NodeRepository interface {
	Create(ctx context.Context, node *Node) error
	GetByID(ctx context.Context, id string) (*Node, error)
	GetByProvider(ctx context.Context, providerID string) ([]*Node, error)
	Update(ctx context.Context, node *Node) error
	Delete(ctx context.Context, id string) error
}

// SessionRepository defines the interface for session persistence
type SessionRepository interface {
	Create(ctx context.Context, session *Session) error
	GetByToken(ctx context.Context, token string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) error
}

// NonceRepository defines the interface for nonce persistence (SIWE replay prevention)
type NonceRepository interface {
	Save(ctx context.Context, nonce string, expiresAt time.Time) error
	ConsumeIfValid(ctx context.Context, nonce string) (bool, error)
	CleanupExpired(ctx context.Context) error
}

// RentalSessionRepository defines the interface for rental session persistence
// RentalSession tracks GPU rental lifecycle from PENDING through RUNNING to terminal states
type RentalSessionRepository interface {
	Create(ctx context.Context, session *RentalSession) error
	GetByID(ctx context.Context, id string) (*RentalSession, error)
	GetByRentalID(ctx context.Context, rentalID uint64) (*RentalSession, error)
	Update(ctx context.Context, session *RentalSession) error
	ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*RentalSession, error)
	ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*RentalSession, error)
	ListByState(ctx context.Context, state RentalSessionState, limit, offset int) ([]*RentalSession, error)
	// FindStale finds sessions in specified state older than duration (for timeout enforcement)
	FindStale(ctx context.Context, state RentalSessionState, olderThan time.Duration) ([]*RentalSession, error)
}
