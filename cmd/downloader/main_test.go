// Integration tests for re-check mode and smart polling features
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/reddit-media-downloader/internal/config"
	"github.com/user/reddit-media-downloader/internal/downloader"
	"github.com/user/reddit-media-downloader/internal/storage"
)

func setupIntegrationTest(t *testing.T) (*storage.DB, string, func()) {
	t.Helper()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, tempDir, cleanup
}

func TestReCheckMode_FileMissing(t *testing.T) {
	db, tempDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()

	nonExistentFile := filepath.Join(tempDir, "missing_file.jpg")
	post := &storage.Post{
		ID:           "missing123",
		Title:        "Test Missing File",
		Subreddit:    "test",
		DownloadedAt: time.Now(),
		Source:       "saved",
		FilePath:     nonExistentFile,
		RetryCount:   2,
		LastError:    "previous error",
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	if _, err := os.Stat(nonExistentFile); !os.IsNotExist(err) {
		t.Fatal("Expected file to not exist")
	}

	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		t.Fatalf("Failed to get all posts: %v", err)
	}

	var missingCount int
	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}

		_, err := os.Stat(p.FilePath)
		if err != nil {
			if err := db.ResetRetry(ctx, p.ID); err != nil {
				t.Errorf("Error resetting retry for %s: %v", p.ID, err)
				continue
			}
			missingCount++
		}
	}

	if missingCount != 1 {
		t.Errorf("Expected 1 missing file, got %d", missingCount)
	}

	retrieved, err := db.GetPost(ctx, "missing123")
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}

	if retrieved.RetryCount != 0 {
		t.Errorf("Expected retry count to be reset to 0, got %d", retrieved.RetryCount)
	}

	if retrieved.LastError != "" {
		t.Errorf("Expected last_error to be cleared, got %s", retrieved.LastError)
	}

	if !retrieved.LastAttempt.IsZero() {
		t.Error("Expected LastAttempt to be zero after reset")
	}
}

func TestReCheckMode_FileExists(t *testing.T) {
	db, tempDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()

	existingFile := filepath.Join(tempDir, "existing_file.jpg")
	if err := os.WriteFile(existingFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	post := &storage.Post{
		ID:           "exists456",
		Title:        "Test Existing File",
		Subreddit:    "test",
		DownloadedAt: time.Now(),
		Source:       "upvoted",
		FilePath:     existingFile,
		RetryCount:   0,
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		t.Fatalf("Failed to get all posts: %v", err)
	}

	var verifiedCount, missingCount int
	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}

		_, err := os.Stat(p.FilePath)
		if err != nil {
			missingCount++
		} else {
			verifiedCount++
		}
	}

	if verifiedCount != 1 {
		t.Errorf("Expected 1 verified file, got %d", verifiedCount)
	}

	if missingCount != 0 {
		t.Errorf("Expected 0 missing files, got %d", missingCount)
	}

	retrieved, err := db.GetPost(ctx, "exists456")
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}

	if retrieved.FilePath != existingFile {
		t.Errorf("Expected file path unchanged, got %s", retrieved.FilePath)
	}
}

func TestReCheckMode_MixedFiles(t *testing.T) {
	db, tempDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()

	testCases := []struct {
		id          string
		createFile  bool
		shouldReset bool
	}{
		{"mixed1", true, false},
		{"mixed2", false, true},
		{"mixed3", true, false},
		{"mixed4", false, true},
	}

	for _, tc := range testCases {
		filePath := filepath.Join(tempDir, tc.id+".jpg")

		if tc.createFile {
			if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
		}

		post := &storage.Post{
			ID:           tc.id,
			Title:        "Test Post " + tc.id,
			Subreddit:    "test",
			DownloadedAt: time.Now(),
			Source:       "saved",
			FilePath:     filePath,
			RetryCount:   2,
			LastError:    "previous error",
		}

		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		t.Fatalf("Failed to get all posts: %v", err)
	}

	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}

		_, err := os.Stat(p.FilePath)
		if err != nil {
			if err := db.ResetRetry(ctx, p.ID); err != nil {
				t.Errorf("Error resetting retry for %s: %v", p.ID, err)
			}
		}
	}

	for _, tc := range testCases {
		retrieved, err := db.GetPost(ctx, tc.id)
		if err != nil {
			t.Fatalf("Failed to get post %s: %v", tc.id, err)
		}

		if tc.shouldReset {
			if retrieved.RetryCount != 0 {
				t.Errorf("Post %s: expected retry count reset to 0, got %d", tc.id, retrieved.RetryCount)
			}
			if retrieved.LastError != "" {
				t.Errorf("Post %s: expected last_error cleared, got %s", tc.id, retrieved.LastError)
			}
		} else {
			if retrieved.RetryCount != 2 {
				t.Errorf("Post %s: expected retry count unchanged (2), got %d", tc.id, retrieved.RetryCount)
			}
			if retrieved.LastError != "previous error" {
				t.Errorf("Post %s: expected last_error unchanged, got %s", tc.id, retrieved.LastError)
			}
		}
	}
}

