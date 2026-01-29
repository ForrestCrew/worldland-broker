package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// PostgresNonceRepository implements domain.NonceRepository
type PostgresNonceRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.NonceRepository = (*PostgresNonceRepository)(nil)

// NewNonceRepository creates a new PostgreSQL nonce repository
func NewNonceRepository(pool *pgxpool.Pool) *PostgresNonceRepository {
	return &PostgresNonceRepository{pool: pool}
}

// Save inserts a new nonce with expiration
func (r *PostgresNonceRepository) Save(ctx context.Context, nonce string, expiresAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO nonces (nonce, expires_at, created_at)
		VALUES ($1, $2, NOW())
	`

	_, err := r.pool.Exec(ctx, query, nonce, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to save nonce: %w", err)
	}

	return nil
}

// ConsumeIfValid atomically deletes and returns whether nonce was valid
// Uses DELETE...RETURNING pattern for atomic consume-if-valid operation
func (r *PostgresNonceRepository) ConsumeIfValid(ctx context.Context, nonce string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Atomic DELETE...RETURNING - only one transaction can delete the nonce
	query := `
		DELETE FROM nonces
		WHERE nonce = $1 AND expires_at > NOW()
		RETURNING nonce
	`

	var deleted string
	err := r.pool.QueryRow(ctx, query, nonce).Scan(&deleted)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Nonce not found or already consumed/expired
			return false, nil
		}
		return false, fmt.Errorf("failed to consume nonce: %w", err)
	}

	// Successfully deleted = nonce was valid and consumed
	return true, nil
}

// CleanupExpired removes all expired nonces
func (r *PostgresNonceRepository) CleanupExpired(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `DELETE FROM nonces WHERE expires_at < NOW()`

	_, err := r.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired nonces: %w", err)
	}

	return nil
}
