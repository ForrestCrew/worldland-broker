// Package provider provides repository for provider data persistence.
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProviderRepository defines the interface for provider data access.
type ProviderRepository interface {
	// CRUD operations
	Create(ctx context.Context, p *ProviderState) error
	Update(ctx context.Context, p *ProviderState) error
	Delete(ctx context.Context, providerID string) error
	GetByID(ctx context.Context, providerID string) (*ProviderState, error)

	// Query operations
	Search(ctx context.Context, filter *SearchFilter) ([]*ProviderState, error)
	ListAll(ctx context.Context) ([]*ProviderState, error)

	// Heartbeat operations
	UpdateHeartbeat(ctx context.Context, providerID string, status RegistrationStatus) error
	GetStaleProviders(ctx context.Context, threshold time.Duration) ([]*ProviderState, error)
}

// SearchFilter defines search criteria for providers.
type SearchFilter struct {
	Status          string  // available, offline, etc.
	GPUModel        string  // partial match (LIKE)
	MinMemoryMB     int64   // minimum RAM
	MinCPUCores     int     // minimum CPU cores
	MinDiskGB       int64   // minimum available disk
	MaxPricePerHour float64 // maximum GPU price per hour
	Limit           int     // max results (default 100)
	Offset          int     // pagination offset
}

// PostgresRepository implements ProviderRepository using PostgreSQL.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgreSQL repository.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Create inserts a new provider record.
func (r *PostgresRepository) Create(ctx context.Context, p *ProviderState) error {
	query := `
		INSERT INTO providers (
			provider_id, wallet_addr, node_name, status,
			hostname, os, kernel_ver, architecture,
			cpu_model, cpu_cores, cpu_threads,
			total_memory_mb, total_gpus, gpu_model, gpu_memory_mb,
			cuda_version, driver_version,
			total_disk_gb, available_disk_gb,
			public_ip, private_ip,
			shared_gpu_count, shared_cpu_cores, shared_memory_mb,
			gpu_price_per_hour, cpu_price_per_hour,
			registered_at, joined_at, last_heartbeat
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11,
			$12, $13, $14, $15,
			$16, $17,
			$18, $19,
			$20, $21,
			$22, $23, $24,
			$25, $26,
			$27, $28, $29
		)
		ON CONFLICT (provider_id) DO UPDATE SET
			wallet_addr = EXCLUDED.wallet_addr,
			node_name = EXCLUDED.node_name,
			status = EXCLUDED.status,
			hostname = EXCLUDED.hostname,
			os = EXCLUDED.os,
			kernel_ver = EXCLUDED.kernel_ver,
			architecture = EXCLUDED.architecture,
			cpu_model = EXCLUDED.cpu_model,
			cpu_cores = EXCLUDED.cpu_cores,
			cpu_threads = EXCLUDED.cpu_threads,
			total_memory_mb = EXCLUDED.total_memory_mb,
			total_gpus = EXCLUDED.total_gpus,
			gpu_model = EXCLUDED.gpu_model,
			gpu_memory_mb = EXCLUDED.gpu_memory_mb,
			cuda_version = EXCLUDED.cuda_version,
			driver_version = EXCLUDED.driver_version,
			total_disk_gb = EXCLUDED.total_disk_gb,
			available_disk_gb = EXCLUDED.available_disk_gb,
			public_ip = EXCLUDED.public_ip,
			private_ip = EXCLUDED.private_ip,
			shared_gpu_count = EXCLUDED.shared_gpu_count,
			shared_cpu_cores = EXCLUDED.shared_cpu_cores,
			shared_memory_mb = EXCLUDED.shared_memory_mb,
			gpu_price_per_hour = EXCLUDED.gpu_price_per_hour,
			cpu_price_per_hour = EXCLUDED.cpu_price_per_hour,
			updated_at = NOW()
	`

	// Extract GPU info
	gpuModel := ""
	gpuMemoryMB := int64(0)
	cudaVersion := ""
	driverVersion := ""
	if len(p.Spec.GPUs) > 0 {
		gpuModel = p.Spec.GPUs[0].Name
		gpuMemoryMB = p.Spec.GPUs[0].MemoryMB
		cudaVersion = p.Spec.GPUs[0].CUDAVersion
		driverVersion = p.Spec.GPUs[0].DriverVer
	}

	_, err := r.pool.Exec(ctx, query,
		p.ProviderID, p.WalletAddr, p.NodeName, string(p.Status),
		p.Spec.Hostname, p.Spec.OS, p.Spec.KernelVer, p.Spec.Architecture,
		p.Spec.CPUModel, p.Spec.CPUCores, p.Spec.CPUThreads,
		p.Spec.TotalMemoryMB, p.Spec.TotalGPUs, gpuModel, gpuMemoryMB,
		cudaVersion, driverVersion,
		p.Spec.TotalDiskGB, p.Spec.AvailableDiskGB,
		p.Spec.PublicIP, p.Spec.PrivateIP,
		p.Capacity.GPUCount, p.Capacity.CPUCores, p.Capacity.MemoryMB,
		p.Capacity.GPUPricePerHour, p.Capacity.CPUPricePerHour,
		p.RegisteredAt, p.JoinedAt, p.LastHeartbeat,
	)

	return err
}