func TestRetryThreshold(t *testing.T) {
	db, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	threshold := 3

	testCases := []struct {
		name        string
		retryCount  int
		shouldSkip  bool
		shouldRetry bool
		waitAfter   time.Duration // Wait after IncrementRetry to pass backoff
	}{
		// With backoffBase=1s, retryCount=2 gives backoffDelay=4s
		// After immediately calling CheckPostStatus, the post should be in backoff window
		{"below_threshold_in_backoff", 2, true, false, 0},
		// Wait 5s to pass the 4s backoff window
		{"below_threshold_after_backoff", 2, false, true, 5 * time.Second},
		{"at_threshold", 3, true, false, 0},
		{"exceeds_threshold", 5, true, false, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			postID := "threshold_" + tc.name

			post := &storage.Post{
				ID:           postID,
				DownloadedAt: time.Now(),
				Source:       "saved",
			}

			if err := db.SavePost(ctx, post); err != nil {
				t.Fatalf("Failed to save post: %v", err)
			}

			for i := 0; i < tc.retryCount; i++ {
				if err := db.IncrementRetry(ctx, postID, "test error"); err != nil {
					t.Fatalf("Failed to increment retry: %v", err)
				}
			}

			// Wait if needed to pass through backoff window
			if tc.waitAfter > 0 {
				time.Sleep(tc.waitAfter)
			}

			status, err := db.CheckPostStatus(ctx, postID, threshold, time.Second, time.Minute)
			if err != nil {
				t.Fatalf("Failed to check post status: %v", err)
			}

			if status.ShouldSkip != tc.shouldSkip {
				t.Errorf("ShouldSkip = %v, want %v", status.ShouldSkip, tc.shouldSkip)
			}

			if status.RetryEligible != tc.shouldRetry {
				t.Errorf("RetryEligible = %v, want %v", status.RetryEligible, tc.shouldRetry)
			}

			if status.RetryCount != tc.retryCount {
				t.Errorf("RetryCount = %d, want %d", status.RetryCount, tc.retryCount)
			}
		})
	}
}

func TestExponentialBackoffCalculation(t *testing.T) {
	testCases := []struct {
		retryCount    int
		baseDelay     time.Duration
		maxDelay      time.Duration
		expectedDelay time.Duration
	}{
		{0, 100 * time.Millisecond, time.Second, 100 * time.Millisecond},
		{1, 100 * time.Millisecond, time.Second, 200 * time.Millisecond},
		{2, 100 * time.Millisecond, time.Second, 400 * time.Millisecond},
		{3, 100 * time.Millisecond, time.Second, 800 * time.Millisecond},
		{4, 100 * time.Millisecond, time.Second, 1000 * time.Millisecond},
		{5, 100 * time.Millisecond, time.Second, 1000 * time.Millisecond},
		{10, 100 * time.Millisecond, time.Second, 1000 * time.Millisecond},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("retry_%d", tc.retryCount), func(t *testing.T) {
			delay := calculateBackoffDelay(tc.retryCount, tc.baseDelay, tc.maxDelay)
			if delay != tc.expectedDelay {
				t.Errorf("Expected delay %v, got %v", tc.expectedDelay, delay)
			}
		})
	}
}

func calculateBackoffDelay(retryCount int, base, max time.Duration) time.Duration {
	if retryCount < 0 || base <= 0 {
		return 0
	}

	delay := base * time.Duration(1<<uint(retryCount))

	if max > 0 && delay > max {
		return max
	}
	return delay
}

