// Package db provides database connection utilities.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nubro999/worldland-gpu/internal/config"
)

// PostgresDB wraps a PostgreSQL connection pool.
type PostgresDB struct {
	Pool *pgxpool.Pool
}

// NewPostgresDB creates a new PostgreSQL connection pool.
func NewPostgresDB(ctx context.Context, cfg *config.Config) (*PostgresDB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.PostgresHost,
		cfg.PostgresPort,
		cfg.PostgresUser,
		cfg.PostgresPassword,
		cfg.PostgresDB,
		cfg.PostgresSSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres config: %w", err)
	}

	// Connection pool settings
	poolConfig.MaxConns = 25
	poolConfig.MinConns = 5
	poolConfig.MaxConnLifetime = 5 * time.Minute
	poolConfig.MaxConnIdleTime = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return &PostgresDB{Pool: pool}, nil
}

// Close closes the connection pool.
func (db *PostgresDB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
}

// Ping checks if the database is reachable.
func (db *PostgresDB) Ping(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}

// Migrate runs database migrations.
func (db *PostgresDB) Migrate(ctx context.Context) error {
	// Create providers table
	query := `
	CREATE TABLE IF NOT EXISTS providers (
		provider_id VARCHAR(64) PRIMARY KEY,
		wallet_addr VARCHAR(128),
		node_name VARCHAR(128),
		status VARCHAR(32) NOT NULL DEFAULT 'pending',
		
		-- System Spec
		hostname VARCHAR(128),
		os VARCHAR(128),
		kernel_ver VARCHAR(64),
		architecture VARCHAR(16),
		
		-- CPU
		cpu_model VARCHAR(128),
		cpu_cores INTEGER,
		cpu_threads INTEGER,
		
		-- Memory
		total_memory_mb BIGINT,
		
		-- GPU
		total_gpus INTEGER,
		gpu_model VARCHAR(128),
		gpu_memory_mb BIGINT,
		cuda_version VARCHAR(16),
		driver_version VARCHAR(32),
		
		-- Storage
		total_disk_gb BIGINT,
		available_disk_gb BIGINT,
		
		-- Network
		public_ip VARCHAR(45),
		private_ip VARCHAR(45),
		
		-- Capacity (공유 가능량)
		shared_gpu_count INTEGER,
		shared_cpu_cores INTEGER,
		shared_memory_mb BIGINT,
		gpu_price_per_hour DECIMAL(10,4),
		cpu_price_per_hour DECIMAL(10,4),
		
		-- Timestamps
		registered_at TIMESTAMP,
		joined_at TIMESTAMP,
		last_heartbeat TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	);

	-- Indexes for search performance
	CREATE INDEX IF NOT EXISTS idx_providers_status ON providers(status);
	CREATE INDEX IF NOT EXISTS idx_providers_gpu_model ON providers(gpu_model);
	CREATE INDEX IF NOT EXISTS idx_providers_cpu_cores ON providers(cpu_cores);
	CREATE INDEX IF NOT EXISTS idx_providers_memory ON providers(total_memory_mb);
	CREATE INDEX IF NOT EXISTS idx_providers_disk ON providers(available_disk_gb);
	`

	_, err := db.Pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}
