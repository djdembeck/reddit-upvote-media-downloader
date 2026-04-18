// Reddit Media Downloader - Main entry point
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/config"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/downloader"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/migration"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/ownutil"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/strutil"
)

const fullSyncCompleted = "completed"

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

// memoryTokenStore implements reddit.TokenStore with in-memory storage.
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

// cycleError wraps a cycle-level error to distinguish fatal vs non-fatal errors.
// A cycleError is only returned for true cycle-level failures (e.g., output-dir creation,
// download subsystem errors) and not for per-item extraction/download failures.
type cycleError struct {
	cause error
}

func (e *cycleError) Error() string {
	return fmt.Sprintf("cycle error: %v", e.cause)
}

func (e *cycleError) Unwrap() error {
	return e.cause
}

// buildTokenFromEnv builds an oauth2.Token from environment variables.
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

// maskToken masks a token showing only the last 4 characters.
func maskToken(token string) string {
	if len(token) > 4 {
		return "****" + token[len(token)-4:]
	}
	return "****"
}

// setupTokenStore creates and initializes the token store with tokens from environment.
func setupTokenStore(_ *config.Config) (*memoryTokenStore, error) {
	tokenStore := &memoryTokenStore{}

	// Check for existing OAuth tokens from environment variables
	token := buildTokenFromEnv()

	// Save token if one was built
	if token != nil {
		if err := tokenStore.SaveToken(token); err != nil {
			return nil, fmt.Errorf("saving token from env: %w", err)
		}
	}

	return tokenStore, nil
}

// setupRedditClient creates and initializes the Reddit client.
func setupRedditClient(cfg *config.Config, tokenStore *memoryTokenStore) (reddit.Client, error) {
	redditConfig := &reddit.Config{
		ClientID:     cfg.Reddit.ClientID,
		ClientSecret: cfg.Reddit.ClientSecret,
		UserAgent:    cfg.Reddit.UserAgent,
		Username:     cfg.Reddit.Username,
		Password:     cfg.Reddit.Password,
		RefreshToken: cfg.Reddit.RefreshToken,
	}

	redditClient, err := reddit.NewClient(redditConfig, tokenStore)
	if err != nil {
		return nil, fmt.Errorf("creating Reddit client: %w", err)
	}

	return redditClient, nil
}

// setupLogger creates and configures the structured logger.
func setupLogger(cfg *config.Config) *slog.Logger {
	parsedLevel := parseSlogLevel(cfg.Log.Level)

	slogLogger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parsedLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
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

	return slogLogger
}

// setupDownloader creates and configures the downloader.
func setupDownloader(cfg *config.Config, db *storage.DB, logger *slog.Logger, owner *ownutil.Owner) *downloader.Downloader {
	downloaderConfig := downloader.Config{
		OutputDir:     cfg.Storage.OutputDir,
		Concurrency:   cfg.Download.Concurrency,
		DownloadDelay: cfg.Download.DownloadDelay,
		Logger:        logger,
		Owner:         owner,
	}
	return downloader.NewDownloader(downloaderConfig, db)
}

// findAndParseIndexHTML searches for and parses index.html in common locations.
func findAndParseIndexHTML(ctx context.Context, parser *migration.HTMLParser, sourceDir string) error {
	indexPaths := []struct {
		path    string
		baseDir string
	}{
		{filepath.Join(filepath.Dir(sourceDir), "index.html"), filepath.Dir(sourceDir)},
		{filepath.Join(sourceDir, "index.html"), sourceDir},
	}

	for _, idx := range indexPaths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled: %w", err)
		}
		if _, err := os.Stat(idx.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("checking index.html at %s: %w", idx.path, err)
		}
		fmt.Printf("No individual HTML files found. Parsing index.html at %s...\n", idx.path)
		if err := parser.ParseIndexHTML(ctx, idx.baseDir, idx.path); err != nil {
			return fmt.Errorf("parsing index.html at %s: %w", idx.path, err)
		}

		break
	}

	return nil
}

