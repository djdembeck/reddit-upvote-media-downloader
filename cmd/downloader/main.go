// Reddit Media Downloader - Main entry point
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/config"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/downloader"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/migration"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"golang.org/x/oauth2"
)

// parseSlogLevel converts a log level string to slog.Level.
func parseSlogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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

// buildTokenFromEnv builds an oauth2.Token from environment variables
func buildTokenFromEnv() *oauth2.Token {
	accessToken := os.Getenv("REDDIT_ACCESS_TOKEN")
	refreshToken := os.Getenv("REDDIT_REFRESH_TOKEN")

	if accessToken != "" && refreshToken != "" {
		return &oauth2.Token{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(1 * time.Hour),
		}
	}
	if refreshToken != "" {
		return &oauth2.Token{
			RefreshToken: refreshToken,
			TokenType:    "Bearer",
			Expiry:       time.Now(),
		}
	}
	if accessToken != "" {
		return &oauth2.Token{
			AccessToken: accessToken,
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(1 * time.Hour),
		}
	}
	return nil
}

// maskToken masks a token showing only the last 4 characters
func maskToken(token string) string {
	if len(token) > 4 {
		return "****" + token[len(token)-4:]
	}
	return "****"
}

func main() {
	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Handle --auth flag: run OAuth2 code flow to get refresh token
	if cfg.Auth {
		if err := handleAuth(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Setup logging
	fmt.Printf("Log level: %s\n", cfg.Log.Level)

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

	// Create output directories
	if err := os.MkdirAll(cfg.Storage.OutputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating data directory: %v\n", err)
		os.Exit(1)
	}

	// Open database
	db, err := storage.NewDB(cfg.Storage.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Auto-migrate on first run
	if cfg.Migrate.OnStart {
		if err := runAutoMigration(ctx, db, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Migration failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Run re-check mode if enabled
	if cfg.SmartPolling.ReCheck {
		if err := runReCheckMode(ctx, db); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Re-check failed: %v\n", err)
		}
	}

	// Create Reddit client
	redditConfig := &reddit.Config{
		ClientID:     cfg.Reddit.ClientID,
		ClientSecret: cfg.Reddit.ClientSecret,
		UserAgent:    cfg.Reddit.UserAgent,
		Username:     cfg.Reddit.Username,
		Password:     cfg.Reddit.Password,
		RefreshToken: cfg.Reddit.RefreshToken,
	}

	tokenStore := &memoryTokenStore{}

	// Check for existing OAuth tokens from environment variables
	token := buildTokenFromEnv()

	// Save token if one was built
	if token != nil {
		if err := tokenStore.SaveToken(token); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving token from env: %v\n", err)
		}
	}

	redditClient, err := reddit.NewClient(redditConfig, tokenStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Reddit client: %v\n", err)
		os.Exit(1)
	}
	defer redditClient.Close()

	// Parse log level from configuration
	parsedLevel := parseSlogLevel(cfg.Log.Level)

	slogLogger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parsedLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "timestamp"
			}
			if a.Key == slog.LevelKey {
				a.Key = "level"
			}
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			if a.Key == slog.SourceKey {
				a.Key = "source"
			}
			return a
		},
	}))
	slog.SetDefault(slogLogger)

	// Create downloader with structured slog logger
	downloaderConfig := downloader.Config{
		OutputDir:   cfg.Storage.OutputDir,
		Concurrency: cfg.Download.Concurrency,
		Logger:      slogLogger,
	}
	dl := downloader.NewDownloader(downloaderConfig, db)

	// Main loop
	for {
		select {
		case <-ctx.Done():
			slogLogger.Info("Shutdown complete")
			return
		default:
			if err := runCycle(ctx, db, redditClient, dl, cfg, slogLogger); err != nil {
				slogLogger.Error("Cycle error", "error", err)
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

func runAutoMigration(ctx context.Context, db *storage.DB, cfg *config.Config) error {
	outputDir := cfg.Storage.OutputDir

	migrationComplete, err := db.GetMetadata(ctx, "migration_complete")
	if err != nil {
		fmt.Printf("Warning: Could not check migration_complete metadata: %v\n", err)
	}
	if migrationComplete == "true" {
		fmt.Println("Migration already completed (migration_complete=true), skipping migration")
		return nil
	}

	stats, err := db.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("getting stats: %w", err)
	}
	if stats.TotalPosts > 0 {
		fmt.Printf("Database has %d posts, marking migration as complete\n", stats.TotalPosts)
		if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
			return fmt.Errorf("setting migration_complete metadata: %w", err)
		}
		return nil
	}

	if cfg.Migrate.ReorganizeEnabled {
		if cfg.Migrate.SourceDir == "" {
			return fmt.Errorf("migration cannot proceed: ReorganizeEnabled is true but SourceDir is empty; set MIGRATE_SOURCE_DIR environment variable")
		}
		if err := runFileReorganization(ctx, cfg.Migrate.SourceDir, outputDir, cfg.Migrate.HTMLDir, db); err != nil {
			return fmt.Errorf("file reorganization failed: %w", err)
		}
	}

	idListPath := filepath.Join(filepath.Dir(outputDir), "idList.txt")
	if _, err := os.Stat(idListPath); err == nil {
		fmt.Printf("Migrating existing data from %s...\n", idListPath)
		count, err := db.ImportFromIDList(ctx, idListPath)
		if err != nil {
			return fmt.Errorf("importing idList: %w", err)
		}
		fmt.Printf("Migrated %d posts from idList.txt\n", count)
	}

	count, err := db.ImportFromDirectory(ctx, outputDir)
	if err != nil {
		return fmt.Errorf("importing media directory: %w", err)
	}
	if count > 0 {
		fmt.Printf("Migrated %d posts from media directory\n", count)
	}

	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		return fmt.Errorf("setting migration_complete metadata: %w", err)
	}

	return nil
}

