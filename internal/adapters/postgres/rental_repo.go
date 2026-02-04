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
			docker_image, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
		session.DockerImage,
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE id = $1 AND deleted_at IS NULL
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE rental_id = $1 AND deleted_at IS NULL
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

// TouchSession updates the updated_at timestamp to current time (heartbeat for timeout prevention)
func (r *RentalSessionRepository) TouchSession(ctx context.Context, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `UPDATE rental_sessions SET updated_at = NOW() WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to touch session: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("rental session not found: %s", sessionID)
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE user_address = $1 AND deleted_at IS NULL
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE provider_address = $1 AND deleted_at IS NULL
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE state = $1 AND deleted_at IS NULL
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
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE state = $1 AND updated_at < $2 AND deleted_at IS NULL
		ORDER BY updated_at ASC
	`

	rows, err := r.pool.Query(ctx, query, state, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to find stale rental sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// FindByUserAndState finds rental sessions for a user in a specific state
// Used for pending settlement calculation (e.g., find all RUNNING sessions for a user)
func (r *RentalSessionRepository) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE user_address = $1 AND state = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, userAddress, state)
	if err != nil {
		return nil, fmt.Errorf("failed to find sessions by user and state: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// FindPendingSettlement finds STOPPED sessions without settlement (settled_at IS NULL)
// Note: settled_at column will be added in future migration, for now returns empty list
func (r *RentalSessionRepository) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// TODO: Update query when settled_at column is added to rental_sessions table
	// For now, return STOPPED sessions (settlement tracking will be added in future phase)
	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE user_address = $1 AND state = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, userAddress, domain.RentalStateStopped)
	if err != nil {
		return nil, fmt.Errorf("failed to find pending settlement sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// FindAllPendingSettlement finds all STOPPED sessions without SettledAt (for batch settlement - 04-07)
func (r *RentalSessionRepository) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			settled_at, settled_amount, deleted_at, created_at, updated_at
		FROM rental_sessions
		WHERE state = $1 AND settled_at IS NULL AND deleted_at IS NULL
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(ctx, query, domain.RentalStateStopped)
	if err != nil {
		return nil, fmt.Errorf("failed to find pending settlement sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessionsWithSettlement(rows)
}

// UpdateSettlement records settlement amount and timestamp (for batch settlement - 04-07)
func (r *RentalSessionRepository) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE rental_sessions
		SET settled_amount = $2, settled_at = $3, updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.pool.Exec(ctx, query, sessionID, amount, settledAt)
	if err != nil {
		return fmt.Errorf("failed to update settlement: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("rental session not found: %s", sessionID)
	}

	return nil
}

// GetByTxHash retrieves a session by its transaction hash (for idempotency check)
func (r *RentalSessionRepository) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			deleted_at, created_at, updated_at
		FROM rental_sessions
		WHERE tx_hash = $1 AND deleted_at IS NULL
	`

	return r.scanSession(r.pool.QueryRow(ctx, query, txHash))
}

// SetTxHash atomically sets tx_hash for a session (returns error if already set or session deleted)
func (r *RentalSessionRepository) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE rental_sessions
		SET tx_hash = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`

	result, err := r.pool.Exec(ctx, query, txHash, sessionID)
	if err != nil {
		return fmt.Errorf("failed to set tx_hash: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("rental session not found or already deleted: %s", sessionID)
	}

	return nil
}

// SoftDeletePendingBefore soft-deletes PENDING sessions older than cutoff without tx_hash
// CRITICAL: Include tx_hash IS NULL to not delete sessions with confirmation in progress
func (r *RentalSessionRepository) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		UPDATE rental_sessions
		SET deleted_at = NOW()
		WHERE state = 'PENDING'
			AND created_at < $1
			AND deleted_at IS NULL
			AND tx_hash IS NULL
	`

	result, err := r.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to soft delete pending sessions: %w", err)
	}

	return result.RowsAffected(), nil
}

// ListPendingWithTxHash finds PENDING sessions that have tx_hash set (for verification worker)
func (r *RentalSessionRepository) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			deleted_at, extended_until, extension_count, total_extended_minutes,
			docker_image, created_at, updated_at
		FROM rental_sessions
		WHERE state = 'PENDING' AND tx_hash IS NOT NULL AND deleted_at IS NULL
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending sessions with tx_hash: %w", err)
	}
	defer rows.Close()

	return r.collectSessions(rows)
}

// FindExpiringSessions finds RUNNING sessions with extended_until before cutoff time
// Used by ExpirationWorker to find sessions that need to be stopped (16-01)
func (r *RentalSessionRepository) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT id, user_address, provider_address, node_id, rental_id, state,
			price_per_second, start_time, end_time, tx_hash, block_number,
			settled_at, settled_amount, deleted_at, extended_until, extension_count,
			total_extended_minutes, created_at, updated_at
		FROM rental_sessions
		WHERE state = 'RUNNING'
			AND extended_until IS NOT NULL
			AND extended_until <= $1
			AND deleted_at IS NULL
		ORDER BY extended_until ASC
		LIMIT 100
	`

	rows, err := r.pool.Query(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to find expiring sessions: %w", err)
	}
	defer rows.Close()

	return r.collectSessionsWithSettlement(rows)
}

// UpdateExtension atomically updates session extension fields (16-01)
// Returns error if session not found, not RUNNING, or already deleted
func (r *RentalSessionRepository) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE rental_sessions
		SET extended_until = $1,
			extension_count = extension_count + 1,
			total_extended_minutes = total_extended_minutes + $2,
			updated_at = NOW()
		WHERE id = $3 AND state = 'RUNNING' AND deleted_at IS NULL
	`

	result, err := r.pool.Exec(ctx, query, extendedUntil, extensionMinutes, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update extension: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("session not found or not in RUNNING state: %s", sessionID)
	}

	return nil
}

// CreateExtensionRecord creates an audit record for a session extension (16-01)
// Returns existing record ID on idempotency key conflict, new ID otherwise
func (r *RentalSessionRepository) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO session_extensions (session_id, extended_by_minutes, cost_estimate, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id
	`

	var recordID string
	err := r.pool.QueryRow(ctx, query, sessionID, extensionMinutes, costEstimate, idempotencyKey).Scan(&recordID)
	if err != nil {
		// If no rows returned, it's a conflict - find existing record
		if err == pgx.ErrNoRows && idempotencyKey != "" {
			existingQuery := `SELECT id FROM session_extensions WHERE idempotency_key = $1`
			err = r.pool.QueryRow(ctx, existingQuery, idempotencyKey).Scan(&recordID)
			if err != nil {
				return "", fmt.Errorf("failed to find existing extension record: %w", err)
			}
			return recordID, nil
		}
		return "", fmt.Errorf("failed to create extension record: %w", err)
	}

	return recordID, nil
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
		&session.DeletedAt,
		&session.ExtendedUntil,
		&session.ExtensionCount,
		&session.TotalExtendedMinutes,
		&session.DockerImage,
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
			&session.DeletedAt,
			&session.ExtendedUntil,
			&session.ExtensionCount,
			&session.TotalExtendedMinutes,
			&session.DockerImage,
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

// collectSessionsWithSettlement collects rows into a slice of RentalSession (including settlement fields)
func (r *RentalSessionRepository) collectSessionsWithSettlement(rows pgx.Rows) ([]*domain.RentalSession, error) {
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
			&session.SettledAt,
			&session.SettledAmount,
			&session.DeletedAt,
			&session.ExtendedUntil,
			&session.ExtensionCount,
			&session.TotalExtendedMinutes,
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