// checkFullSyncStatus determines if full sync is needed and returns the fetch limit.
//
//nolint:unparam
func checkFullSyncStatus(ctx context.Context, db *storage.DB, cfg *config.Config) (bool, int, error) {
	fetchLimit := cfg.Download.FetchLimit

	// Check if full sync is pending (first run after migration)
	fullSyncOnce, err := db.GetMetadata(ctx, "full_sync_once")
	if err != nil {
		// Continue with empty metadata - full sync can be skipped if metadata unavailable
		fullSyncOnce = ""
	}

	isFullSync := fullSyncOnce == storage.MetadataValuePending && cfg.Migrate.FullSyncOnce

	if isFullSync {
		// Use higher limit for full sync (fetch all posts)
		fetchLimit = 1000
		fmt.Println("Full sync mode: fetching all posts (first run after migration)")
	}

	return isFullSync, fetchLimit, nil
}

// filterNewPosts filters posts to include only new posts and posts eligible for retry.
//
//nolint:unparam
func filterNewPosts(ctx context.Context, db *storage.DB, posts []storage.Post, cfg *config.Config) ([]storage.Post, error) {
	var newPosts []storage.Post

	for _, post := range posts {
		status, err := db.CheckPostStatus(
			ctx,
			post.ID,
			cfg.SmartPolling.RetryThreshold,
			cfg.Backoff.Base,
			cfg.Backoff.Max,
		)
		if err != nil {
			// Propagate error so full sync cannot complete when lookups fail
			return nil, fmt.Errorf("checking post status for %s: %w", post.ID, err)
		}
		if !status.Exists || status.RetryEligible {
			newPosts = append(newPosts, post)
		}
	}

	return newPosts, nil
}

