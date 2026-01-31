// Package indexer provides configuration for the standalone indexer service.
package indexer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// IndexerConfig holds indexer-specific configuration loaded from environment variables.
type IndexerConfig struct {
	// Database connection
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	// Blockchain
	RPCEndpoints    []string
	ContractAddress string
	DeploymentBlock uint64 // Block to start indexing from on first run

	// Indexer settings
	FinalityBlocks uint64 // Blocks to wait before marking events as finalized
}

// LoadIndexerConfig loads configuration from environment variables.
// Environment variables:
//   - DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME (database connection)
//   - BLOCKCHAIN_RPC_ENDPOINTS (comma-separated WebSocket URLs)
//   - CONTRACT_ADDRESS (WorldlandRental contract address)
//   - CONTRACT_DEPLOYMENT_BLOCK (first block to index, default 0)
//   - FINALITY_BLOCKS (blocks to wait for finality, default 3 for BNB Chain post-Fermi)
func LoadIndexerConfig() *IndexerConfig {
	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))
	deploymentBlock, _ := strconv.ParseUint(getEnv("CONTRACT_DEPLOYMENT_BLOCK", "0"), 10, 64)
	finalityBlocks, _ := strconv.ParseUint(getEnv("FINALITY_BLOCKS", "3"), 10, 64)

	// Parse RPC endpoints (comma-separated)
	rpcEndpointsStr := getEnv("BLOCKCHAIN_RPC_ENDPOINTS", "wss://bsc-ws-node.nariox.org:443")
	rpcEndpoints := strings.Split(rpcEndpointsStr, ",")
	for i := range rpcEndpoints {
		rpcEndpoints[i] = strings.TrimSpace(rpcEndpoints[i])
	}

	return &IndexerConfig{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     dbPort,
		DBUser:     getEnv("DB_USER", "worldland"),
		DBPassword: getEnv("DB_PASSWORD", "devpassword"),
		DBName:     getEnv("DB_NAME", "worldland_hub"),

		RPCEndpoints:    rpcEndpoints,
		ContractAddress: getEnv("CONTRACT_ADDRESS", ""),
		DeploymentBlock: deploymentBlock,
		FinalityBlocks:  finalityBlocks,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// DatabaseURL returns the PostgreSQL connection string.
func (c *IndexerConfig) DatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}
