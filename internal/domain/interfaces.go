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
	GetByGPUUUID(ctx context.Context, gpuUUID string) (*Node, error) // For duplicate prevention
	GetByProvider(ctx context.Context, providerID string) ([]*Node, error)
	ListActive(ctx context.Context) ([]*Node, error) // For discovery endpoints (Phase 27)
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
	// FindByUserAndState finds sessions for a user in a specific state (for pending settlement calculation)
	FindByUserAndState(ctx context.Context, userAddress string, state RentalSessionState) ([]*RentalSession, error)
	// FindPendingSettlement finds STOPPED sessions without settlement (settled_at IS NULL)
	FindPendingSettlement(ctx context.Context, userAddress string) ([]*RentalSession, error)
	// FindAllPendingSettlement finds all STOPPED sessions without SettledAt (for batch settlement - 04-07)
	FindAllPendingSettlement(ctx context.Context) ([]*RentalSession, error)
	// UpdateSettlement records settlement amount and timestamp (for batch settlement - 04-07)
	UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error

	// GetByTxHash retrieves a session by its transaction hash (for idempotency check)
	GetByTxHash(ctx context.Context, txHash string) (*RentalSession, error)
	// SetTxHash atomically sets tx_hash for a session (returns error if already set)
	SetTxHash(ctx context.Context, sessionID, txHash string) error
	// SoftDeletePendingBefore soft-deletes PENDING sessions older than cutoff without tx_hash
	SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// ListPendingWithTxHash finds PENDING sessions that have tx_hash set (for verification worker)
	ListPendingWithTxHash(ctx context.Context) ([]*RentalSession, error)

	// TouchSession updates updated_at timestamp to current time (heartbeat for timeout prevention)
	TouchSession(ctx context.Context, sessionID string) error
	// FindExpiringSessions finds RUNNING sessions with extended_until before cutoff time (16-01)
	FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*RentalSession, error)
	// UpdateExtension atomically updates session extension fields (16-01)
	UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error
	// CreateExtensionRecord creates an audit record for a session extension (16-01)
	CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error)
}

// ImageRepository defines the interface for base image preset persistence (24-01)
type ImageRepository interface {
	// GetByID retrieves a base image by its ID
	GetByID(ctx context.Context, id string) (*BaseImage, error)
	// List retrieves all active base images
	List(ctx context.Context) ([]*BaseImage, error)
	// ListByCategory retrieves base images filtered by category
	ListByCategory(ctx context.Context, category ImageCategory) ([]*BaseImage, error)
}
