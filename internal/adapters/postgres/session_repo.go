package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// PostgresSessionRepository implements domain.SessionRepository
type PostgresSessionRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.SessionRepository = (*PostgresSessionRepository)(nil)

// NewSessionRepository creates a new PostgreSQL session repository
func NewSessionRepository(pool *pgxpool.Pool) *PostgresSessionRepository {
	return &PostgresSessionRepository{pool: pool}
}

// Create inserts a new session
func (r *PostgresSessionRepository) Create(ctx context.Context, session *domain.Session) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO sessions (id, provider_id, token, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.pool.Exec(ctx, query,
		session.ID,
		session.ProviderID,
		session.Token,
		session.ExpiresAt,
		session.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// GetByToken retrieves a session by token if not expired
func (r *PostgresSessionRepository) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, provider_id, token, expires_at, created_at
		FROM sessions
		WHERE token = $1 AND expires_at > NOW()
	`

	var session domain.Session
	err := r.pool.QueryRow(ctx, query, token).Scan(
		&session.ID,
		&session.ProviderID,
		&session.Token,
		&session.ExpiresAt,
		&session.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get session by token: %w", err)
	}

	return &session, nil
}

// Delete removes a session by ID
func (r *PostgresSessionRepository) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `DELETE FROM sessions WHERE id = $1`

	_, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

// DeleteExpired removes all expired sessions
func (r *PostgresSessionRepository) DeleteExpired(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `DELETE FROM sessions WHERE expires_at < NOW()`

	_, err := r.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to delete expired sessions: %w", err)
	}

	return nil
}
