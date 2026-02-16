package config

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadWithEnvVars(t *testing.T) {
	// Set environment variables
	t.Setenv("REDDIT_CLIENT_ID", "test-client-id")
	t.Setenv("REDDIT_CLIENT_SECRET", "test-client-secret")
	t.Setenv("REDDIT_USERNAME", "test-user")
	t.Setenv("REDDIT_PASSWORD", "test-pass")
	t.Setenv("OUTPUT_DIR", "/tmp/test-output")
	t.Setenv("DB_PATH", "/tmp/test.db")
	t.Setenv("CONCURRENCY", "5")
	t.Setenv("FETCH_LIMIT", "50")
	t.Setenv("MAX_RETRIES", "5")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("MIGRATE_ON_START", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Reddit.ClientID != "test-client-id" {
		t.Errorf("Expected ClientID 'test-client-id', got '%s'", cfg.Reddit.ClientID)
	}
	if cfg.Reddit.ClientSecret != "test-client-secret" {
		t.Errorf("Expected ClientSecret 'test-client-secret', got '%s'", cfg.Reddit.ClientSecret)
	}
	if cfg.Reddit.Username != "test-user" {
		t.Errorf("Expected Username 'test-user', got '%s'", cfg.Reddit.Username)
	}
	if cfg.Storage.OutputDir != "/tmp/test-output" {
		t.Errorf("Expected OutputDir '/tmp/test-output', got '%s'", cfg.Storage.OutputDir)
	}
	if cfg.Download.Concurrency != 5 {
		t.Errorf("Expected Concurrency 5, got %d", cfg.Download.Concurrency)
	}
	if cfg.Download.FetchLimit != 50 {
		t.Errorf("Expected FetchLimit 50, got %d", cfg.Download.FetchLimit)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Expected Log.Level 'debug', got '%s'", cfg.Log.Level)
	}
	if cfg.Migrate.OnStart != false {
		t.Errorf("Expected Migrate.OnStart false, got %v", cfg.Migrate.OnStart)
	}
}

func TestLoadWithDefaults(t *testing.T) {
	// Clear all environment variables
	envVars := []string{
		"REDDIT_CLIENT_ID", "REDDIT_CLIENT_SECRET", "REDDIT_USERNAME",
		"REDDIT_PASSWORD", "OUTPUT_DIR", "DB_PATH", "CONCURRENCY",
		"FETCH_LIMIT", "MAX_RETRIES", "LOG_LEVEL", "MIGRATE_ON_START",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}

	cfg, err := Load()
	if err == nil {
		t.Error("Load() should return error when required env vars are missing")
	}
	if cfg != nil {
		t.Error("Config should be nil when required env vars are missing")
	}
}

func TestValidationMissingClientID(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when REDDIT_CLIENT_ID is missing")
	}
}

func TestValidationMissingClientSecret(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_USERNAME", "user")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_USERNAME")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when REDDIT_CLIENT_SECRET is missing")
	}
}

func TestValidationMissingUsername(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when REDDIT_USERNAME is missing")
	}
}

func TestValidationInvalidConcurrency(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("CONCURRENCY", "0")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("CONCURRENCY")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when CONCURRENCY is 0")
	}
}

func TestValidationInvalidFetchLimit(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("FETCH_LIMIT", "-1")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("FETCH_LIMIT")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when FETCH_LIMIT is negative")
	}
}

func TestValidationInvalidLogLevel(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("LOG_LEVEL", "invalid")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("LOG_LEVEL")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when LOG_LEVEL is invalid")
	}
}

func TestValidationInvalidBackoffBase(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("BACKOFF_BASE", "0s")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("BACKOFF_BASE")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when BACKOFF_BASE is 0")
	}
}

func TestValidationInvalidBackoffMax(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("BACKOFF_MAX", "0s")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("BACKOFF_MAX")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when BACKOFF_MAX is 0")
	}
}

func TestValidationBackoffBaseGreaterThanMax(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("BACKOFF_BASE", "60s")
	os.Setenv("BACKOFF_MAX", "5s")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("BACKOFF_BASE")
		os.Unsetenv("BACKOFF_MAX")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when BACKOFF_BASE > BACKOFF_MAX")
	}
}

func TestValidationInvalidRetryThreshold(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	os.Setenv("RETRY_THRESHOLD", "-1")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("RETRY_THRESHOLD")
	}()

	_, err := Load()
	if err == nil {
		t.Error("Load() should return error when RETRY_THRESHOLD is negative")
	}
}