func runFileReorganization(ctx context.Context, sourceDir, destDir, htmlDir string, db *storage.DB) error {
	fmt.Println("===================")
	fmt.Println("File Reorganization")
	fmt.Println("===================")
	fmt.Printf("Source: %s\n", sourceDir)
	fmt.Printf("Destination: %s\n", destDir)
	fmt.Printf("HTML Directory: %s\n", htmlDir)
	fmt.Println()

	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := os.Stat(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source directory does not exist: %s", sourceDir)
		}
		return fmt.Errorf("checking source directory %s: %w", sourceDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path is not a directory: %s", sourceDir)
	}

	parser := migration.NewHTMLParser()
	if htmlDir != "" {
		fmt.Println("Parsing HTML files...")
		if err := parser.ParseHTMLFiles(ctx, htmlDir); err != nil {
			return fmt.Errorf("parsing HTML files: %w", err)
		}
	} else {
		indexPaths := []string{
			filepath.Join(filepath.Dir(sourceDir), "index.html"),
			filepath.Join(sourceDir, "index.html"),
		}
		for _, indexPath := range indexPaths {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := os.Stat(indexPath); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fmt.Errorf("checking index.html at %s: %w", indexPath, err)
			}
			fmt.Printf("Parsing index.html at %s...\n", indexPath)
			if err := parser.ParseIndexHTML(ctx, indexPath); err != nil {
				return fmt.Errorf("parsing index.html at %s: %w", indexPath, err)
			}
			if len(parser.PostMap) > 0 {
				break
			}
		}
		if len(parser.PostMap) == 0 {
			fmt.Println("Warning: No index.html found. Files will be organized as 'unknown' subreddit.")
		}
	}
	fmt.Printf("Found %d posts in HTML metadata\n\n", len(parser.PostMap))

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	logPath := filepath.Join(destDir, ".migration_log.json")
	migrator := migration.NewMigrator(sourceDir, destDir, parser.PostMap, false, db)
	if err := migrator.LoadExistingLog(ctx, logPath); err != nil {
		return fmt.Errorf("loading existing log: %w", err)
	}
	if err := migrator.Execute(ctx); err != nil {
		return fmt.Errorf("executing migration: %w", err)
	}

	if err := migrator.SaveLog(ctx, logPath); err != nil {
		return fmt.Errorf("saving migration log: %w", err)
	}

	fmt.Println("\nReorganization Summary")
	fmt.Println("======================")
	fmt.Printf("Total: %d\n", migrator.Log.TotalFiles)
	fmt.Printf("Moved: %d\n", migrator.Log.MovedCount)
	fmt.Printf("Skipped: %d\n", migrator.Log.SkippedCount)
	fmt.Printf("Warnings: %d\n", migrator.Log.WarningCount)
	fmt.Printf("Errors: %d\n", migrator.Log.ErrorCount)
	fmt.Printf("Log: %s\n", logPath)
	fmt.Println()

	return nil
}