// Update updates an existing provider record.
func (r *PostgresRepository) Update(ctx context.Context, p *ProviderState) error {
	return r.Create(ctx, p) // Uses UPSERT
}

// Delete removes a provider record.
func (r *PostgresRepository) Delete(ctx context.Context, providerID string) error {
	query := `DELETE FROM providers WHERE provider_id = $1`
	_, err := r.pool.Exec(ctx, query, providerID)
	return err
}

// GetByID retrieves a provider by ID.
func (r *PostgresRepository) GetByID(ctx context.Context, providerID string) (*ProviderState, error) {
	query := `
		SELECT 
			provider_id, wallet_addr, node_name, status,
			hostname, os, kernel_ver, architecture,
			cpu_model, cpu_cores, cpu_threads,
			total_memory_mb, total_gpus, gpu_model, gpu_memory_mb,
			cuda_version, driver_version,
			total_disk_gb, available_disk_gb,
			public_ip, private_ip,
			shared_gpu_count, shared_cpu_cores, shared_memory_mb,
			gpu_price_per_hour, cpu_price_per_hour,
			registered_at, joined_at, last_heartbeat
		FROM providers
		WHERE provider_id = $1
	`

	row := r.pool.QueryRow(ctx, query, providerID)
	return r.scanProvider(row)
}

// Search finds providers matching the filter criteria.
func (r *PostgresRepository) Search(ctx context.Context, filter *SearchFilter) ([]*ProviderState, error) {
	query := `
		SELECT 
			provider_id, wallet_addr, node_name, status,
			hostname, os, kernel_ver, architecture,
			cpu_model, cpu_cores, cpu_threads,
			total_memory_mb, total_gpus, gpu_model, gpu_memory_mb,
			cuda_version, driver_version,
			total_disk_gb, available_disk_gb,
			public_ip, private_ip,
			shared_gpu_count, shared_cpu_cores, shared_memory_mb,
			gpu_price_per_hour, cpu_price_per_hour,
			registered_at, joined_at, last_heartbeat
		FROM providers
		WHERE 1=1
	`

	args := []interface{}{}
	argIdx := 1

	// Build dynamic WHERE clause
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, filter.Status)
		argIdx++
	}

	if filter.GPUModel != "" {
		query += fmt.Sprintf(" AND gpu_model ILIKE $%d", argIdx)
		args = append(args, "%"+filter.GPUModel+"%")
		argIdx++
	}

	if filter.MinMemoryMB > 0 {
		query += fmt.Sprintf(" AND total_memory_mb >= $%d", argIdx)
		args = append(args, filter.MinMemoryMB)
		argIdx++
	}

	if filter.MinCPUCores > 0 {
		query += fmt.Sprintf(" AND cpu_cores >= $%d", argIdx)
		args = append(args, filter.MinCPUCores)
		argIdx++
	}

	if filter.MinDiskGB > 0 {
		query += fmt.Sprintf(" AND available_disk_gb >= $%d", argIdx)
		args = append(args, filter.MinDiskGB)
		argIdx++
	}

	if filter.MaxPricePerHour > 0 {
		query += fmt.Sprintf(" AND gpu_price_per_hour <= $%d", argIdx)
		args = append(args, filter.MaxPricePerHour)
		argIdx++
	}

	// Ordering and pagination
	query += " ORDER BY gpu_price_per_hour ASC NULLS LAST"

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", filter.Offset)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanProviders(rows)
}

