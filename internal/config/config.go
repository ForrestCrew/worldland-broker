package config

import (
	"os"
	"strconv"
	"time"
)

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
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() *Config {
	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "5432"))

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
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