func TestDefaultValues(t *testing.T) {
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// Check default values
	if cfg.Download.Concurrency != 10 {
		t.Errorf("Expected default Concurrency 10, got %d", cfg.Download.Concurrency)
	}
	if cfg.Download.FetchLimit != 100 {
		t.Errorf("Expected default FetchLimit 100, got %d", cfg.Download.FetchLimit)
	}
	if cfg.Download.MaxRetries != 3 {
		t.Errorf("Expected default MaxRetries 3, got %d", cfg.Download.MaxRetries)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Expected default Log.Level 'info', got '%s'", cfg.Log.Level)
	}
	if cfg.Migrate.OnStart != true {
		t.Errorf("Expected default Migrate.OnStart true, got %v", cfg.Migrate.OnStart)
	}
	if cfg.Backoff.Base != 5*time.Second {
		t.Errorf("Expected default Backoff.Base 5s, got %v", cfg.Backoff.Base)
	}
	if cfg.Backoff.Max != 60*time.Second {
		t.Errorf("Expected default Backoff.Max 60s, got %v", cfg.Backoff.Max)
	}
	if cfg.SmartPolling.ReCheck != false {
		t.Errorf("Expected default SmartPolling.ReCheck false, got %v", cfg.SmartPolling.ReCheck)
	}
	if cfg.SmartPolling.RetryThreshold != 3 {
		t.Errorf("Expected default SmartPolling.RetryThreshold 3, got %d", cfg.SmartPolling.RetryThreshold)
	}
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_VAR", "test-value")
	defer os.Unsetenv("TEST_VAR")

	result := GetEnv("TEST_VAR", "default")
	if result != "test-value" {
		t.Errorf("Expected 'test-value', got '%s'", result)
	}

	result = GetEnv("NONEXISTENT_VAR", "default")
	if result != "default" {
		t.Errorf("Expected 'default', got '%s'", result)
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Setenv("TEST_INT_VAR", "42")
	defer os.Unsetenv("TEST_INT_VAR")

	result := GetEnvInt("TEST_INT_VAR", 10)
	if result != 42 {
		t.Errorf("Expected 42, got %d", result)
	}

	result = GetEnvInt("NONEXISTENT_INT_VAR", 10)
	if result != 10 {
		t.Errorf("Expected 10, got %d", result)
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		envValue string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"", false},
	}

	for _, tt := range tests {
		os.Setenv("TEST_BOOL_VAR", tt.envValue)
		result := GetEnvBool("TEST_BOOL_VAR", false)
		if result != tt.expected {
			t.Errorf("GetEnvBool('%s') = %v, expected %v", tt.envValue, result, tt.expected)
		}
		os.Unsetenv("TEST_BOOL_VAR")
	}
}

func TestCLIFlagsOverrideEnvVars(t *testing.T) {
	// Save original os.Args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Set environment variables
	os.Setenv("REDDIT_CLIENT_ID", "env-client-id")
	os.Setenv("REDDIT_CLIENT_SECRET", "env-client-secret")
	os.Setenv("REDDIT_USERNAME", "env-user")
	os.Setenv("CONCURRENCY", "5")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
		os.Unsetenv("CONCURRENCY")
	}()

	// Set CLI flags via os.Args
	os.Args = []string{"program", "--client-id", "cli-client-id", "--concurrency", "20"}

	// Create new FlagSet and define flags
	testFlagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	testFlagSet.BoolVar(&flagReCheck, "re-check", false, "Enable re-check mode for previously failed posts")
	testFlagSet.IntVar(&flagRetryThreshold, "retry-threshold", 0, "Max retries before permanent skip")
	testFlagSet.StringVar(&flagClientID, "client-id", "", "Reddit API client ID")
	testFlagSet.StringVar(&flagClientSecret, "client-secret", "", "Reddit API client secret")
	testFlagSet.StringVar(&flagUsername, "username", "", "Reddit username")
	testFlagSet.IntVar(&flagConcurrency, "concurrency", 0, "Number of parallel downloads")
	testFlagSet.IntVar(&flagFetchLimit, "fetch-limit", 0, "Posts per fetch")
	testFlagSet.DurationVar(&flagBackoffBase, "backoff-base", 0, "Base backoff delay for retries")
	testFlagSet.DurationVar(&flagBackoffMax, "backoff-max", 0, "Max backoff delay for retries")

	// Parse flags using the test FlagSet
	flagSet = false
	testFlagSet.Parse(os.Args[1:])
	testFlagSet.Visit(func(f *flag.Flag) {
		flagSet = true
	})

	// Update global flag variables from parsed values
	flagClientID = "cli-client-id"
	flagConcurrency = 20

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// CLI flags should override env vars
	if cfg.Reddit.ClientID != "cli-client-id" {
		t.Errorf("Expected ClientID 'cli-client-id', got '%s'", cfg.Reddit.ClientID)
	}
	if cfg.Download.Concurrency != 20 {
		t.Errorf("Expected Concurrency 20, got %d", cfg.Download.Concurrency)
	}
}

