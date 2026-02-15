// Reddit Media Downloader - Main entry point
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/user/reddit-media-downloader/internal/config"
	"github.com/user/reddit-media-downloader/internal/downloader"
	"github.com/user/reddit-media-downloader/internal/reddit"
	"github.com/user/reddit-media-downloader/internal/storage"
	"golang.org/x/oauth2"
)

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

	// Create Reddit client
	redditConfig := &reddit.Config{
		ClientID:     cfg.Reddit.ClientID,
		ClientSecret: cfg.Reddit.ClientSecret,
		UserAgent:    cfg.Reddit.UserAgent,
		Username:     cfg.Reddit.Username,
		Password:     cfg.Reddit.Password,
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
		OutputDir:   cfg.Storage.OutputDir,
		Concurrency: cfg.Download.Concurrency,
	}
	dl := downloader.NewDownloader(downloaderConfig)

	// Main loop
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Shutdown complete")
			return
		default:
			if err := runCycle(ctx, db, redditClient, dl, cfg); err != nil {
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
func runCycle(ctx context.Context, db *storage.DB, client *reddit.Client, dl *downloader.Downloader, cfg *config.Config) error {
	fmt.Println("Starting download cycle...")

	// Fetch upvoted and saved posts
	upvoted, err := client.GetUpvoted(ctx, cfg.Download.FetchLimit)
	if err != nil {
		return fmt.Errorf("fetching upvoted: %w", err)
	}

	saved, err := client.GetSaved(ctx, cfg.Download.FetchLimit)
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