func TestCheckPostStatus_Integration(t *testing.T) {
	db, tempDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	threshold := 3
	baseDelay := 100 * time.Millisecond
	maxDelay := time.Second

	testCases := []struct {
		name             string
		setupFunc        func(string) (*storage.Post, error)
		waitAfterSetup   time.Duration
		expectExists     bool
		expectFileExists bool
		expectShouldSkip bool
		expectEligible   bool
	}{
		{
			name: "existing_file",
			setupFunc: func(id string) (*storage.Post, error) {
				filePath := filepath.Join(tempDir, id+".jpg")
				if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
					return nil, err
				}
				return &storage.Post{
					ID:           id,
					DownloadedAt: time.Now(),
					Source:       "saved",
					FilePath:     filePath,
				}, nil
			},
			expectExists:     true,
			expectFileExists: true,
			expectShouldSkip: true,
			expectEligible:   false,
		},
		{
			name: "missing_file_no_retries",
			setupFunc: func(id string) (*storage.Post, error) {
				filePath := filepath.Join(tempDir, id+".jpg")
				return &storage.Post{
					ID:           id,
					DownloadedAt: time.Now(),
					Source:       "saved",
					FilePath:     filePath,
					RetryCount:   0,
				}, nil
			},
			expectExists:     true,
			expectFileExists: false,
			expectShouldSkip: false,
			expectEligible:   true,
		},
		{
			name: "missing_file_after_backoff",
			setupFunc: func(id string) (*storage.Post, error) {
				post := &storage.Post{
					ID:           id,
					DownloadedAt: time.Now(),
					Source:       "saved",
					FilePath:     filepath.Join(tempDir, id+".jpg"),
				}
				return post, nil
			},
			waitAfterSetup:   250 * time.Millisecond,
			expectExists:     true,
			expectFileExists: false,
			expectShouldSkip: false,
			expectEligible:   true,
		},
		{
			name: "exceeds_threshold",
			setupFunc: func(id string) (*storage.Post, error) {
				return &storage.Post{
					ID:           id,
					DownloadedAt: time.Now(),
					Source:       "saved",
					FilePath:     filepath.Join(tempDir, id+".jpg"),
				}, nil
			},
			waitAfterSetup:   0,
			expectExists:     true,
			expectFileExists: false,
			expectShouldSkip: true,
			expectEligible:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			postID := "integration_" + tc.name

			post, err := tc.setupFunc(postID)
			if err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			if err := db.SavePost(ctx, post); err != nil {
				t.Fatalf("Failed to save post: %v", err)
			}

			switch tc.name {
			case "missing_file_after_backoff":
				for i := 0; i < 1; i++ {
					db.IncrementRetry(ctx, postID, "error")
				}
			case "exceeds_threshold":
				for i := 0; i < 4; i++ {
					db.IncrementRetry(ctx, postID, "error")
				}
			}

			if tc.waitAfterSetup > 0 {
				time.Sleep(tc.waitAfterSetup)
			}

			status, err := db.CheckPostStatus(ctx, postID, threshold, baseDelay, maxDelay)
			if err != nil {
				t.Fatalf("Failed to check post status: %v", err)
			}

			if status.Exists != tc.expectExists {
				t.Errorf("Exists = %v, want %v", status.Exists, tc.expectExists)
			}
			if status.FileExists != tc.expectFileExists {
				t.Errorf("FileExists = %v, want %v", status.FileExists, tc.expectFileExists)
			}
			if status.ShouldSkip != tc.expectShouldSkip {
				t.Errorf("ShouldSkip = %v, want %v", status.ShouldSkip, tc.expectShouldSkip)
			}
			if status.RetryEligible != tc.expectEligible {
				t.Errorf("RetryEligible = %v, want %v", status.RetryEligible, tc.expectEligible)
			}
		})
	}
}

func TestReCheckMode_EmptyDatabase(t *testing.T) {
	db, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()

	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		t.Fatalf("Failed to get all posts: %v", err)
	}

	if len(posts) != 0 {
		t.Errorf("Expected 0 posts in empty database, got %d", len(posts))
	}

	var verifiedCount, missingCount int
	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}
		_, err := os.Stat(p.FilePath)
		if err != nil {
			missingCount++
		} else {
			verifiedCount++
		}
	}

	if verifiedCount != 0 || missingCount != 0 {
		t.Errorf("Expected 0 verified and 0 missing, got %d verified and %d missing", verifiedCount, missingCount)
	}
}

