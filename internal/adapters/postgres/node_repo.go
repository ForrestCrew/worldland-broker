package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// nodeColumns is the standard column list for node queries
const nodeColumns = `id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second,
	api_endpoint, status, certificate_expiry,
	total_gpus, available_gpus, total_cpu_cores, total_memory_gb, k8s_node_name,
	gpu_model, vram_mb, driver_version, external_ip, max_storage_gb,
	created_at, updated_at`

// scanNode scans a single row into a domain.Node
func scanNode(row pgx.Row) (*domain.Node, error) {
	var node domain.Node
	err := row.Scan(
		&node.ID,
		&node.ProviderID,
		&node.GPUUUID,
		&node.GPUType,
		&node.MemoryGB,
		&node.PricePerSecond,
		&node.APIEndpoint,
		&node.Status,
		&node.CertificateExpiry,
		&node.TotalGPUs,
		&node.AvailableGPUs,
		&node.TotalCPUCores,
		&node.TotalMemoryGB,
		&node.K8sNodeName,
		&node.GPUModel,
		&node.VramMB,
		&node.DriverVersion,
		&node.ExternalIP,
		&node.MaxStorageGB,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// scanNodes scans multiple rows into a slice of domain.Node
func scanNodes(rows pgx.Rows) ([]*domain.Node, error) {
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
			&node.TotalGPUs,
			&node.AvailableGPUs,
			&node.TotalCPUCores,
			&node.TotalMemoryGB,
			&node.K8sNodeName,
			&node.GPUModel,
			&node.VramMB,
			&node.DriverVersion,
			&node.ExternalIP,
			&node.MaxStorageGB,
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

// Create inserts a new node
func (r *PostgresNodeRepository) Create(ctx context.Context, node *domain.Node) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		INSERT INTO nodes (id, provider_id, gpu_uuid, gpu_type, memory_gb, price_per_second,
			api_endpoint, status, certificate_expiry,
			total_gpus, available_gpus, total_cpu_cores, total_memory_gb, k8s_node_name,
			gpu_model, vram_mb, driver_version, external_ip, max_storage_gb,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
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
		node.TotalGPUs,
		node.AvailableGPUs,
		node.TotalCPUCores,
		node.TotalMemoryGB,
		node.K8sNodeName,
		node.GPUModel,
		node.VramMB,
		node.DriverVersion,
		node.ExternalIP,
		node.MaxStorageGB,
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

	query := fmt.Sprintf(`SELECT %s FROM nodes WHERE id = $1`, nodeColumns)

	node, err := scanNode(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return nil, fmt.Errorf("failed to get node by ID: %w", err)
	}

	return node, nil
}

// GetByGPUUUID retrieves a node by GPU UUID (for duplicate prevention)
func (r *PostgresNodeRepository) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`SELECT %s FROM nodes WHERE gpu_uuid = $1`, nodeColumns)

	node, err := scanNode(r.pool.QueryRow(ctx, query, gpuUUID))
	if err != nil {
		return nil, fmt.Errorf("failed to get node by GPU UUID: %w", err)
	}

	return node, nil
}

// GetByProvider retrieves all nodes for a provider
func (r *PostgresNodeRepository) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`SELECT %s FROM nodes WHERE provider_id = $1 ORDER BY created_at DESC`, nodeColumns)

	rows, err := r.pool.Query(ctx, query, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by provider: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

// Update updates an existing node
func (r *PostgresNodeRepository) Update(ctx context.Context, node *domain.Node) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		UPDATE nodes
		SET gpu_type = $2, memory_gb = $3, price_per_second = $4, api_endpoint = $5,
			status = $6, certificate_expiry = $7,
			total_gpus = $8, available_gpus = $9, total_cpu_cores = $10, total_memory_gb = $11,
			k8s_node_name = $12, gpu_model = $13, vram_mb = $14, driver_version = $15,
			external_ip = $16, max_storage_gb = $17, updated_at = $18
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
		node.TotalGPUs,
		node.AvailableGPUs,
		node.TotalCPUCores,
		node.TotalMemoryGB,
		node.K8sNodeName,
		node.GPUModel,
		node.VramMB,
		node.DriverVersion,
		node.ExternalIP,
		node.MaxStorageGB,
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
	query := fmt.Sprintf(`
		SELECT %s
		FROM nodes n
		WHERE n.status = $1
		AND NOT EXISTS (
			SELECT 1 FROM rental_sessions rs
			WHERE rs.node_id = n.id
			AND rs.state IN ('PENDING', 'RUNNING')
			AND rs.deleted_at IS NULL
		)
		ORDER BY n.created_at DESC
	`, "n.id, n.provider_id, n.gpu_uuid, n.gpu_type, n.memory_gb, n.price_per_second, n.api_endpoint, n.status, n.certificate_expiry, n.total_gpus, n.available_gpus, n.total_cpu_cores, n.total_memory_gb, n.k8s_node_name, n.gpu_model, n.vram_mb, n.driver_version, n.external_ip, n.max_storage_gb, n.created_at, n.updated_at")

	rows, err := r.pool.Query(ctx, query, domain.NodeStatusActive)
	if err != nil {
		return nil, fmt.Errorf("failed to list active nodes: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

// ListActiveGroupedByGPU aggregates active GPU nodes by GPU model for marketplace display
func (r *PostgresNodeRepository) ListActiveGroupedByGPU(ctx context.Context) ([]*domain.GPUTypeGroup, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT
			COALESCE(NULLIF(gpu_model, ''), gpu_type) as gpu_model,
			MAX(vram_mb) as vram_mb,
			SUM(total_gpus)::integer as total_gpus,
			SUM(available_gpus)::integer as available_gpus,
			COUNT(*)::integer as total_nodes,
			MIN(price_per_second) as min_price,
			MAX(price_per_second) as max_price,
			AVG(total_cpu_cores)::integer as avg_cpu,
			AVG(total_memory_gb)::integer as avg_memory
		FROM nodes
		WHERE status = $1 AND total_gpus > 0
		GROUP BY COALESCE(NULLIF(gpu_model, ''), gpu_type)
		ORDER BY SUM(available_gpus) DESC
	`

	rows, err := r.pool.Query(ctx, query, domain.NodeStatusActive)
	if err != nil {
		return nil, fmt.Errorf("failed to list GPU type groups: %w", err)
	}
	defer rows.Close()

	var groups []*domain.GPUTypeGroup
	for rows.Next() {
		var g domain.GPUTypeGroup
		err := rows.Scan(
			&g.GPUModel,
			&g.VramMB,
			&g.TotalGPUs,
			&g.AvailableGPUs,
			&g.TotalNodes,
			&g.MinPricePerSec,
			&g.MaxPricePerSec,
			&g.AvgCPUCores,
			&g.AvgMemoryGB,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan GPU type group: %w", err)
		}
		groups = append(groups, &g)
	}
	return groups, nil
}