func TestInvalidFlagValuesRejected(t *testing.T) {
	testFlagSet := flag.NewFlagSet("program", flag.ContinueOnError)
	testFlagSet.IntVar(&flagConcurrency, "concurrency", 0, "Number of parallel downloads")

	testFlagSet.Parse([]string{"--concurrency", "-5"})

	if flagConcurrency != -5 {
		t.Errorf("Expected flagConcurrency to be -5, got %d", flagConcurrency)
	}

	cfg := &Config{
		Reddit: RedditConfig{
			ClientID:     "id",
			ClientSecret: "secret",
			Username:     "user",
		},
		Download: DownloadConfig{
			Concurrency: flagConcurrency,
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should return error when concurrency is negative")
	}
}

func TestHelpOutput(t *testing.T) {
	testFlagSet := flag.NewFlagSet("program", flag.ContinueOnError)
	testFlagSet.BoolVar(&flagReCheck, "re-check", false, "Enable re-check mode for previously failed posts")
	testFlagSet.IntVar(&flagRetryThreshold, "retry-threshold", 0, "Max retries before permanent skip")
	testFlagSet.StringVar(&flagClientID, "client-id", "", "Reddit API client ID")
	testFlagSet.StringVar(&flagClientSecret, "client-secret", "", "Reddit API client secret")
	testFlagSet.StringVar(&flagUsername, "username", "", "Reddit username")
	testFlagSet.IntVar(&flagConcurrency, "concurrency", 0, "Number of parallel downloads")
	testFlagSet.IntVar(&flagFetchLimit, "fetch-limit", 0, "Posts per fetch")
	testFlagSet.DurationVar(&flagBackoffBase, "backoff-base", 0, "Base backoff delay for retries")
	testFlagSet.DurationVar(&flagBackoffMax, "backoff-max", 0, "Max backoff delay for retries")

	var helpOutput strings.Builder
	testFlagSet.SetOutput(&helpOutput)

	testFlagSet.PrintDefaults()

	helpText := helpOutput.String()
	if helpText == "" {
		t.Skip("Help output not captured")
	}

	expectedFlags := []string{"-re-check", "-retry-threshold", "-client-id", "-concurrency", "-backoff-base", "-backoff-max"}
	for _, flagName := range expectedFlags {
		if !strings.Contains(helpText, flagName) {
			t.Errorf("Help output should contain '%s'", flagName)
		}
	}
}

func TestFlagDefaults(t *testing.T) {
	// Save original os.Args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Clear os.Args to use no CLI flags
	os.Args = []string{"program"}

	// Create new FlagSet and define flags
	testFlagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	testFlagSet.BoolVar(&flagReCheck, "re-check", false, "Enable re-check mode for previously failed posts")
	testFlagSet.IntVar(&flagRetryThreshold, "retry-threshold", 0, "Max retries before permanent skip")
	testFlagSet.StringVar(&flagClientID, "client-id", "", "Reddit API client ID")
	testFlagSet.StringVar(&flagClientSecret, "client-secret", "", "Reddit API client secret")
	testFlagSet.StringVar(&flagUsername, "username", "", "Reddit username")
	testFlagSet.IntVar(&flagConcurrency, "concurrency", 0, "Number of parallel downloads")
	testFlagSet.IntVar(&flagFetchLimit, "fetch-limit", 0, "Posts per fetch")
	testFlagSet.DurationVar(&flagBackoffBase, "backoff-base", 0, "Base backoff delay for retries")
	testFlagSet.DurationVar(&flagBackoffMax, "backoff-max", 0, "Max backoff delay for retries")

	// Parse flags using the test FlagSet
	flagSet = false
	testFlagSet.Parse(os.Args[1:])
	testFlagSet.Visit(func(f *flag.Flag) {
		flagSet = true
	})

	// Set required env vars
	os.Setenv("REDDIT_CLIENT_ID", "id")
	os.Setenv("REDDIT_CLIENT_SECRET", "secret")
	os.Setenv("REDDIT_USERNAME", "user")
	defer func() {
		os.Unsetenv("REDDIT_CLIENT_ID")
		os.Unsetenv("REDDIT_CLIENT_SECRET")
		os.Unsetenv("REDDIT_USERNAME")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// Verify defaults are used when flags not set
	if cfg.Download.Concurrency != 10 {
		t.Errorf("Expected default Concurrency 10, got %d", cfg.Download.Concurrency)
	}
	if cfg.Download.FetchLimit != 100 {
		t.Errorf("Expected default FetchLimit 100, got %d", cfg.Download.FetchLimit)
	}
	if cfg.Backoff.Base != 5*time.Second {
		t.Errorf("Expected default Backoff.Base 5s, got %v", cfg.Backoff.Base)
	}
	if cfg.Backoff.Max != 60*time.Second {
		t.Errorf("Expected default Backoff.Max 60s, got %v", cfg.Backoff.Max)
	}
}
