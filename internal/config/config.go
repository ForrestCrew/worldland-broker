package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

func init() {
	// .env 파일 로드 (없어도 에러 무시 - K8s에서는 환경변수 직접 주입)
	_ = godotenv.Load()
}

// Config 구조체는 애플리케이션에서 사용하는 모든 설정값을 정의합니다.
type Config struct {
	Port           string // 프록시 서버가 리스닝할 포트
	K8sMasterURL   string // 쿠버네티스 API 서버 주소 (예: https://10.96.0.1:443)
	K8sToken       string // 쿠버네티스 ServiceAccount 토큰
	CACertPath     string // 쿠버네티스 CA 인증서 경로
	DebugMode      bool   // 디버그 로그 출력 여부
	JWTSecret      string // JWT 서명용 비밀키
	AllowedOrigins string // CORS 허용 Origin (쉼표 구분)
	IsInCluster    bool   // K8s 클러스터 내부 실행 여부

	// Redis 설정
	RedisHost string // Redis 호스트
	RedisPort int    // Redis 포트
	RedisPass string // Redis 비밀번호 (선택)

	// Orchestrator 설정
	EnableOrchestrator bool   // Provider Orchestrator 활성화 여부
	MasterPublicIP     string // 마스터 노드 Public IP (Provider join용)
	MasterAPIPort      int    // Kubernetes API 서버 포트

	// PostgreSQL 설정
	PostgresHost     string // PostgreSQL 호스트
	PostgresPort     int    // PostgreSQL 포트
	PostgresDB       string // 데이터베이스 이름
	PostgresUser     string // 사용자명
	PostgresPassword string // 비밀번호
	PostgresSSLMode  string // SSL 모드 (disable, require, verify-full)

	// Blockchain 설정 (BSC)
	EnableBlockchain  bool   // 블록체인 연동 활성화
	BlockchainRPCURL  string // BSC RPC URL
	BlockchainChainID int64  // Chain ID (56=BSC Mainnet, 97=Testnet)
	VaultAddress      string // GPUVault 컨트랙트 주소
	BackendPrivateKey string // 백엔드 서명용 Private Key (tx relay용)
}

// Load 함수는 환경 변수에서 설정값을 읽어 Config 객체를 반환합니다.
func Load() (*Config, error) {
	cfg := &Config{
		// 기본값 설정 (환경변수가 없을 경우 사용됨)
		Port:           getEnv("PROXY_PORT", "8080"),
		K8sMasterURL:   getEnv("K8S_MASTER_URL", ""),
		K8sToken:       getEnv("K8S_TOKEN", ""),
		CACertPath:     getEnv("K8S_CA_CERT_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		DebugMode:      getEnv("DEBUG_MODE", "false") == "true",
		JWTSecret:      getEnv("JWT_SECRET", "your-secret-key-change-in-production"),
		AllowedOrigins: getEnv("ALLOWED_ORIGINS", "*"), // 로컬 개발 시 기본 *
		IsInCluster:    false,

		// Redis
		RedisHost: getEnv("REDIS_HOST", "localhost"),
		RedisPort: getEnvInt("REDIS_PORT", 6379),
		RedisPass: getEnv("REDIS_PASSWORD", ""),

		// Orchestrator
		EnableOrchestrator: getEnv("ENABLE_ORCHESTRATOR", "false") == "true",
		MasterPublicIP:     getEnv("MASTER_PUBLIC_IP", ""),
		MasterAPIPort:      getEnvInt("MASTER_API_PORT", 6443),

		// PostgreSQL
		PostgresHost:     getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:     getEnvInt("POSTGRES_PORT", 5432),
		PostgresDB:       getEnv("POSTGRES_DB", "worldland"),
		PostgresUser:     getEnv("POSTGRES_USER", "worldland"),
		PostgresPassword: getEnv("POSTGRES_PASSWORD", ""),
		PostgresSSLMode:  getEnv("POSTGRES_SSL_MODE", "disable"),

		// Blockchain (BSC)
		EnableBlockchain:  getEnv("ENABLE_BLOCKCHAIN", "false") == "true",
		BlockchainRPCURL:  getEnv("BLOCKCHAIN_RPC_URL", "https://bsc-dataseed.binance.org/"),
		BlockchainChainID: int64(getEnvInt("BLOCKCHAIN_CHAIN_ID", 56)),
		VaultAddress:      getEnv("VAULT_ADDRESS", ""),
		BackendPrivateKey: getEnv("BACKEND_PRIVATE_KEY", ""),
	}

	// K8s 클러스터 내부 실행 감지 (ServiceAccount 토큰 파일 존재 여부)
	tokenPath := "/var/run/secrets/kubernetes.io/serviceaccount/token"
	if _, err := os.Stat(tokenPath); err == nil {
		cfg.IsInCluster = true
		log.Println("K8s 클러스터 내부 실행 감지됨")

		// 토큰이 환경변수에 없으면, 파드 내부의 기본 경로에서 읽기
		if cfg.K8sToken == "" {
			tokenBytes, err := os.ReadFile(tokenPath)
			if err == nil {
				cfg.K8sToken = strings.TrimSpace(string(tokenBytes))
			}
		}

		// K8s Master URL 자동 설정
		if cfg.K8sMasterURL == "" {
			cfg.K8sMasterURL = "https://kubernetes.default.svc"
		}
	} else {
		log.Println("로컬 환경 실행 감지됨")
	}

	return cfg, nil
}

// getEnv는 환경 변수 값을 가져오되, 값이 없으면 기본값(fallback)을 반환하는 헬퍼 함수입니다.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		// 공백 제거 후 반환
		return strings.TrimSpace(value)
	}
	return fallback
}

// getEnvInt는 환경 변수 값을 정수로 가져오되, 값이 없거나 파싱 실패 시 기본값을 반환합니다.
func getEnvInt(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return intVal
		}
	}
	return fallback
}