// saveDownloadedPosts saves downloaded posts to the database.
func saveDownloadedPosts(ctx context.Context, db *storage.DB, posts []storage.Post, hashes map[string]string, slogLogger *slog.Logger) error {
	var firstSaveErr error
	for _, post := range posts {
		hash := findHashForPost(hashes, post.ID)
		if hash != "" {
			post.DownloadedAt = time.Now()
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

	return firstSaveErr
}

// saveIncompletePosts tracks posts that had extraction/download failures for retry.
func saveIncompletePosts(ctx context.Context, db *storage.DB, posts []storage.Post, slogLogger *slog.Logger) error {
	var firstSaveErr error
	now := time.Now()
	for _, post := range posts {
		// Set retry tracking fields
		post.LastAttempt = now
		post.RetryCount++                      // Increment retry count
		post.LastError = "incomplete_download" // Mark as incomplete for retry logic

		if saveErr := db.SavePost(ctx, &post); saveErr != nil {
			slogLogger.Error("Error saving incomplete post", "error", saveErr, "post_id", post.ID)
			if firstSaveErr == nil {
				firstSaveErr = fmt.Errorf("failed to save incomplete post %s: %w", post.ID, saveErr)
			}
		}
	}
	return firstSaveErr
}

// saveCyclePosts handles saving both complete and incomplete posts after a download cycle.
// It returns a *cycleError on failure so the caller can decide whether to abort.
func saveCyclePosts(
	ctx context.Context,
	db *storage.DB,
	completePosts []storage.Post,
	incompletePosts []storage.Post,
	hashes map[string]string,
	slogLogger *slog.Logger,
) error {
	if err := saveDownloadedPosts(ctx, db, completePosts, hashes, slogLogger); err != nil {
		slogLogger.Error("aborting cycle: failed to save downloaded posts", "error", err, "post_count", len(completePosts))
		return &cycleError{cause: err}
	}

	// Also save incomplete posts so they don't reappear as "new" every cycle
	if len(incompletePosts) > 0 {
		if err := saveIncompletePosts(ctx, db, incompletePosts, slogLogger); err != nil {
			slogLogger.Error("aborting cycle: failed to save incomplete posts", "error", err, "post_count", len(incompletePosts))
			return &cycleError{cause: err}
		}
	}

	return nil
}

// classifyStepError checks if an error is fatal (context cancellation)
// and returns the appropriate wrapped error with the step name.
func classifyStepError(ctx context.Context, step string, err error) (fatal bool, wrapped error) {
	if err == nil {
		return false, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true, &cycleError{cause: fmt.Errorf("%s: %w", step, err)}
	}
	// Check for explicit context cancellation in case aggregate errors don't preserve wrapping
	if ctx.Err() != nil {
		return true, &cycleError{cause: fmt.Errorf("%s: %w", step, ctx.Err())}
	}
	return false, nil
}

// filterCompletePosts separates complete and incomplete posts.
// Returns (completePosts, incompletePosts) where:
// - completePosts: posts where expected > 0 && expected == actual
// - incompletePosts: posts where expected == 0 || expected != actual
func filterCompletePosts(
	items []downloader.Downloadable,
	hashes map[string]string,
	newPosts []storage.Post,
) (completePosts []storage.Post, incompletePosts []storage.Post) {
	// Compute expected count per post from items
	expectedByPost := make(map[string]int)
	for _, item := range items {
		expectedByPost[item.PostID]++
	}

	// Compute actual count per post from hashes (exclude _duplicate keys)
	actualByPost := make(map[string]int)
	for key := range hashes {
		if strings.HasSuffix(key, "_duplicate") {
			continue
		}
		// Key format: "{postID}" for single items or "{postID}_{index}" for gallery items
		if idx := strings.LastIndex(key, "_"); idx != -1 {
			postID := key[:idx]
			actualByPost[postID]++
		} else {
			actualByPost[key]++
		}
	}

	// Separate complete and incomplete posts
	for _, post := range newPosts {
		expected := expectedByPost[post.ID]
		actual := actualByPost[post.ID]
		if expected > 0 && expected == actual {
			completePosts = append(completePosts, post)
		} else {
			incompletePosts = append(incompletePosts, post)
		}
	}

	return completePosts, incompletePosts
}

// handleExtractionAndDownload handles extraction, download, and saving.
// Returns items, hashes, and any error (fatal cycle errors or non-fatal extraction/download failures).
func handleExtractionAndDownload(
	ctx context.Context,
	dl *downloader.Downloader,
	db *storage.DB,
	redditPosts []reddit.Post,
	newPosts []storage.Post,
	slogLogger *slog.Logger,
) ([]downloader.Downloadable, map[string]string, error) {
	items, extractErr := dl.Extract(ctx, redditPosts)
	if extractErr != nil {
		if fatal, wrapped := classifyStepError(ctx, "extracting reddit posts", extractErr); fatal {
			return nil, nil, wrapped
		}
		slogLogger.Warn("Extraction completed with some failures", "error", extractErr)
	}

	slogLogger.Info("Extracted downloadable items", "count", len(items))

	hashes, downloadErr := dl.Download(ctx, items)
	if downloadErr != nil {
		if fatal, wrapped := classifyStepError(ctx, "downloading items", downloadErr); fatal {
			return nil, nil, wrapped
		}
		slogLogger.Warn("Download completed with some failures", "error", downloadErr)
	}

	// Use helper to separate complete and incomplete posts
	completePosts, incompletePosts := filterCompletePosts(items, hashes, newPosts)

	if len(incompletePosts) > 0 {
		slogLogger.Info("Some posts incomplete, will track for retry", "complete", len(completePosts), "incomplete", len(incompletePosts))
	}

	if err := saveCyclePosts(ctx, db, completePosts, incompletePosts, hashes, slogLogger); err != nil {
		return nil, nil, err
	}

	// Return any non-fatal extraction/download errors so caller can decide on full sync completion
	if extractErr != nil {
		return items, hashes, fmt.Errorf("extract failed: %w", extractErr)
	}
	if downloadErr != nil {
		return items, hashes, fmt.Errorf("download failed: %w", downloadErr)
	}

	return items, hashes, nil
}

// finalizeFullSyncIfNeeded marks full sync as completed when !isFullSync or if no cycle-level errors occurred.
// Returns db.SetMetadata error if SetMetadata fails, or nil on success/when !isFullSync.
func finalizeFullSyncIfNeeded(
	ctx context.Context,
	db *storage.DB,
	isFullSync bool,
	slogLogger *slog.Logger,
) error {
	if !isFullSync {
		return nil
	}

	if err := db.SetMetadata(ctx, "full_sync_once", fullSyncCompleted); err != nil {
		slogLogger.Error("Error marking full sync as completed", "error", err)
		return fmt.Errorf("marking full sync as completed: %w", err)
	}

	slogLogger.Info("Full sync completed, switching to incremental mode")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

//nolint:cyclop,gocyclo
func run() error {
	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Handle --auth flag: run OAuth2 code flow to get refresh token
	if cfg.Auth {
		if err := handleAuth(cfg); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		return nil
	}

	// Setup logging
	fmt.Printf("Log level: %s\n", cfg.Log.Level)

	owner := ownutil.NewOwner(cfg.Storage.PUID, cfg.Storage.PGID)

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
	if err := os.MkdirAll(cfg.Storage.OutputDir, 0750); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	owner.ChownDir(cfg.Storage.OutputDir, nil)
	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0750); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}
	owner.Chown(filepath.Dir(cfg.Storage.DBPath), nil)

	// Open database
	db, err := storage.NewDB(ctx, cfg.Storage.DBPath, owner)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
		}
	}()

	// Auto-migrate on first run
	if cfg.Migrate.OnStart {
		if err := runAutoMigration(ctx, db, cfg, owner); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	// Run re-check mode if enabled
	if cfg.SmartPolling.ReCheck {
		if err := runReCheckMode(ctx, db); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Re-check failed: %v\n", err)
		}
	}

	// Setup token store
	tokenStore, err := setupTokenStore(cfg)
	if err != nil {
		return fmt.Errorf("setting up token store: %w", err)
	}

	// Setup Reddit client
	redditClient, err := setupRedditClient(cfg, tokenStore)
	if err != nil {
		return fmt.Errorf("creating Reddit client: %w", err)
	}
	defer func() {
		if err := redditClient.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing Reddit client: %v\n", err)
		}
	}()

	// Setup logger
	slogLogger := setupLogger(cfg)
	slog.SetDefault(slogLogger)

	// Setup downloader
	dl := setupDownloader(cfg, db, slogLogger, owner)

	// Main loop
	for {
		select {
		case <-ctx.Done():
			slogLogger.Info("Shutdown complete")
			return nil
		default:
			if err := runCycle(ctx, db, redditClient, dl, cfg, slogLogger); err != nil {
				slogLogger.Error("Cycle error", "error", err)
			}

			// Sleep for 1 hour
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Hour):
			}
		}
	}
}

