package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for filwizard
type Config struct {
	// Filecoin node connection
	RPC     string
	Token   string
	Timeout time.Duration

	// Wallet settings
	DefaultKeyType string
	MinBalance     int64 // attoFIL

	// Contract settings
	ContractTimeout time.Duration

	// Logging
	Verbose bool
}

// Load creates a new config from environment variables
func Load() *Config {
	return &Config{
		RPC:             getEnv("FILECOIN_RPC", "http://127.0.0.1:1234/rpc/v1"),
		Token:           getEnv("FILECOIN_TOKEN", "~/.lotus/token"),
		Timeout:         getDuration("FILECOIN_TIMEOUT", 30*time.Second),
		DefaultKeyType:  getEnv("DEFAULT_KEY_TYPE", "secp256k1"),
		MinBalance:      getInt64("MIN_WALLET_BALANCE", 1000000000000000000), // 1 FIL
		ContractTimeout: getDuration("CONTRACT_TIMEOUT", 5*time.Minute),
		Verbose:         getBool("VERBOSE", false),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getInt64(key string, fallback int64) int64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func getBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
