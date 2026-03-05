// Package config provides centralized configuration management
// Supports environment variables, .env files, and sensible defaults
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Reddit       RedditConfig
	Storage      StorageConfig
	Download     DownloadConfig
	Log          LogConfig
	Migrate      MigrateConfig
	Backoff      BackoffConfig
	SmartPolling SmartPollingConfig
	Auth         bool
}

// RedditConfig holds Reddit API credentials and settings
type RedditConfig struct {
	ClientID     string
	ClientSecret string
	UserAgent    string
	Username     string
	Password     string
	RefreshToken string
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
	OnStart           bool
	FullSyncOnce      bool
	SourceDir         string // Source directory containing media files to reorganize
	HTMLDir           string // Directory containing bdfr-html HTML files for metadata
	ReorganizeEnabled bool   // Enable file reorganization into subreddit folders
}

// BackoffConfig holds exponential backoff settings for retries
type BackoffConfig struct {
	Base time.Duration
	Max  time.Duration
}

// CalculateBackoffDelay calculates exponential backoff delay for retries
// Formula: baseDelay * (2^retryCount), capped at maxDelay
// Edge cases: negative retryCount returns 0, zero base returns 0
func CalculateBackoffDelay(retryCount int, base, max time.Duration) time.Duration {
	// Handle edge cases
	if retryCount < 0 {
		return 0
	}
	if base <= 0 {
		return 0
	}

	// Calculate delay using bit shift for efficiency: base * 2^retryCount
	delay := base * time.Duration(1<<uint(retryCount))

	// Cap at max delay
	if delay > max {
		return max
	}
	return delay
}

// SmartPollingConfig holds smart polling settings for re-checking posts
type SmartPollingConfig struct {
	ReCheck        bool
	RetryThreshold int
}

// Flag variables for CLI parsing
var (
	flagReCheck        bool
	flagRetryThreshold int
	flagClientID       string
	flagClientSecret   string
	flagUsername       string
	flagConcurrency    int
	flagFetchLimit     int
	flagBackoffBase    time.Duration
	flagBackoffMax     time.Duration
	flagSet            bool
	flagAuth           bool
)

func init() {
	// Define CLI flags - use zero values as defaults
	flag.BoolVar(&flagReCheck, "re-check", false, "Enable re-check mode for previously failed posts")
	flag.IntVar(&flagRetryThreshold, "retry-threshold", 0, "Max retries before permanent skip")
	flag.StringVar(&flagClientID, "client-id", "", "Reddit API client ID")
	flag.StringVar(&flagClientSecret, "client-secret", "", "Reddit API client secret")
	flag.StringVar(&flagUsername, "username", "", "Reddit username")
	flag.IntVar(&flagConcurrency, "concurrency", 0, "Number of parallel downloads")
	flag.IntVar(&flagFetchLimit, "fetch-limit", 0, "Posts per fetch")
	flag.DurationVar(&flagBackoffBase, "backoff-base", 0, "Base backoff delay for retries")
	flag.DurationVar(&flagBackoffMax, "backoff-max", 0, "Max backoff delay for retries")
	flag.BoolVar(&flagAuth, "auth", false, "Run OAuth2 authentication to get refresh token")
}

// flagWasSet returns true if a flag was explicitly provided on the command line
func flagWasSet() bool {
	// Check if any non-default flag values were set
	// We use flag.CommandLine.Lookup to check if flags were explicitly set
	flag.CommandLine.Visit(func(f *flag.Flag) {
		flagSet = true
	})
	return flagSet
}

