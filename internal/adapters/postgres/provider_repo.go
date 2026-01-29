package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// PostgresProviderRepository implements domain.ProviderRepository
type PostgresProviderRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.ProviderRepository = (*PostgresProviderRepository)(nil)

// NewProviderRepository creates a new PostgreSQL provider repository
func NewProviderRepository(pool *pgxpool.Pool) *PostgresProviderRepository {
	return &PostgresProviderRepository{pool: pool}
}

// Create inserts a new provider
func (r *PostgresProviderRepository) Create(ctx context.Context, provider *domain.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO providers (id, wallet_address, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.pool.Exec(ctx, query,
		provider.ID,
		provider.WalletAddress,
		provider.Status,
		provider.CreatedAt,
		provider.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	return nil
}

// GetByID retrieves a provider by ID
func (r *PostgresProviderRepository) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, wallet_address, status, created_at, updated_at
		FROM providers
		WHERE id = $1
	`

	var provider domain.Provider
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&provider.ID,
		&provider.WalletAddress,
		&provider.Status,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get provider by ID: %w", err)
	}

	return &provider, nil
}

// GetByWallet retrieves a provider by wallet address
func (r *PostgresProviderRepository) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, wallet_address, status, created_at, updated_at
		FROM providers
		WHERE wallet_address = $1
	`

	var provider domain.Provider
	err := r.pool.QueryRow(ctx, query, walletAddress).Scan(
		&provider.ID,
		&provider.WalletAddress,
		&provider.Status,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get provider by wallet: %w", err)
	}

	return &provider, nil
}

// Update updates an existing provider
func (r *PostgresProviderRepository) Update(ctx context.Context, provider *domain.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE providers
		SET wallet_address = $2, status = $3, updated_at = $4
		WHERE id = $1
	`

	_, err := r.pool.Exec(ctx, query,
		provider.ID,
		provider.WalletAddress,
		provider.Status,
		provider.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}

	return nil
}