// runReCheckMode verifies that all recorded files exist on disk and resets retry status for missing files.
// This is useful for recovering from partial downloads, disk corruption, or accidental file deletion.
func runReCheckMode(ctx context.Context, db *storage.DB) error {
	fmt.Println("Starting re-check mode...")
	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		return fmt.Errorf("getting all posts: %w", err)
	}
	var verifiedCount, missingCount int
	for _, post := range posts {
		if post.FilePath == "" {
			continue
		}
		_, err := os.Stat(post.FilePath)
		if err != nil {
			fmt.Printf("File missing: %s, resetting for re-download\n", post.FilePath)
			if err := db.ResetRetry(ctx, post.ID); err != nil {
				fmt.Fprintf(os.Stderr, "Error resetting retry for %s: %v\n", post.ID, err)
				continue
			}
			missingCount++
		} else {
			fmt.Printf("File verified: %s\n", post.FilePath)
			verifiedCount++
		}
	}
	fmt.Printf("Re-check complete: %d files verified, %d missing\n", verifiedCount, missingCount)
	return nil
}

// runCycle performs one download cycle.
//
// Parameters:
//   - slogLogger: Structured logger (*slog.Logger) for contextual fields and structured sink.
//     Must be non-nil. Use this for structured logging with contextual attributes.
func runCycle(ctx context.Context, db *storage.DB, client reddit.RedditClient, dl *downloader.Downloader, cfg *config.Config, slogLogger *slog.Logger) error {
	fmt.Println("Starting download cycle...")

	// Check if full sync is pending (first run after migration)
	fullSyncOnce, _ := db.GetMetadata(ctx, "full_sync_once")
	isFullSync := fullSyncOnce == "pending" && cfg.Migrate.FullSyncOnce

	fetchLimit := cfg.Download.FetchLimit
	if isFullSync {
		// Use higher limit for full sync (fetch all posts)
		fetchLimit = 1000
		fmt.Println("Full sync mode: fetching all posts (first run after migration)")
	}

	// Fetch upvoted and saved posts
	upvoted, err := client.GetUpvoted(ctx, fetchLimit)
	if err != nil {
		return fmt.Errorf("fetching upvoted: %w", err)
	}

	saved, err := client.GetSaved(ctx, fetchLimit)
	if err != nil {
		return fmt.Errorf("fetching saved: %w", err)
	}

	fmt.Printf("Fetched %d upvoted and %d saved posts\n", len(upvoted), len(saved))

	// Combine all posts
	allPosts := append(upvoted, saved...)

	// Filter posts: include new posts and posts eligible for retry
	var newPosts []storage.Post
	for _, post := range allPosts {
		status, err := db.CheckPostStatus(ctx, post.ID, cfg.SmartPolling.RetryThreshold, cfg.Backoff.Base, cfg.Backoff.Max)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking post status: %v\n", err)
			continue
		}
		if !status.Exists || status.RetryEligible {
			newPosts = append(newPosts, post)
		}
	}

	fmt.Printf("Found %d new posts to download\n", len(newPosts))

	if len(newPosts) == 0 {
		fmt.Println("No new posts to download")
		if isFullSync {
			if err := db.SetMetadata(ctx, "full_sync_once", "completed"); err != nil {
				fmt.Fprintf(os.Stderr, "Error marking full sync as completed: %v\n", err)
			} else {
				fmt.Println("Full sync completed, switching to incremental mode")
			}
		}
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

	// Download items and get hashes (may return partial hashes + error)
	hashes, err := dl.Download(ctx, items)

	// Save posts with whatever hashes we have (preserves partial results on error)
	// Collect any save errors to prevent finalizing full_sync_once on persistence failures
	var firstSaveErr error
	for _, post := range newPosts {
		if hash, ok := hashes[post.ID]; ok {
			post.DownloadedAt = time.Now()
			// Strip DUPLICATE: prefix if present before saving to database
			if strings.HasPrefix(hash, "DUPLICATE:") {
				post.Hash = strings.TrimPrefix(hash, "DUPLICATE:")
			} else {
				post.Hash = hash
			}
			if saveErr := db.SavePost(ctx, &post); saveErr != nil {
				slogLogger.Error("Error saving post", "error", saveErr, "post_id", post.ID)
				if firstSaveErr == nil {
					firstSaveErr = fmt.Errorf("failed to save post %s: %w", post.ID, saveErr)
				}
			}
		}
	}

	if err != nil {
		slogLogger.Warn("Warning: download completed with errors", "error", err)
		return fmt.Errorf("downloading media: %w", err)
	}
	if firstSaveErr != nil {
		return fmt.Errorf("saving posts: %w", firstSaveErr)
	}

	if isFullSync {
		if err := db.SetMetadata(ctx, "full_sync_once", "completed"); err != nil {
			slogLogger.Error("Error marking full sync as completed", "error", err)
		} else {
			slogLogger.Info("Full sync completed, switching to incremental mode")
		}
	}

	slogLogger.Info("Cycle complete", "downloaded_items", len(items))
	return nil
}