// ListAll retrieves all providers.
func (r *PostgresRepository) ListAll(ctx context.Context) ([]*ProviderState, error) {
	return r.Search(ctx, &SearchFilter{Limit: 1000})
}

// UpdateHeartbeat updates the last heartbeat timestamp and status.
func (r *PostgresRepository) UpdateHeartbeat(ctx context.Context, providerID string, status RegistrationStatus) error {
	query := `
		UPDATE providers 
		SET last_heartbeat = NOW(), status = $2, updated_at = NOW()
		WHERE provider_id = $1
	`
	_, err := r.pool.Exec(ctx, query, providerID, string(status))
	return err
}

// GetStaleProviders finds providers with heartbeat older than threshold.
func (r *PostgresRepository) GetStaleProviders(ctx context.Context, threshold time.Duration) ([]*ProviderState, error) {
	query := `
		SELECT 
			provider_id, wallet_addr, node_name, status,
			hostname, os, kernel_ver, architecture,
			cpu_model, cpu_cores, cpu_threads,
			total_memory_mb, total_gpus, gpu_model, gpu_memory_mb,
			cuda_version, driver_version,
			total_disk_gb, available_disk_gb,
			public_ip, private_ip,
			shared_gpu_count, shared_cpu_cores, shared_memory_mb,
			gpu_price_per_hour, cpu_price_per_hour,
			registered_at, joined_at, last_heartbeat
		FROM providers
		WHERE status IN ('available', 'joined')
		  AND last_heartbeat < NOW() - $1::INTERVAL
	`

	rows, err := r.pool.Query(ctx, query, threshold.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanProviders(rows)
}

// scanProvider scans a single row into ProviderState.
func (r *PostgresRepository) scanProvider(row pgx.Row) (*ProviderState, error) {
	var p ProviderState
	var status string
	var gpuModel, cudaVersion, driverVersion, nodeName, walletAddr string
	var gpuMemoryMB, registeredAt, joinedAt, lastHeartbeat interface{}
	var hostname, os, kernelVer, arch, cpuModel, publicIP, privateIP string

	err := row.Scan(
		&p.ProviderID, &walletAddr, &nodeName, &status,
		&hostname, &os, &kernelVer, &arch,
		&cpuModel, &p.Spec.CPUCores, &p.Spec.CPUThreads,
		&p.Spec.TotalMemoryMB, &p.Spec.TotalGPUs, &gpuModel, &gpuMemoryMB,
		&cudaVersion, &driverVersion,
		&p.Spec.TotalDiskGB, &p.Spec.AvailableDiskGB,
		&publicIP, &privateIP,
		&p.Capacity.GPUCount, &p.Capacity.CPUCores, &p.Capacity.MemoryMB,
		&p.Capacity.GPUPricePerHour, &p.Capacity.CPUPricePerHour,
		&registeredAt, &joinedAt, &lastHeartbeat,
	)
	if err != nil {
		return nil, err
	}

	p.WalletAddr = walletAddr
	p.NodeName = nodeName
	p.Status = RegistrationStatus(status)
	p.Spec.Hostname = hostname
	p.Spec.OS = os
	p.Spec.KernelVer = kernelVer
	p.Spec.Architecture = arch
	p.Spec.CPUModel = cpuModel
	p.Spec.PublicIP = publicIP
	p.Spec.PrivateIP = privateIP

	// Handle nullable GPU fields
	if gpuModel != "" {
		gpu := GPUInfo{
			Name:        gpuModel,
			CUDAVersion: cudaVersion,
			DriverVer:   driverVersion,
		}
		if mem, ok := gpuMemoryMB.(int64); ok {
			gpu.MemoryMB = mem
		}
		p.Spec.GPUs = []GPUInfo{gpu}
	}

	// Handle nullable timestamps
	if t, ok := registeredAt.(time.Time); ok {
		p.RegisteredAt = t
	}
	if t, ok := joinedAt.(time.Time); ok {
		p.JoinedAt = t
	}
	if t, ok := lastHeartbeat.(time.Time); ok {
		p.LastHeartbeat = t
	}

	return &p, nil
}

// scanProviders scans multiple rows into ProviderState slice.
func (r *PostgresRepository) scanProviders(rows pgx.Rows) ([]*ProviderState, error) {
	var providers []*ProviderState

	for rows.Next() {
		var p ProviderState
		var status string
		var gpuModel, cudaVersion, driverVersion, nodeName, walletAddr *string
		var gpuMemoryMB *int64
		var hostname, os, kernelVer, arch, cpuModel, publicIP, privateIP *string
		var registeredAt, joinedAt, lastHeartbeat *time.Time

		err := rows.Scan(
			&p.ProviderID, &walletAddr, &nodeName, &status,
			&hostname, &os, &kernelVer, &arch,
			&cpuModel, &p.Spec.CPUCores, &p.Spec.CPUThreads,
			&p.Spec.TotalMemoryMB, &p.Spec.TotalGPUs, &gpuModel, &gpuMemoryMB,
			&cudaVersion, &driverVersion,
			&p.Spec.TotalDiskGB, &p.Spec.AvailableDiskGB,
			&publicIP, &privateIP,
			&p.Capacity.GPUCount, &p.Capacity.CPUCores, &p.Capacity.MemoryMB,
			&p.Capacity.GPUPricePerHour, &p.Capacity.CPUPricePerHour,
			&registeredAt, &joinedAt, &lastHeartbeat,
		)
		if err != nil {
			return nil, err
		}

		// Handle nullable string fields
		if walletAddr != nil {
			p.WalletAddr = *walletAddr
		}
		if nodeName != nil {
			p.NodeName = *nodeName
		}
		p.Status = RegistrationStatus(status)
		if hostname != nil {
			p.Spec.Hostname = *hostname
		}
		if os != nil {
			p.Spec.OS = *os
		}
		if kernelVer != nil {
			p.Spec.KernelVer = *kernelVer
		}
		if arch != nil {
			p.Spec.Architecture = *arch
		}
		if cpuModel != nil {
			p.Spec.CPUModel = *cpuModel
		}
		if publicIP != nil {
			p.Spec.PublicIP = *publicIP
		}
		if privateIP != nil {
			p.Spec.PrivateIP = *privateIP
		}

		// Handle GPU fields
		if gpuModel != nil && *gpuModel != "" {
			gpu := GPUInfo{
				Name: *gpuModel,
			}
			if cudaVersion != nil {
				gpu.CUDAVersion = *cudaVersion
			}
			if driverVersion != nil {
				gpu.DriverVer = *driverVersion
			}
			if gpuMemoryMB != nil {
				gpu.MemoryMB = *gpuMemoryMB
			}
			p.Spec.GPUs = []GPUInfo{gpu}
		}

		// Handle timestamps
		if registeredAt != nil {
			p.RegisteredAt = *registeredAt
		}
		if joinedAt != nil {
			p.JoinedAt = *joinedAt
		}
		if lastHeartbeat != nil {
			p.LastHeartbeat = *lastHeartbeat
		}

		providers = append(providers, &p)
	}

	return providers, rows.Err()
}
