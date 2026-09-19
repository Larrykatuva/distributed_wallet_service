package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/katuva/wallet/dpk/logger"
)

type Config struct {
	// Servers
	Port     string
	GrpcPort string

	// HTTP hardening
	CorsAllowedOrigins []string
	ShutdownTimeout    time.Duration

	// Cache (Redis). Empty RedisAddr disables caching.
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	CacheTTL      time.Duration

	// Recovery of interrupted transactions
	TransactionTimeout    time.Duration // per-stage wait for wallet replies
	RecoveryStaleAfter    time.Duration // incomplete + unchanged for this long → re-drive
	RecoverySweepInterval time.Duration
	RecoveryMaxAttempts   int

	// Actor system
	ClusterMode     string // "single" or "cluster"
	ClusterName     string
	ClusterPort     int
	AdvertisedHost  string // this node's routable IP; critical in k8s
	AutomanagedPort int    // discovery port for single-node (automanaged) mode

	// K8s provider (only used when ClusterMode = cluster)
	K8sNamespace string

	// Connection pooler. When UsePgBouncer is true the application pool talks
	// to PgBouncer; migrations and other session-level work always go direct.
	UsePgBouncer      bool
	PgBouncerHost     string
	PgBouncerPort     string
	PgBouncerPoolMode string // session | transaction (affects prepared statements)

	// Database
	DbHost     string
	DbUser     string
	DbPassword string
	DbName     string
	DbPort     string
	DbSSLMode  string
}

// Load reads .env (if present) and then the process environment.
func Load() *Config {
	if err := godotenv.Load(); err != nil {
		logger.InfoLog.Println("no .env file loaded, using process environment")
	}

	return &Config{
		Port:     getEnv("PORT", "3003"),
		GrpcPort: getEnv("GRPC_PORT", "3004"),

		CorsAllowedOrigins: getEnvList("CORS_ALLOWED_ORIGINS", []string{"http://localhost:*"}),
		ShutdownTimeout:    getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		RedisAddr:     getEnv("REDIS_ADDR", getEnv("REDIS_URL", "")),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),
		CacheTTL:      getEnvDuration("CACHE_TTL", 5*time.Minute),

		TransactionTimeout:    getEnvDuration("TRANSACTION_TIMEOUT", 30*time.Second),
		RecoveryStaleAfter:    getEnvDuration("RECOVERY_STALE_AFTER", 2*time.Minute),
		RecoverySweepInterval: getEnvDuration("RECOVERY_SWEEP_INTERVAL", 30*time.Second),
		RecoveryMaxAttempts:   getEnvInt("RECOVERY_MAX_ATTEMPTS", 5),

		ClusterMode:     getEnv("CLUSTER_MODE", "single"),
		ClusterName:     getEnv("CLUSTER_NAME", "wallet-cluster"),
		ClusterPort:     getEnvInt("CLUSTER_PORT", 8090),
		AdvertisedHost:  getEnv("ADVERTISED_HOST", "127.0.0.1"),
		AutomanagedPort: getEnvInt("AUTOMANAGED_PORT", 6330),
		K8sNamespace:    getEnv("K8S_NAMESPACE", "default"),

		UsePgBouncer:      getEnvBool("USE_PGBOUNCER", false),
		PgBouncerHost:     getEnv("PGBOUNCER_HOST", "127.0.0.1"),
		PgBouncerPort:     getEnv("PGBOUNCER_PORT", "6432"),
		PgBouncerPoolMode: strings.ToLower(getEnv("PGBOUNCER_POOL_MODE", "transaction")),

		DbHost:     getEnv("DB_HOST", "127.0.0.1"),
		DbUser:     getEnv("DB_USER", "postgres"),
		DbPassword: getEnv("DB_PASSWORD", ""),
		DbName:     getEnv("DB_NAME", "wallet_database"),
		DbPort:     getEnv("DB_PORT", "5432"),
		DbSSLMode:  getEnv("DB_SSLMODE", "disable"),
	}
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		logger.WarningLog.Printf("config: %s=%q is not a boolean, using %v", key, v, fallback)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
		logger.WarningLog.Printf("config: %s=%q is not an integer, using %d", key, v, fallback)
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		logger.WarningLog.Printf("config: %s=%q is not a duration, using %s", key, v, fallback)
	}
	return fallback
}

func getEnvList(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
