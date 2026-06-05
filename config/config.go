package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/katuva/wallet/dpk/logger"
)

type Config struct {
	// System bootstrapping and startup
	Port string

	// Actor System
	ClusterMode    string // "single" or "cluster"
	ClusterName    string
	ClusterPort    int
	AdvertisedHost string // this node's IP — important in k8s

	// K8s provider (only used when ClusterMode = cluster)
	K8sNamespace string

	// Database connection
	DbHost     string
	DbUser     string
	DbPassword string
	DbName     string
	DbPort     string
}

func loadEnv() {
	err := godotenv.Load()
	if err != nil {
		logger.ErrorLog.Println(fmt.Sprintf("{ Failed to load env with error: %v }", err))
	} else {
		logger.InfoLog.Println("Env loaded successfully")
	}
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}

	return fallback
}

func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func Load() *Config {
	loadEnv()

	return &Config{
		Port: getEnv("PORT", "3000"),

		ClusterMode:    getEnv("CLUSTER_MODE", "single"),
		ClusterName:    getEnv("CLUSTER_NAME", "wallet-cluster"),
		ClusterPort:    getEnvInt("CLUSTER_PORT", 8090),
		AdvertisedHost: getEnv("ADVERTISED_HOST", "127.0.0.1"),
		K8sNamespace:   getEnv("K8S_NAMESPACE", "default"),

		DbHost:     getEnv("DB_HOST", "127.0.0.1"),
		DbUser:     getEnv("DB_USER", "postgres"),
		DbPassword: getEnv("DB_PASSWORD", "postgres"),
		DbName:     getEnv("DB_NAME", "wallet_database"),
		DbPort:     getEnv("DB_PORT", "5432"),
	}
}