// handleAuth runs the OAuth2 code flow to get a refresh token.
func handleAuth(cfg *config.Config) error {
	// Validate we have the required credentials
	if cfg.Reddit.ClientID == "" || cfg.Reddit.ClientSecret == "" {
		return fmt.Errorf("REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET are required for authentication")
	}

	userAgent := cfg.Reddit.UserAgent
	if userAgent == "" {
		userAgent = "reddit-media-downloader/1.0"
	}

	fmt.Println("Starting OAuth2 authentication...")
	fmt.Println("This will open a browser window for you to authorize the application.")
	fmt.Println("")

	refreshToken, err := reddit.OAuth2CodeFlow(cfg.Reddit.ClientID, cfg.Reddit.ClientSecret, userAgent)
	if err != nil {
		return fmt.Errorf("OAuth2 code flow failed: %w", err)
	}

	// Mask token for display (show only last 4 characters)
	maskedToken := maskToken(refreshToken)

	fmt.Println("")
	fmt.Println("=== SETUP COMPLETE ===")
	fmt.Println("")
	fmt.Println("Security Note: Store your refresh token securely.")
	fmt.Println("Do not commit it to version control or share it publicly.")
	fmt.Println("")
	fmt.Printf("Masked token for reference: %s\n", maskedToken)
	fmt.Println("")
	fmt.Println("Options to save your token:")
	fmt.Println("1. Add to .env file: REDDIT_REFRESH_TOKEN=<FULL_TOKEN_FROM_refresh_token.txt>")
	fmt.Println("2. Copy full token from ./refresh_token.txt to your .env file")
	fmt.Println("")
	fmt.Println("To use with Docker, add this to your .env file:")
	fmt.Printf("# REDDIT_REFRESH_TOKEN=<FULL_TOKEN_FROM_refresh_token.txt>\n")
	fmt.Println("")
	fmt.Println("Or pass it via environment variable:")
	fmt.Printf("# REDDIT_REFRESH_TOKEN=<FULL_TOKEN_FROM_refresh_token.txt> docker-compose up -d\n")
	fmt.Println("")
	fmt.Println("Note: For security, the full token was saved to ./refresh_token.txt")
	fmt.Println("Please copy it to your .env file manually.")
	fmt.Println("")

	// Write token to a file for the user to retrieve
	fmt.Println("Writing token to ./refresh_token.txt for retrieval...")
	if err := os.WriteFile("./refresh_token.txt", []byte(refreshToken), 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}
	fmt.Println("Token written to ./refresh_token.txt - please secure this file!")
	return nil
}
