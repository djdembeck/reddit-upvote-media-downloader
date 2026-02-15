// Package config provides centralized configuration management
// Supports environment variables, .env files, and sensible defaults
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Reddit   RedditConfig
	Storage  StorageConfig
	Download DownloadConfig
	Log      LogConfig
	Migrate  MigrateConfig
}

// RedditConfig holds Reddit API credentials and settings
type RedditConfig struct {
	ClientID     string
	ClientSecret string
	UserAgent    string
	Username     string
	Password     string
}

// StorageConfig holds database and file storage settings
type StorageConfig struct {
	OutputDir string
	DBPath    string
}

// DownloadConfig holds downloader settings
type DownloadConfig struct {
	Concurrency int
	FetchLimit  int
	MaxRetries  int
}

// LogConfig holds logging configuration
type LogConfig struct {
	Level string
}

// MigrateConfig holds migration settings
type MigrateConfig struct {
	OnStart bool
}

// Load loads configuration from environment variables and .env file
// Priority: Environment vars > .env file > defaults
func Load() (*Config, error) {
	// Load .env file if exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	cfg := &Config{
		Reddit: RedditConfig{
			ClientID:     getEnv("REDDIT_CLIENT_ID", ""),
			ClientSecret: getEnv("REDDIT_CLIENT_SECRET", ""),
			UserAgent:    getEnv("REDDIT_USER_AGENT", ""),
			Username:     getEnv("REDDIT_USERNAME", ""),
			Password:     getEnv("REDDIT_PASSWORD", ""),
		},
		Storage: StorageConfig{
			OutputDir: getEnv("OUTPUT_DIR", "./data/output"),
			DBPath:    getEnv("DB_PATH", "./data/posts.db"),
		},
		Download: DownloadConfig{
			Concurrency: getEnvInt("CONCURRENCY", 10),
			FetchLimit:  getEnvInt("FETCH_LIMIT", 100),
			MaxRetries:  getEnvInt("MAX_RETRIES", 3),
		},
		Log: LogConfig{
			Level: getEnv("LOG_LEVEL", "info"),
		},
		Migrate: MigrateConfig{
			OnStart: getEnvBool("MIGRATE_ON_START", true),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
	var missing []string

	if c.Reddit.ClientID == "" {
		missing = append(missing, "REDDIT_CLIENT_ID")
	}
	if c.Reddit.ClientSecret == "" {
		missing = append(missing, "REDDIT_CLIENT_SECRET")
	}
	if c.Reddit.Username == "" {
		missing = append(missing, "REDDIT_USERNAME")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	// Validate numeric values
	if c.Download.Concurrency <= 0 {
		return fmt.Errorf("CONCURRENCY must be greater than 0, got %d", c.Download.Concurrency)
	}
	if c.Download.FetchLimit <= 0 {
		return fmt.Errorf("FETCH_LIMIT must be greater than 0, got %d", c.Download.FetchLimit)
	}

	// Validate log level
	validLogLevels := []string{"debug", "info", "warn", "error"}
	if !contains(validLogLevels, strings.ToLower(c.Log.Level)) {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error, got %s", c.Log.Level)
	}

	return nil
}

// GetEnv returns the value of an environment variable or a default
func GetEnv(key, defaultValue string) string {
	return getEnv(key, defaultValue)
}

// GetEnvInt returns an integer environment variable or a default
func GetEnvInt(key string, defaultValue int) int {
	return getEnvInt(key, defaultValue)
}

// GetEnvBool returns a boolean environment variable or a default
func GetEnvBool(key string, defaultValue bool) bool {
	return getEnvBool(key, defaultValue)
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		lower := strings.ToLower(value)
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return defaultValue
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
