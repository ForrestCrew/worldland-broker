package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// BlockchainConfig holds blockchain connection settings for event listener
type BlockchainConfig struct {
	// RPCEndpoints contains WebSocket RPC endpoints for BNB Chain (comma-separated in env)
	RPCEndpoints []string
	// HTTPRPCEndpoint is the HTTP RPC endpoint for contract calls (balance queries)
	HTTPRPCEndpoint string
	// ContractAddress is the deployed WorldlandRental contract address
	ContractAddress string
	// DeploymentBlock is the block number when the contract was deployed (for initial backfill)
	DeploymentBlock uint64
	// ListenerEnabled toggles the event listener (useful for local development/testing)
	ListenerEnabled bool
}

// K8sConfig holds Kubernetes integration settings (Phase 22)
type K8sConfig struct {
	Enabled        bool   // Enable K8s integration
	KubeconfigPath string // Path to kubeconfig (empty for in-cluster)
	DefaultImage   string // Default GPU container image

	// K8s Join settings (Phase 29) - for nodes joining the cluster
	JoinEnabled bool   // Enable automatic K8s join for new nodes
	MasterIP    string // K8s master API server IP
	MasterPort  int    // K8s master API server port (default 6443)
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

	// Hub-to-Node mTLS Client Certificates (DEBT-01)
	NodeClientCertPath string
	NodeClientKeyPath  string

	// Auth
	SIWEDomain   string
	SessionTTL   time.Duration
	AuthDisabled bool // Disable auth for E2E testing

	// Blockchain
	Blockchain BlockchainConfig

	// K8s integration (Phase 22)
	K8s K8sConfig
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() *Config {
	// Parse DATABASE_URL if present (takes precedence for Docker/12-factor compatibility)
	var dbHost, dbUser, dbPassword, dbName string
	var dbPort int

	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		if u, err := url.Parse(databaseURL); err == nil {
			dbHost = u.Hostname()
			if portStr := u.Port(); portStr != "" {
				dbPort, _ = strconv.Atoi(portStr)
			}
			if u.User != nil {
				dbUser = u.User.Username()
				dbPassword, _ = u.User.Password()
			}
			dbName = strings.TrimPrefix(u.Path, "/")
		}
	}

	// Fall back to individual env vars if DATABASE_URL not set or parsing failed
	if dbHost == "" {
		dbHost = getEnv("DB_HOST", "localhost")
	}
	if dbPort == 0 {
		dbPort, _ = strconv.Atoi(getEnv("DB_PORT", "5432"))
	}
	if dbUser == "" {
		dbUser = getEnv("DB_USER", "worldland")
	}
	if dbPassword == "" {
		dbPassword = getEnv("DB_PASSWORD", "devpassword")
	}
	if dbName == "" {
		dbName = getEnv("DB_NAME", "worldland_hub")
	}

	deploymentBlock, _ := strconv.ParseUint(getEnv("CONTRACT_DEPLOYMENT_BLOCK", "0"), 10, 64)
	listenerEnabled := getEnv("BLOCKCHAIN_LISTENER_ENABLED", "true") == "true"

	// Parse RPC endpoints (comma-separated)
	rpcEndpointsStr := getEnv("BLOCKCHAIN_RPC_ENDPOINTS", "wss://bsc-ws-node.nariox.org:443")
	rpcEndpoints := strings.Split(rpcEndpointsStr, ",")
	for i := range rpcEndpoints {
		rpcEndpoints[i] = strings.TrimSpace(rpcEndpoints[i])
	}

	// K8s configuration (Phase 22)
	k8sEnabled := os.Getenv("K8S_ENABLED")
	k8sEnabledBool := k8sEnabled == "true" || k8sEnabled == "1"

	// K8s Join configuration (Phase 29)
	k8sJoinEnabled := os.Getenv("K8S_JOIN_ENABLED")
	k8sJoinEnabledBool := k8sJoinEnabled == "true" || k8sJoinEnabled == "1"
	k8sMasterPort, _ := strconv.Atoi(getEnv("K8S_MASTER_PORT", "6443"))

	return &Config{
		DBHost:     dbHost,
		DBPort:     dbPort,
		DBUser:     dbUser,
		DBPassword: dbPassword,
		DBName:     dbName,

		ServerPort: getEnv("SERVER_PORT", "8080"),
		MTLSPort:   getEnv("MTLS_PORT", "8443"),

		ACMEDirectoryURL: getEnv("ACME_DIRECTORY_URL", "https://localhost:9000/acme/acme/directory"),
		CACertPath:       getEnv("CA_CERT_PATH", "./dev/step-ca/certs/root_ca.crt"),
		CAKeyPath:        getEnv("CA_KEY_PATH", "./dev/step-ca/secrets/root_ca_key"),
		MTLSCertPath:     getEnv("MTLS_CERT_PATH", "./dev/certs/hub.crt"),
		MTLSKeyPath:      getEnv("MTLS_KEY_PATH", "./dev/certs/hub.key"),
		CertTTL:          24 * time.Hour,

		// Hub-to-Node mTLS Client Certificates (DEBT-01)
		NodeClientCertPath: getEnv("NODE_CLIENT_CERT_PATH", "certs/hub-client.crt"),
		NodeClientKeyPath:  getEnv("NODE_CLIENT_KEY_PATH", "certs/hub-client.key"),

		SIWEDomain:   getEnv("SIWE_DOMAIN", "hub.worldland.io"),
		SessionTTL:   24 * time.Hour,
		AuthDisabled: os.Getenv("AUTH_DISABLED") == "true",

		Blockchain: BlockchainConfig{
			RPCEndpoints:    rpcEndpoints,
			HTTPRPCEndpoint: getEnv("BLOCKCHAIN_HTTP_RPC", "https://bsc-dataseed1.binance.org"),
			ContractAddress: getEnv("CONTRACT_ADDRESS", ""),
			DeploymentBlock: deploymentBlock,
			ListenerEnabled: listenerEnabled,
		},

		K8s: K8sConfig{
			Enabled:        k8sEnabledBool,
			KubeconfigPath: os.Getenv("KUBECONFIG"),
			DefaultImage:   getEnv("K8S_DEFAULT_IMAGE", "ubuntu:22.04"),
			JoinEnabled:    k8sJoinEnabledBool,
			MasterIP:       os.Getenv("K8S_MASTER_IP"),
			MasterPort:     k8sMasterPort,
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
