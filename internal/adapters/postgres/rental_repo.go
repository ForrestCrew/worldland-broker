package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// RentalSessionRepository implements domain.RentalSessionRepository using PostgreSQL
type RentalSessionRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.RentalSessionRepository = (*RentalSessionRepository)(nil)

// NewRentalSessionRepository creates a new PostgreSQL rental session repository
func NewRentalSessionRepository(pool *pgxpool.Pool) *RentalSessionRepository {
	return &RentalSessionRepository{pool: pool}
}

// Create inserts a new rental session and returns the generated ID
func (r *RentalSessionRepository) Create(ctx context.Context, session *domain.RentalSession) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO rental_sessions (
			id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at
	`

	err := r.pool.QueryRow(ctx, query,
		session.ID,
		session.UserAddress,
		session.ProviderAddress,
		session.NodeID,
		session.RentalID,
		session.State,
		session.PricePerSecond,
		session.StartTime,
		session.EndTime,
		session.TxHash,
		session.BlockNumber,
		session.CreatedAt,
		session.UpdatedAt,
	).Scan(&session.ID, &session.CreatedAt, &session.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create rental session: %w", err)
	}

	return nil
}

// GetByID retrieves a rental session by its UUID
func (r *RentalSessionRepository) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE id = $1
	`

	return r.scanSession(r.pool.QueryRow(ctx, query, id))
}

// GetByRentalID retrieves a rental session by its on-chain rental ID
func (r *RentalSessionRepository) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE rental_id = $1
	`

	return r.scanSession(r.pool.QueryRow(ctx, query, rentalID))
}

// Update updates an existing rental session
func (r *RentalSessionRepository) Update(ctx context.Context, session *domain.RentalSession) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE rental_sessions
		SET rental_id = $2, state = $3, start_time = $4, end_time = $5,
			tx_hash = $6, block_number = $7, updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.pool.Exec(ctx, query,
		session.ID,
		session.RentalID,
		session.State,
		session.StartTime,
		session.EndTime,
		session.TxHash,
		session.BlockNumber,
	)

	if err != nil {
		return fmt.Errorf("failed to update rental session: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("rental session not found: %s", session.ID)
	}

	return nil
}

// ListByUser retrieves rental sessions for a specific user address with pagination
func (r *RentalSessionRepository) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE user_address = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	return r.scanSessions(ctx, query, userAddress, limit, offset)
}

// ListByProvider retrieves rental sessions for a specific provider address with pagination
func (r *RentalSessionRepository) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE provider_address = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	return r.scanSessions(ctx, query, providerAddress, limit, offset)
}

// ListByState retrieves rental sessions in a specific state with pagination
func (r *RentalSessionRepository) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE state = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	return r.scanSessions(ctx, query, state, limit, offset)
}

// FindStale finds rental sessions in specified state older than the given duration
// Used for timeout enforcement (e.g., PENDING sessions > 5 minutes should be marked FAILED)
func (r *RentalSessionRepository) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cutoff := time.Now().Add(-olderThan)

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			created_at, updated_at
		FROM rental_sessions
		WHERE state = $1 AND created_at < $2
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(ctx, query, state, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to find stale rental sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// scanSession scans a single row into a RentalSession
func (r *RentalSessionRepository) scanSession(row pgx.Row) (*domain.RentalSession, error) {
	var session domain.RentalSession
	err := row.Scan(
		&session.ID,
		&session.UserAddress,
		&session.ProviderAddress,
		&session.NodeID,
		&session.RentalID,
		&session.State,
		&session.PricePerSecond,
		&session.StartTime,
		&session.EndTime,
		&session.TxHash,
		&session.BlockNumber,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("rental session not found")
		}
		return nil, fmt.Errorf("failed to scan rental session: %w", err)
	}
	return &session, nil
}

// scanSessions executes a query with pagination and returns sessions
func (r *RentalSessionRepository) scanSessions(ctx context.Context, query string, args ...any) ([]*domain.RentalSession, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query rental sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// collectSessions collects rows into a slice of RentalSession
func (r *RentalSessionRepository) collectSessions(rows pgx.Rows) ([]*domain.RentalSession, error) {
	var sessions []*domain.RentalSession
	for rows.Next() {
		var session domain.RentalSession
		err := rows.Scan(
			&session.ID,
			&session.UserAddress,
			&session.ProviderAddress,
			&session.NodeID,
			&session.RentalID,
			&session.State,
			&session.PricePerSecond,
			&session.StartTime,
			&session.EndTime,
			&session.TxHash,
			&session.BlockNumber,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan rental session: %w", err)
		}
		sessions = append(sessions, &session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rental sessions: %w", err)
	}

	return sessions, nil
}
