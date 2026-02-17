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

	"github.com/user/reddit-media-downloader/internal/config"
	"github.com/user/reddit-media-downloader/internal/downloader"
	"github.com/user/reddit-media-downloader/internal/reddit"
	"github.com/user/reddit-media-downloader/internal/storage"
	"golang.org/x/oauth2"
)

// slogPrintfWrapper wraps *slog.Logger to provide Printf interface for compatibility
type slogPrintfWrapper struct {
	logger *slog.Logger
}

func (w *slogPrintfWrapper) Printf(format string, v ...any) {
	w.logger.Info(fmt.Sprintf(format, v...))
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
	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
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
		if err := runAutoMigration(ctx, db, cfg.Storage.OutputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Migration failed: %v\n", err)
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
	}

	tokenStore := &memoryTokenStore{}

	// Check for existing OAuth access token from environment variable
	if accessToken := os.Getenv("REDDIT_ACCESS_TOKEN"); accessToken != "" {
		token := &oauth2.Token{
			AccessToken: accessToken,
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(1 * time.Hour),
		}
		if err := tokenStore.SaveToken(token); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving token from env: %v\n", err)
		}
	}

	if refreshToken := os.Getenv("REDDIT_REFRESH_TOKEN"); refreshToken != "" {
		token := &oauth2.Token{
			RefreshToken: refreshToken,
			TokenType:    "Bearer",
			Expiry:       time.Now(),
		}
		if err := tokenStore.SaveToken(token); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving refresh token from env: %v\n", err)
		}
	}

	redditClient, err := reddit.NewClient(redditConfig, tokenStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Reddit client: %v\n", err)
		os.Exit(1)
	}
	defer redditClient.Close()

	slogLogger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
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

	// Wrap slog logger with Printf interface for backward compatibility
	loggerWrapper := &slogPrintfWrapper{logger: slogLogger}

	// Create downloader with wrapped slog logger (preserving structured logging)
	downloaderConfig := downloader.Config{
		OutputDir:   cfg.Storage.OutputDir,
		Concurrency: cfg.Download.Concurrency,
		Logger:      loggerWrapper,
	}
	dl := downloader.NewDownloader(downloaderConfig, db)

	// Main loop
	for {
		select {
		case <-ctx.Done():
			slogLogger.Info("Shutdown complete")
			return
		default:
			if err := runCycle(ctx, db, redditClient, dl, cfg, slogLogger, loggerWrapper); err != nil {
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

// runAutoMigration imports existing bdfr-html data
func runAutoMigration(ctx context.Context, db *storage.DB, outputDir string) error {
	// Check if migration has already been completed using metadata
	// This check is backward compatible - if metadata doesn't exist or key not found,
	// migration will proceed normally
	migrationComplete, err := db.GetMetadata(ctx, "migration_complete")
	if err != nil {
		// Log warning but don't fail - proceed with migration for backward compatibility
		fmt.Printf("Warning: Could not check migration_complete metadata: %v\n", err)
	}
	if migrationComplete == "true" {
		fmt.Println("Migration already completed (migration_complete=true), skipping migration")
		return nil
	}

	// Check if database is empty (legacy check for backward compatibility)
	stats, err := db.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("getting stats: %w", err)
	}
	if stats.TotalPosts > 0 {
		// Database already has data, mark migration as complete and skip
		fmt.Printf("Database has %d posts, marking migration as complete\n", stats.TotalPosts)
		if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
			return fmt.Errorf("setting migration_complete metadata: %w", err)
		}
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

// runCycle performs one download cycle
func runCycle(ctx context.Context, db *storage.DB, client reddit.RedditClient, dl *downloader.Downloader, cfg *config.Config, slogLogger *slog.Logger, logger interface{ Printf(format string, v ...any) }) error {
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
	for _, post := range newPosts {
		if hash, ok := hashes[post.ID]; ok {
			post.DownloadedAt = time.Now()
			// Strip DUPLICATE: prefix if present before saving to database
			if strings.HasPrefix(hash, "DUPLICATE:") {
				post.Hash = strings.TrimPrefix(hash, "DUPLICATE:")
			} else {
				post.Hash = hash
			}
			if err := db.SavePost(ctx, &post); err != nil {
				slogLogger.Error("Error saving post", "error", err, "post_id", post.ID)
			}
		}
	}

	if err != nil {
		slogLogger.Warn("Warning: download completed with errors", "error", err)
		return fmt.Errorf("downloading media: %w", err)
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