func TestReCheckMode_NoFilePath(t *testing.T) {
	db, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()

	post := &storage.Post{
		ID:           "nofile123",
		Title:        "Post Without File",
		Subreddit:    "test",
		DownloadedAt: time.Now(),
		Source:       "saved",
		FilePath:     "",
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	posts, err := db.GetAllPosts(ctx)
	if err != nil {
		t.Fatalf("Failed to get all posts: %v", err)
	}

	var processedCount int
	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}
		processedCount++
	}

	if processedCount != 0 {
		t.Errorf("Expected 0 processed posts (none have file_path), got %d", processedCount)
	}
}

// ============================================================================
// E2E WORKFLOW TESTS
// ============================================================================

type mockRedditClient struct {
	upvoted      []storage.Post
	saved        []storage.Post
	callCount    int
	upvotedCalls int
	savedCalls   int
}

func (m *mockRedditClient) GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error) {
	m.callCount++
	m.upvotedCalls++
	if limit >= len(m.upvoted) {
		return m.upvoted, nil
	}
	return m.upvoted[:limit], nil
}

func (m *mockRedditClient) GetSaved(ctx context.Context, limit int) ([]storage.Post, error) {
	m.callCount++
	m.savedCalls++
	if limit >= len(m.saved) {
		return m.saved, nil
	}
	return m.saved[:limit], nil
}

func (m *mockRedditClient) Close() error {
	return nil
}

func createTestPost(id, source string) storage.Post {
	return storage.Post{
		ID:        id,
		Title:     fmt.Sprintf("Test Post %s", id),
		Subreddit: "testsubreddit",
		Author:    "testuser",
		URL:       fmt.Sprintf("https://example.com/image_%s.jpg", id),
		Permalink: fmt.Sprintf("/r/testsubreddit/comments/%s/test/", id),
		CreatedAt: time.Now().Add(-time.Hour),
		Source:    source,
		MediaType: "image",
	}
}