//nolint:cyclop
func runAutoMigration(ctx context.Context, db *storage.DB, cfg *config.Config, owner *ownutil.Owner) error {
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
			return fmt.Errorf(
				"migration cannot proceed: ReorganizeEnabled is true but SourceDir is empty; " +
					"set MIGRATE_SOURCE_DIR environment variable",
			)
		}
		if err := runFileReorganization(ctx, cfg.Migrate.SourceDir, outputDir, cfg.Migrate.HTMLDir, db, owner); err != nil {
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

//nolint:cyclop,gocyclo
func runFileReorganization(ctx context.Context, sourceDir, destDir, htmlDir string, db *storage.DB, owner *ownutil.Owner) error {
	fmt.Println("===================")
	fmt.Println("File Reorganization")
	fmt.Println("===================")
	fmt.Printf("Source: %s\n", sourceDir)
	fmt.Printf("Destination: %s\n", destDir)
	fmt.Printf("HTML Directory: %s\n", htmlDir)
	fmt.Println()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled: %w", err)
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
		fmt.Printf("Parsing HTML files from %s...\n", htmlDir)
		if err := parser.ParseHTMLFiles(ctx, htmlDir); err != nil {
			return fmt.Errorf("parsing HTML files: %w", err)
		}
	} else {
		fmt.Printf("Parsing HTML files from %s...\n", sourceDir)
		if err := parser.ParseHTMLFiles(ctx, sourceDir); err != nil {
			return fmt.Errorf("parsing HTML files: %w", err)
		}

		if len(parser.PostMap) == 0 {
			if err := findAndParseIndexHTML(ctx, parser, sourceDir); err != nil {
				return err
			}
		}
	}

	if len(parser.PostMap) == 0 {
		fmt.Println("Warning: No HTML metadata found. Files will be organized as 'unknown' subreddit.")
	}
	fmt.Printf("Total: %d posts in HTML metadata\n\n", len(parser.PostMap))

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled: %w", err)
	}

	if err := os.MkdirAll(destDir, 0750); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}
	owner.ChownDir(destDir, nil)

	logPath := filepath.Join(destDir, ".migration_log.json")

	migrator := migration.NewMigrator(sourceDir, destDir, parser.PostMap, false, db, owner)
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
//
//nolint:cyclop
func runCycle(ctx context.Context, db *storage.DB, client reddit.Client, dl *downloader.Downloader, cfg *config.Config, slogLogger *slog.Logger) error {
	fmt.Println("Starting download cycle...")

	isFullSync, fetchLimit, err := checkFullSyncStatus(ctx, db, cfg)
	if err != nil {
		slogLogger.Debug("failed to check full sync status", "error", err)
	}

	upvoted, err := client.GetUpvoted(ctx, fetchLimit)
	if err != nil {
		return fmt.Errorf("fetching upvoted: %w", err)
	}

	saved, err := client.GetSaved(ctx, fetchLimit)
	if err != nil {
		return fmt.Errorf("fetching saved: %w", err)
	}

	fmt.Printf("Fetched %d upvoted and %d saved posts\n", len(upvoted), len(saved))

	allPosts := make([]storage.Post, 0, len(upvoted)+len(saved))
	allPosts = append(allPosts, upvoted...)
	allPosts = append(allPosts, saved...)

	newPosts, err := filterNewPosts(ctx, db, allPosts, cfg)
	if err != nil {
		return fmt.Errorf("filtering posts: %w", err)
	}

	fmt.Printf("Found %d new posts to download\n", len(newPosts))

	if len(newPosts) == 0 {
		fmt.Println("No new posts to download")
		if err := finalizeFullSyncIfNeeded(ctx, db, isFullSync, slogLogger); err != nil {
			return err
		}

		return nil
	}

	redditPosts := make([]reddit.Post, len(newPosts))
	for i, post := range newPosts {
		redditPosts[i] = reddit.Post{
			ID:        post.ID,
			Title:     post.Title,
			URL:       post.URL,
			Subreddit: post.Subreddit,
			Author:    post.Author,
		}
	}

	items, _, handleErr := handleExtractionAndDownload(ctx, dl, db, redditPosts, newPosts, slogLogger)
	if handleErr != nil {
		var ce *cycleError
		if errors.As(handleErr, &ce) {
			// Fatal cycle-level error: keep full_sync_once pending for retry
			return handleErr
		}
		// Non-fatal per-item error: log and continue to finalize
		slogLogger.Warn("Cycle completed with non-fatal errors", "error", handleErr)
	}

	if err := finalizeFullSyncIfNeeded(ctx, db, isFullSync, slogLogger); err != nil {
		return err
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

//nolint:fieldalignment
type itemHash struct {
	hash  string
	index int
}

func aggregateItemHashes(hashes []itemHash) string {
	sort.Slice(hashes, func(i, j int) bool {
		return hashes[i].index < hashes[j].index
	})

	var combined strings.Builder

	for i, item := range hashes {
		if i > 0 {
			combined.WriteByte(',')
		}
		combined.WriteString(item.hash)
	}

	aggregateHash := sha256.Sum256([]byte(combined.String()))
	return fmt.Sprintf("%x", aggregateHash)
}

func findHashForPost(hashes map[string]string, postID string) string {
	if hash, ok := hashes[postID]; ok {
		return hash
	}

	var itemHashes []itemHash
	for key, hash := range hashes {
		parts := strings.Split(key, "_")
		if len(parts) == 2 && parts[0] == postID && strutil.IsNumeric(parts[1]) {
			idx, err := strconv.Atoi(parts[1])
			if err != nil {
				slog.Warn("Failed to parse gallery index", "post_id", postID, "key", key, "error", err)
				continue
			}
			itemHashes = append(itemHashes, itemHash{index: idx, hash: hash})
		}
	}

	if len(itemHashes) == 0 {
		return ""
	}
	if len(itemHashes) == 1 {
		return itemHashes[0].hash
	}

	return aggregateItemHashes(itemHashes)
}