// Load loads configuration from environment variables, .env file, and CLI flags
// Priority: CLI flags > Environment vars > .env file > defaults
func Load() (*Config, error) {
	// Load .env file if exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Parse CLI flags
	flag.Parse()

	cfg := &Config{
		Reddit: RedditConfig{
			ClientID:     getEnv("REDDIT_CLIENT_ID", ""),
			ClientSecret: getEnv("REDDIT_CLIENT_SECRET", ""),
			UserAgent:    getEnv("REDDIT_USER_AGENT", ""),
			Username:     getEnv("REDDIT_USERNAME", ""),
			Password:     getEnv("REDDIT_PASSWORD", ""),
			RefreshToken: getEnv("REDDIT_REFRESH_TOKEN", ""),
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
			OnStart:           getEnvBool("MIGRATE_ON_START", true),
			FullSyncOnce:      getEnvBool("FULL_SYNC_ONCE", true),
			SourceDir:         getEnv("MIGRATE_SOURCE_DIR", ""),
			HTMLDir:           getEnv("MIGRATE_HTML_DIR", ""),
			ReorganizeEnabled: getEnvBool("MIGRATE_REORGANIZE", false),
		},
		Backoff: BackoffConfig{
			Base: getEnvDuration("BACKOFF_BASE", 5*time.Second),
			Max:  getEnvDuration("BACKOFF_MAX", 60*time.Second),
		},
		SmartPolling: SmartPollingConfig{
			ReCheck:        getEnvBool("RE_CHECK", false),
			RetryThreshold: getEnvInt("RETRY_THRESHOLD", 3),
		},
	}

	// Apply CLI flag overrides (highest priority)
	// Only override if flags were explicitly provided on command line
	if flagWasSet() {
		if flagClientID != "" {
			cfg.Reddit.ClientID = flagClientID
		}
		if flagClientSecret != "" {
			cfg.Reddit.ClientSecret = flagClientSecret
		}
		if flagUsername != "" {
			cfg.Reddit.Username = flagUsername
		}
		if flagConcurrency > 0 {
			cfg.Download.Concurrency = flagConcurrency
		}
		if flagFetchLimit > 0 {
			cfg.Download.FetchLimit = flagFetchLimit
		}
		if flagBackoffBase > 0 {
			cfg.Backoff.Base = flagBackoffBase
		}
		if flagBackoffMax > 0 {
			cfg.Backoff.Max = flagBackoffMax
		}
		cfg.SmartPolling.ReCheck = flagReCheck
		if flagRetryThreshold > 0 {
			cfg.SmartPolling.RetryThreshold = flagRetryThreshold
		}
	}

	// Note: cfg.Auth is intentionally only set from CLI flags (--auth)
	// to prevent accidental auth mode when running as daemon.
	// Callers needing programmatic auth should call handleAuth() directly.
	// The flagAuth value was already applied above when flagWasSet() returned true.
	if flagWasSet() {
		cfg.Auth = flagAuth
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

	// Skip password/refresh token check when in auth mode
	if !c.Auth {
		// Require either password or refresh token
		if c.Reddit.Password == "" && c.Reddit.RefreshToken == "" {
			missing = append(missing, "REDDIT_PASSWORD or REDDIT_REFRESH_TOKEN")
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
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

	// Validate backoff settings
	if c.Backoff.Base <= 0 {
		return fmt.Errorf("BACKOFF_BASE must be greater than 0, got %v", c.Backoff.Base)
	}
	if c.Backoff.Max <= 0 {
		return fmt.Errorf("BACKOFF_MAX must be greater than 0, got %v", c.Backoff.Max)
	}
	if c.Backoff.Base > c.Backoff.Max {
		return fmt.Errorf("BACKOFF_BASE (%v) must be less than or equal to BACKOFF_MAX (%v)", c.Backoff.Base, c.Backoff.Max)
	}

	// Validate retry threshold
	if c.SmartPolling.RetryThreshold < 0 {
		return fmt.Errorf("RETRY_THRESHOLD must be greater than or equal to 0, got %d", c.SmartPolling.RetryThreshold)
	}

	// Validate migration configuration
	if c.Migrate.ReorganizeEnabled && c.Migrate.SourceDir == "" {
		return fmt.Errorf("MIGRATE_SOURCE_DIR is required when MIGRATE_REORGANIZE is enabled")
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

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
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