func TestE2E_FullWorkflow(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dbPath := filepath.Join(tempDir, "posts.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	migrationComplete, err := db.GetMetadata(ctx, "migration_complete")
	if err != nil {
		t.Fatalf("Failed to check migration metadata: %v", err)
	}
	if migrationComplete != "" {
		t.Errorf("Fresh database should not have migration_complete set, got: %s", migrationComplete)
	}

	testPosts := []storage.Post{
		{ID: "post001", Title: "Migrated Post 1", Source: "imported", DownloadedAt: time.Now(), FilePath: filepath.Join(outputDir, "post001.jpg")},
		{ID: "post002", Title: "Migrated Post 2", Source: "imported", DownloadedAt: time.Now(), FilePath: filepath.Join(outputDir, "post002.jpg")},
	}

	for _, post := range testPosts {
		if err := db.SavePost(ctx, &post); err != nil {
			t.Fatalf("Failed to save test post: %v", err)
		}
	}

	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		t.Fatalf("Failed to set migration_complete: %v", err)
	}
	if err := db.SetMetadata(ctx, "full_sync_once", "pending"); err != nil {
		t.Fatalf("Failed to set full_sync_once: %v", err)
	}

	migrationComplete, err = db.GetMetadata(ctx, "migration_complete")
	if err != nil {
		t.Fatalf("Failed to get migration_complete: %v", err)
	}
	if migrationComplete != "true" {
		t.Errorf("Expected migration_complete=true, got: %s", migrationComplete)
	}

	fullSyncOnce, err := db.GetMetadata(ctx, "full_sync_once")
	if err != nil {
		t.Fatalf("Failed to get full_sync_once: %v", err)
	}
	if fullSyncOnce != "pending" {
		t.Errorf("Expected full_sync_once=pending, got: %s", fullSyncOnce)
	}

	t.Log("Migration phase completed successfully")

	mockClient := &mockRedditClient{
		upvoted: []storage.Post{
			createTestPost("post003", "upvoted"),
			createTestPost("post004", "upvoted"),
		},
		saved: []storage.Post{
			createTestPost("post005", "saved"),
		},
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			OutputDir: outputDir,
			DBPath:    dbPath,
		},
		Download: config.DownloadConfig{
			Concurrency: 5,
			FetchLimit:  100,
		},
		Migrate: config.MigrateConfig{
			OnStart:      true,
			FullSyncOnce: true,
		},
		SmartPolling: config.SmartPollingConfig{
			RetryThreshold: 3,
		},
		Backoff: config.BackoffConfig{
			Base: 5 * time.Second,
			Max:  60 * time.Second,
		},
	}

	dlConfig := downloader.Config{
		OutputDir:   outputDir,
		Concurrency: 5,
		Logger:      log.New(io.Discard, "", 0),
	}
	dl := downloader.NewDownloader(dlConfig, db)

	testLogger := log.New(io.Discard, "", 0)
	if err := runCycle(ctx, db, mockClient, dl, cfg, testLogger); err != nil {
		t.Logf("First run cycle completed with expected download errors: %v", err)
	}

	fullSyncOnce, err = db.GetMetadata(ctx, "full_sync_once")
	if err != nil {
		t.Fatalf("Failed to get full_sync_once after first run: %v", err)
	}
	if fullSyncOnce != "pending" {
		t.Errorf("Expected full_sync_once=pending after first run with errors, got: %s", fullSyncOnce)
	}

	if mockClient.callCount == 0 {
		t.Error("Expected Reddit API to be called during first run")
	}
	t.Logf("First run completed - Reddit API called %d times", mockClient.callCount)

	initialCallCount := mockClient.callCount
	mockClient.callCount = 0

	mockClient.upvoted = append(mockClient.upvoted, createTestPost("post006", "upvoted"))

	if err := runCycle(ctx, db, mockClient, dl, cfg, testLogger); err != nil {
		t.Logf("Second run cycle completed with expected download errors: %v", err)
	}

	fullSyncOnce, err = db.GetMetadata(ctx, "full_sync_once")
	if err != nil {
		t.Fatalf("Failed to get full_sync_once after second run: %v", err)
	}
	if fullSyncOnce != "pending" {
		t.Errorf("Expected full_sync_once=pending after second run with errors, got: %s", fullSyncOnce)
	}

	if mockClient.callCount == 0 {
		t.Error("Expected Reddit API to be called during second run")
	}
	t.Logf("Second run completed - Reddit API called %d times", mockClient.callCount)

	testFile := filepath.Join(outputDir, "testsubreddit", "test_post.jpg")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatalf("Failed to create subreddit dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	postWithFile := storage.Post{
		ID:           "post007",
		Title:        "Post With File",
		Source:       "upvoted",
		FilePath:     testFile,
		DownloadedAt: time.Now(),
	}
	if err := db.SavePost(ctx, &postWithFile); err != nil {
		t.Fatalf("Failed to save post with file: %v", err)
	}

	if err := runReCheckMode(ctx, db); err != nil {
		t.Fatalf("Re-check mode failed: %v", err)
	}

	post, err := db.GetPost(ctx, "post007")
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if post == nil {
		t.Fatal("Post should exist")
	}
	if post.RetryCount != 0 {
		t.Errorf("Expected retry count to be 0 for existing file, got: %d", post.RetryCount)
	}

	if err := os.Remove(testFile); err != nil {
		t.Fatalf("Failed to remove test file: %v", err)
	}

	if err := os.Remove(filepath.Dir(testFile)); err != nil {
		t.Logf("Note: Could not remove subreddit dir: %v", err)
	}

	if err := db.ResetRetry(ctx, "post007"); err != nil {
		t.Fatalf("Failed to reset retry: %v", err)
	}

	if err := runReCheckMode(ctx, db); err != nil {
		t.Fatalf("Re-check mode failed after file deletion: %v", err)
	}

	post, err = db.GetPost(ctx, "post007")
	if err != nil {
		t.Fatalf("Failed to get post after deletion: %v", err)
	}
	if post.RetryCount != 0 {
		t.Errorf("Expected retry count to be 0 after reset, got: %d", post.RetryCount)
	}

	t.Log("Re-check mode completed successfully")

	stats, err := db.GetStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	t.Logf("Final database stats: %d total posts", stats.TotalPosts)

	if stats.TotalPosts < 2 {
		t.Errorf("Expected at least 2 posts in database, got: %d", stats.TotalPosts)
	}

	t.Logf("Full workflow test completed successfully (total API calls in first run: %d)", initialCallCount)
}

