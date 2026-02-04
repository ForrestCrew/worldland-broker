package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/worldland/worldland-hub/internal/domain"
)

// PostgresNodeRepository implements domain.NodeRepository
type PostgresNodeRepository struct {
	pool *pgxpool.Pool
}

// Compile-time interface check
var _ domain.NodeRepository = (*PostgresNodeRepository)(nil)

// NewNodeRepository creates a new PostgreSQL node repository
func NewNodeRepository(pool *pgxpool.Pool) *PostgresNodeRepository {
	return &PostgresNodeRepository{pool: pool}
}

// Create inserts a new node
func (r *PostgresNodeRepository) Create(ctx context.Context, node *domain.Node) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO nodes (id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second, api_endpoint, status, certificate_expiry, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := r.pool.Exec(ctx, query,
		node.ID,
		node.ProviderID,
		node.GPUUUID,
		node.GPUType,
		node.MemoryGB,
		node.PricePerSecond,
		node.APIEndpoint,
		node.Status,
		node.CertificateExpiry,
		node.CreatedAt,
		node.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	return nil
}

// GetByID retrieves a node by ID
func (r *PostgresNodeRepository) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second, api_endpoint, status, certificate_expiry, created_at, updated_at
		FROM nodes
		WHERE id = $1
	`

	var node domain.Node
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&node.ID,
		&node.ProviderID,
		&node.GPUUUID,
		&node.GPUType,
		&node.MemoryGB,
		&node.PricePerSecond,
		&node.APIEndpoint,
		&node.Status,
		&node.CertificateExpiry,
		&node.CreatedAt,
		&node.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get node by ID: %w", err)
	}

	return &node, nil
}

// GetByGPUUUID retrieves a node by GPU UUID (for duplicate prevention)
func (r *PostgresNodeRepository) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second, api_endpoint, status, certificate_expiry, created_at, updated_at
		FROM nodes
		WHERE gpu_uuid = $1
	`

	var node domain.Node
	err := r.pool.QueryRow(ctx, query, gpuUUID).Scan(
		&node.ID,
		&node.ProviderID,
		&node.GPUUUID,
		&node.GPUType,
		&node.MemoryGB,
		&node.PricePerSecond,
		&node.APIEndpoint,
		&node.Status,
		&node.CertificateExpiry,
		&node.CreatedAt,
		&node.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get node by GPU UUID: %w", err)
	}

	return &node, nil
}

// GetByProvider retrieves all nodes for a provider
func (r *PostgresNodeRepository) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second, api_endpoint, status, certificate_expiry, created_at, updated_at
		FROM nodes
		WHERE provider_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by provider: %w", err)
	}
	defer rows.Close()

	var nodes []*domain.Node
	for rows.Next() {
		var node domain.Node
		err := rows.Scan(
			&node.ID,
			&node.ProviderID,
			&node.GPUUUID,
			&node.GPUType,
			&node.MemoryGB,
			&node.PricePerSecond,
			&node.APIEndpoint,
			&node.Status,
			&node.CertificateExpiry,
			&node.CreatedAt,
			&node.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}
		nodes = append(nodes, &node)
	}

	return nodes, nil
}

// Update updates an existing node
func (r *PostgresNodeRepository) Update(ctx context.Context, node *domain.Node) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE nodes
		SET gpu_type = $2, memory_gb = $3, price_per_second = $4, api_endpoint = $5, status = $6, certificate_expiry = $7, updated_at = $8
		WHERE id = $1
	`

	_, err := r.pool.Exec(ctx, query,
		node.ID,
		node.GPUType,
		node.MemoryGB,
		node.PricePerSecond,
		node.APIEndpoint,
		node.Status,
		node.CertificateExpiry,
		node.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

// Delete removes a node by ID
func (r *PostgresNodeRepository) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `DELETE FROM nodes WHERE id = $1`

	_, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete node: %w", err)
	}

	return nil
}

// ListActive retrieves all nodes with status 'active' that don't have RUNNING/PENDING rentals
// Implements matching.NodeLister interface for ProviderMatcher
func (r *PostgresNodeRepository) ListActive(ctx context.Context) ([]*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Exclude nodes with active (PENDING or RUNNING) rental sessions
	query := `
		SELECT n.id, n.provider_id, n.gpu_uuid, n.gpu_type, n.memory_gb, n.price_per_second, n.api_endpoint, n.status, n.certificate_expiry, n.created_at, n.updated_at
		FROM nodes n
		WHERE n.status = $1
		AND NOT EXISTS (
			SELECT 1 FROM rental_sessions rs
			WHERE rs.node_id = n.id
			AND rs.state IN ('PENDING', 'RUNNING')
			AND rs.deleted_at IS NULL
		)
		ORDER BY n.created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, domain.NodeStatusActive)
	if err != nil {
		return nil, fmt.Errorf("failed to list active nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*domain.Node
	for rows.Next() {
		var node domain.Node
		err := rows.Scan(
			&node.ID,
			&node.ProviderID,
			&node.GPUUUID,
			&node.GPUType,
			&node.MemoryGB,
			&node.PricePerSecond,
			&node.APIEndpoint,
			&node.Status,
			&node.CertificateExpiry,
			&node.CreatedAt,
			&node.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}
		nodes = append(nodes, &node)
	}

	return nodes, nil
}
