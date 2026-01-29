package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// BlockchainConfig holds blockchain connection settings for event listener
type BlockchainConfig struct {
	// RPCEndpoints contains WebSocket RPC endpoints for BNB Chain (comma-separated in env)
	RPCEndpoints []string
	// ContractAddress is the deployed WorldlandRental contract address
	ContractAddress string
	// DeploymentBlock is the block number when the contract was deployed (for initial backfill)
	DeploymentBlock uint64
	// ListenerEnabled toggles the event listener (useful for local development/testing)
	ListenerEnabled bool
}

// Config holds application configuration
type Config struct {
	// Database
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	// Server
	ServerPort string
	MTLSPort   string

	// ACME/CA
	ACMEDirectoryURL string
	CACertPath       string
	CAKeyPath        string
	MTLSCertPath     string
	MTLSKeyPath      string
	CertTTL          time.Duration

	// Auth
	SIWEDomain string
	SessionTTL time.Duration

	// Blockchain
	Blockchain BlockchainConfig
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() *Config {
	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))
	deploymentBlock, _ := strconv.ParseUint(getEnv("CONTRACT_DEPLOYMENT_BLOCK", "0"), 10, 64)
	listenerEnabled := getEnv("BLOCKCHAIN_LISTENER_ENABLED", "true") == "true"

	// Parse RPC endpoints (comma-separated)
	rpcEndpointsStr := getEnv("BLOCKCHAIN_RPC_ENDPOINTS", "wss://bsc-ws-node.nariox.org:443")
	rpcEndpoints := strings.Split(rpcEndpointsStr, ",")
	for i := range rpcEndpoints {
		rpcEndpoints[i] = strings.TrimSpace(rpcEndpoints[i])
	}

	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     dbPort,
		DBUser:     getEnv("DB_USER", "worldland"),
		DBPassword: getEnv("DB_PASSWORD", "devpassword"),
		DBName:     getEnv("DB_NAME", "worldland_hub"),

		ServerPort: getEnv("SERVER_PORT", "8080"),
		MTLSPort:   getEnv("MTLS_PORT", "8443"),

		ACMEDirectoryURL: getEnv("ACME_DIRECTORY_URL", "https://localhost:9000/acme/acme/directory"),
		CACertPath:       getEnv("CA_CERT_PATH", "./dev/step-ca/certs/root_ca.crt"),
		CAKeyPath:        getEnv("CA_KEY_PATH", "./dev/step-ca/secrets/root_ca_key"),
		MTLSCertPath:     getEnv("MTLS_CERT_PATH", "./dev/certs/hub.crt"),
		MTLSKeyPath:      getEnv("MTLS_KEY_PATH", "./dev/certs/hub.key"),
		CertTTL:          24 * time.Hour,

		SIWEDomain: getEnv("SIWE_DOMAIN", "hub.worldland.io"),
		SessionTTL: 24 * time.Hour,

		Blockchain: BlockchainConfig{
			RPCEndpoints:    rpcEndpoints,
			ContractAddress: getEnv("CONTRACT_ADDRESS", ""),
			DeploymentBlock: deploymentBlock,
			ListenerEnabled: listenerEnabled,
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