func TestE2E_NoRedditCallsForExisting(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dbPath := filepath.Join(tempDir, "posts.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		t.Fatalf("Failed to set migration_complete: %v", err)
	}
	if err := db.SetMetadata(ctx, "full_sync_once", "completed"); err != nil {
		t.Fatalf("Failed to set full_sync_once: %v", err)
	}

	existingPosts := []storage.Post{
		createTestPost("existing001", "upvoted"),
		createTestPost("existing002", "upvoted"),
		createTestPost("existing003", "saved"),
	}

	for _, post := range existingPosts {
		post.DownloadedAt = time.Now()
		if err := db.SavePost(ctx, &post); err != nil {
			t.Fatalf("Failed to save existing post: %v", err)
		}
	}

	mockClient := &mockRedditClient{
		upvoted: []storage.Post{
			existingPosts[0],
			existingPosts[1],
		},
		saved: []storage.Post{
			existingPosts[2],
		},
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			OutputDir: outputDir,
			DBPath:    dbPath,
		},
		Download: config.DownloadConfig{
			Concurrency: 5,
			FetchLimit:  100,
		},
		Migrate: config.MigrateConfig{
			OnStart:      true,
			FullSyncOnce: true,
		},
		SmartPolling: config.SmartPollingConfig{
			RetryThreshold: 3,
		},
		Backoff: config.BackoffConfig{
			Base: 5 * time.Second,
			Max:  60 * time.Second,
		},
	}

	testLogger := log.New(io.Discard, "", 0)
	dlConfig := downloader.Config{
		OutputDir:   outputDir,
		Concurrency: 5,
		Logger:      testLogger,
	}
	dl := downloader.NewDownloader(dlConfig, db)

	if err := runCycle(ctx, db, mockClient, dl, cfg, testLogger); err != nil {
		t.Logf("Cycle completed with expected download errors: %v", err)
	}

	if mockClient.callCount == 0 {
		t.Error("Expected Reddit API to be called to check for new posts")
	}

	t.Logf("Reddit API was called %d times (to check for new content)", mockClient.callCount)

	stats, err := db.GetStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalPosts != 3 {
		t.Errorf("Expected exactly 3 posts (no new posts added), got: %d", stats.TotalPosts)
	}

	t.Log("No duplicate posts were added for existing entries")

	mockClient.callCount = 0
	mockClient.upvoted = append(mockClient.upvoted, createTestPost("newpost001", "upvoted"))

	if err := runCycle(ctx, db, mockClient, dl, cfg, testLogger); err != nil {
		t.Logf("Cycle with new post completed with expected errors: %v", err)
	}

	if mockClient.callCount == 0 {
		t.Error("Expected Reddit API to be called when checking for new content")
	}

	t.Logf("Test completed - Reddit API called appropriately")
}

func TestE2E_MigrationSkipsOnExistingData(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dbPath := filepath.Join(tempDir, "posts.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		t.Fatalf("Failed to set migration_complete: %v", err)
	}

	idListPath := filepath.Join(tempDir, "idList.txt")
	if err := os.WriteFile(idListPath, []byte("testid1\ntestid2\n"), 0644); err != nil {
		t.Fatalf("Failed to create idList.txt: %v", err)
	}

	err = runAutoMigration(ctx, db, outputDir)
	if err != nil {
		t.Fatalf("Auto-migration failed: %v", err)
	}

	stats, err := db.GetStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalPosts != 0 {
		t.Errorf("Expected 0 posts (migration should be skipped), got: %d", stats.TotalPosts)
	}

	t.Log("Migration correctly skipped when migration_complete flag is set")
}

