// Reddit Media Downloader - Main entry point
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/user/reddit-media-downloader/internal/downloader"
	"github.com/user/reddit-media-downloader/internal/reddit"
	"github.com/user/reddit-media-downloader/internal/storage"
	"golang.org/x/oauth2"
)

// Config holds application configuration
type Config struct {
	RedditClientID     string
	RedditClientSecret string
	RedditUserAgent    string
	RedditUsername     string
	RedditPassword     string
	OutputDir          string
	DBPath             string
	Concurrency        int
	FetchLimit         int
	LogLevel           string
	MigrateOnStart     bool
}

// memoryTokenStore implements reddit.TokenStore with in-memory storage
type memoryTokenStore struct {
	token *oauth2.Token
}

func (m *memoryTokenStore) SaveToken(token *oauth2.Token) error {
	m.token = token
	return nil
}

func (m *memoryTokenStore) LoadToken() (*oauth2.Token, error) {
	return m.token, nil
}

func main() {
	// Load .env file if exists
	_ = godotenv.Load()

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Setup logging
	fmt.Printf("Log level: %s\n", config.LogLevel)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down gracefully...")
		cancel()
	}()

	// Open database
	db, err := storage.NewDB(config.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Auto-migrate on first run
	if config.MigrateOnStart {
		if err := runAutoMigration(ctx, db, config.OutputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Migration failed: %v\n", err)
		}
	}

	// Create Reddit client
	redditConfig := &reddit.Config{
		ClientID:     config.RedditClientID,
		ClientSecret: config.RedditClientSecret,
		UserAgent:    config.RedditUserAgent,
		Username:     config.RedditUsername,
		Password:     config.RedditPassword,
	}

	tokenStore := &memoryTokenStore{}
	redditClient, err := reddit.NewClient(redditConfig, tokenStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Reddit client: %v\n", err)
		os.Exit(1)
	}
	defer redditClient.Close()

	// Create downloader
	downloaderConfig := downloader.Config{
		OutputDir:   config.OutputDir,
		Concurrency: config.Concurrency,
	}
	dl := downloader.NewDownloader(downloaderConfig)

	// Main loop
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Shutdown complete")
			return
		default:
			if err := runCycle(ctx, db, redditClient, dl, config); err != nil {
				fmt.Fprintf(os.Stderr, "Cycle error: %v\n", err)
			}

			// Sleep for 1 hour
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Hour):
			}
		}
	}
}

// loadConfig loads configuration from environment variables
func loadConfig() (*Config, error) {
	config := &Config{
		RedditClientID:     getEnv("REDDIT_CLIENT_ID", ""),
		RedditClientSecret: getEnv("REDDIT_CLIENT_SECRET", ""),
		RedditUserAgent:    getEnv("REDDIT_USER_AGENT", ""),
		RedditUsername:     getEnv("REDDIT_USERNAME", ""),
		RedditPassword:     getEnv("REDDIT_PASSWORD", ""),
		OutputDir:          getEnv("OUTPUT_DIR", "./data/output"),
		DBPath:             getEnv("DB_PATH", "./data/posts.db"),
		Concurrency:        getEnvInt("CONCURRENCY", 10),
		FetchLimit:         getEnvInt("FETCH_LIMIT", 100),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		MigrateOnStart:     getEnvBool("MIGRATE_ON_START", true),
	}

	// Validate required fields
	if config.RedditClientID == "" {
		return nil, fmt.Errorf("REDDIT_CLIENT_ID is required")
	}
	if config.RedditClientSecret == "" {
		return nil, fmt.Errorf("REDDIT_CLIENT_SECRET is required")
	}
	if config.RedditUsername == "" {
		return nil, fmt.Errorf("REDDIT_USERNAME is required")
	}

	// Create directories
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(config.DBPath), 0755); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	return config, nil
}

// runAutoMigration imports existing bdfr-html data
func runAutoMigration(ctx context.Context, db *storage.DB, outputDir string) error {
	// Check if database is empty
	stats, err := db.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("getting stats: %w", err)
	}
	if stats.TotalPosts > 0 {
		// Database already has data, skip migration
		return nil
	}

	// Look for idList.txt
	idListPath := filepath.Join(filepath.Dir(outputDir), "idList.txt")
	if _, err := os.Stat(idListPath); err == nil {
		fmt.Printf("Migrating existing data from %s...\n", idListPath)
		count, err := db.ImportFromIDList(ctx, idListPath)
		if err != nil {
			return fmt.Errorf("importing idList: %w", err)
		}
		fmt.Printf("Migrated %d posts from idList.txt\n", count)
	}

	// Scan existing media files
	count, err := db.ImportFromDirectory(ctx, outputDir)
	if err != nil {
		return fmt.Errorf("importing media directory: %w", err)
	}
	if count > 0 {
		fmt.Printf("Migrated %d posts from media directory\n", count)
	}

	return nil
}

// runCycle performs one download cycle
func runCycle(ctx context.Context, db *storage.DB, client *reddit.Client, dl *downloader.Downloader, config *Config) error {
	fmt.Println("Starting download cycle...")

	// Fetch upvoted and saved posts
	upvoted, err := client.GetUpvoted(ctx, config.FetchLimit)
	if err != nil {
		return fmt.Errorf("fetching upvoted: %w", err)
	}

	saved, err := client.GetSaved(ctx, config.FetchLimit)
	if err != nil {
		return fmt.Errorf("fetching saved: %w", err)
	}

	fmt.Printf("Fetched %d upvoted and %d saved posts\n", len(upvoted), len(saved))

	// Combine all posts
	allPosts := append(upvoted, saved...)

	// Filter already downloaded
	var newPosts []storage.Post
	for _, post := range allPosts {
		downloaded, err := db.IsDownloaded(ctx, post.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking if downloaded: %v\n", err)
			continue
		}
		if !downloaded {
			newPosts = append(newPosts, post)
		}
	}

	fmt.Printf("Found %d new posts to download\n", len(newPosts))

	if len(newPosts) == 0 {
		fmt.Println("No new posts to download")
		return nil
	}

	// Convert storage.Post to reddit.RedditPost for extraction
	redditPosts := make([]reddit.RedditPost, len(newPosts))
	for i, post := range newPosts {
		redditPosts[i] = reddit.RedditPost{
			ID:        post.ID,
			Title:     post.Title,
			URL:       post.URL,
			Subreddit: post.Subreddit,
			Author:    post.Author,
		}
	}

	// Extract downloadable items
	items, err := dl.Extract(ctx, redditPosts)
	if err != nil {
		return fmt.Errorf("extracting media: %w", err)
	}

	fmt.Printf("Extracted %d downloadable items\n", len(items))

	// Download items
	if err := dl.Download(ctx, items); err != nil {
		return fmt.Errorf("downloading media: %w", err)
	}

	// Mark posts as downloaded
	for _, post := range newPosts {
		post.DownloadedAt = time.Now().Unix()
		if err := db.SavePost(ctx, &post); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving post: %v\n", err)
		}
	}

	fmt.Printf("Cycle complete: downloaded %d items\n", len(items))
	return nil
}

// getEnv gets environment variable with default
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt gets integer environment variable
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvBool gets boolean environment variable
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		lower := strings.ToLower(value)
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return defaultValue
}