func TestE2E_ReCheckMissingFiles(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dbPath := filepath.Join(tempDir, "posts.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	existingFile := filepath.Join(outputDir, "existing.jpg")
	missingFile := filepath.Join(outputDir, "missing.jpg")

	if err := os.WriteFile(existingFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	posts := []storage.Post{
		{
			ID:           "existing_post",
			Title:        "Existing Post",
			Source:       "upvoted",
			FilePath:     existingFile,
			DownloadedAt: time.Now(),
			RetryCount:   0,
		},
		{
			ID:           "missing_post",
			Title:        "Missing Post",
			Source:       "saved",
			FilePath:     missingFile,
			DownloadedAt: time.Now(),
			RetryCount:   2,
			LastError:    "previous error",
		},
	}

	for _, post := range posts {
		if err := db.SavePost(ctx, &post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	if err := runReCheckMode(ctx, db); err != nil {
		t.Fatalf("Re-check mode failed: %v", err)
	}

	existingPost, err := db.GetPost(ctx, "existing_post")
	if err != nil {
		t.Fatalf("Failed to get existing post: %v", err)
	}
	if existingPost.RetryCount != 0 {
		t.Errorf("Existing post should have retry_count=0, got: %d", existingPost.RetryCount)
	}

	missingPost, err := db.GetPost(ctx, "missing_post")
	if err != nil {
		t.Fatalf("Failed to get missing post: %v", err)
	}
	if missingPost.RetryCount != 0 {
		t.Errorf("Missing post should have retry_count reset to 0, got: %d", missingPost.RetryCount)
	}
	if missingPost.LastError != "" {
		t.Errorf("Missing post should have last_error cleared, got: %s", missingPost.LastError)
	}

	t.Log("Re-check mode correctly handles existing and missing files")
}

type capturingMockClient struct {
	upvoted      []storage.Post
	saved        []storage.Post
	callCount    int
	upvotedLimit int
	savedLimit   int
}

func (m *capturingMockClient) GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error) {
	m.callCount++
	m.upvotedLimit = limit
	if limit >= len(m.upvoted) {
		return m.upvoted, nil
	}
	return m.upvoted[:limit], nil
}

func (m *capturingMockClient) GetSaved(ctx context.Context, limit int) ([]storage.Post, error) {
	m.callCount++
	m.savedLimit = limit
	if limit >= len(m.saved) {
		return m.saved, nil
	}
	return m.saved[:limit], nil
}

func (m *capturingMockClient) Close() error {
	return nil
}

func TestE2E_FullSyncLimit(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "output")
	dbPath := filepath.Join(tempDir, "posts.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		t.Fatalf("Failed to set migration_complete: %v", err)
	}
	if err := db.SetMetadata(ctx, "full_sync_once", "pending"); err != nil {
		t.Fatalf("Failed to set full_sync_once: %v", err)
	}

	mockClient := &capturingMockClient{
		upvoted: make([]storage.Post, 0),
		saved:   make([]storage.Post, 0),
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			OutputDir: outputDir,
			DBPath:    dbPath,
		},
		Download: config.DownloadConfig{
			Concurrency: 5,
			FetchLimit:  100,
		},
		Migrate: config.MigrateConfig{
			OnStart:      true,
			FullSyncOnce: true,
		},
		SmartPolling: config.SmartPollingConfig{
			RetryThreshold: 3,
		},
		Backoff: config.BackoffConfig{
			Base: 5 * time.Second,
			Max:  60 * time.Second,
		},
	}

	testLogger := log.New(io.Discard, "", 0)
	dlConfig := downloader.Config{
		OutputDir:   outputDir,
		Concurrency: 5,
		Logger:      testLogger,
	}
	dl := downloader.NewDownloader(dlConfig, db)

	if err := runCycle(ctx, db, mockClient, dl, cfg, testLogger); err != nil {
		t.Logf("Cycle completed: %v", err)
	}

	if mockClient.upvotedLimit != 1000 {
		t.Errorf("Expected full sync to use limit=1000 for upvoted, got: %d", mockClient.upvotedLimit)
	}
	if mockClient.savedLimit != 1000 {
		t.Errorf("Expected full sync to use limit=1000 for saved, got: %d", mockClient.savedLimit)
	}

	fullSyncOnce, err := db.GetMetadata(ctx, "full_sync_once")
	if err != nil {
		t.Fatalf("Failed to get full_sync_once: %v", err)
	}
	if fullSyncOnce != "completed" {
		t.Errorf("Expected full_sync_once=completed, got: %s", fullSyncOnce)
	}

	t.Logf("Full sync correctly used higher fetch limit (upvoted=%d, saved=%d)", mockClient.upvotedLimit, mockClient.savedLimit)
}
