// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, string) {
	t.Helper()

	// Create a temporary directory for the test database
	tempDir, err := os.MkdirTemp("", "reddit-media-downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := NewDB(dbPath)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create database: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		db.Close()
		os.RemoveAll(tempDir)
	})

	return db, tempDir
}

func TestNewDB(t *testing.T) {
	db, _ := setupTestDB(t)

	if db == nil {
		t.Fatal("Expected database to be created, got nil")
	}

	if db.conn == nil {
		t.Fatal("Expected database connection to be established")
	}
}

func TestSavePost(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	post := &Post{
		ID:           "abc123",
		Title:        "Test Post",
		Subreddit:    "test",
		Author:       "testuser",
		URL:          "https://example.com/image.jpg",
		Permalink:    "/r/test/comments/abc123/test_post/",
		CreatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DownloadedAt: time.Now(),
		MediaType:    "image",
		FilePath:     "/downloads/abc123.jpg",
		Source:       "saved",
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Verify the post was saved
	retrieved, err := db.GetPost(ctx, post.ID)
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to be saved, got nil")
	}

	if retrieved.ID != post.ID {
		t.Errorf("Expected ID %s, got %s", post.ID, retrieved.ID)
	}
	if retrieved.Title != post.Title {
		t.Errorf("Expected Title %s, got %s", post.Title, retrieved.Title)
	}
	if retrieved.Subreddit != post.Subreddit {
		t.Errorf("Expected Subreddit %s, got %s", post.Subreddit, retrieved.Subreddit)
	}
}

func TestSavePost_Update(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	post := &Post{
		ID:           "abc123",
		Title:        "Test Post",
		Subreddit:    "test",
		Author:       "testuser",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}

	// Save initial post
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Update the post
	post.Title = "Updated Title"
	post.Subreddit = "updated"

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to update post: %v", err)
	}

	// Verify the update
	retrieved, err := db.GetPost(ctx, post.ID)
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved.Title != "Updated Title" {
		t.Errorf("Expected Title 'Updated Title', got %s", retrieved.Title)
	}
	if retrieved.Subreddit != "updated" {
		t.Errorf("Expected Subreddit 'updated', got %s", retrieved.Subreddit)
	}
}

func TestGetPost_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	retrieved, err := db.GetPost(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected nil for non-existent post")
	}
}

func TestIsDownloaded(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Initially should not exist
	exists, err := db.IsDownloaded(ctx, "abc123")
	if err != nil {
		t.Fatalf("Failed to check if downloaded: %v", err)
	}
	if exists {
		t.Error("Expected post to not exist initially")
	}

	// Save a post
	post := &Post{
		ID:           "abc123",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Now it should exist
	exists, err = db.IsDownloaded(ctx, "abc123")
	if err != nil {
		t.Fatalf("Failed to check if downloaded: %v", err)
	}
	if !exists {
		t.Error("Expected post to exist after saving")
	}
}

func TestGetStats(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Initially should be empty
	stats, err := db.GetStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.TotalPosts != 0 {
		t.Errorf("Expected 0 total posts, got %d", stats.TotalPosts)
	}

	// Save multiple posts
	posts := []*Post{
		{ID: "post1", Subreddit: "pics", MediaType: "image", Source: "saved", DownloadedAt: time.Now()},
		{ID: "post2", Subreddit: "pics", MediaType: "image", Source: "saved", DownloadedAt: time.Now()},
		{ID: "post3", Subreddit: "videos", MediaType: "video", Source: "upvoted", DownloadedAt: time.Now()},
		{ID: "post4", Subreddit: "gifs", MediaType: "gif", Source: "upvoted", DownloadedAt: time.Now()},
	}

	for _, post := range posts {
		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	// Get stats
	stats, err = db.GetStats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalPosts != 4 {
		t.Errorf("Expected 4 total posts, got %d", stats.TotalPosts)
	}

	if stats.PostsBySource["saved"] != 2 {
		t.Errorf("Expected 2 saved posts, got %d", stats.PostsBySource["saved"])
	}

	if stats.PostsBySource["upvoted"] != 2 {
		t.Errorf("Expected 2 upvoted posts, got %d", stats.PostsBySource["upvoted"])
	}

	if stats.PostsBySubreddit["pics"] != 2 {
		t.Errorf("Expected 2 posts in pics, got %d", stats.PostsBySubreddit["pics"])
	}

	if stats.PostsByMediaType["image"] != 2 {
		t.Errorf("Expected 2 images, got %d", stats.PostsByMediaType["image"])
	}
}

func TestImportFromIDList(t *testing.T) {
	db, tempDir := setupTestDB(t)
	ctx := context.Background()

	// Create a test idList file
	idListPath := filepath.Join(tempDir, "idList.txt")
	content := `# This is a comment
abc123
def456
ghi789

# Another comment
jkl012
abc123  # duplicate, should be ignored
`
	if err := os.WriteFile(idListPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write idList file: %v", err)
	}

	imported, err := db.ImportFromIDList(ctx, idListPath)
	if err != nil {
		t.Fatalf("Failed to import from ID list: %v", err)
	}
	if imported != 4 {
		t.Errorf("Expected 4 imported posts, got %d", imported)
	}

	// Verify posts were imported
	for _, id := range []string{"abc123", "def456", "ghi789", "jkl012"} {
		exists, err := db.IsDownloaded(ctx, id)
		if err != nil {
			t.Fatalf("Failed to check if downloaded: %v", err)
		}
		if !exists {
			t.Errorf("Expected post %s to exist", id)
		}
	}

	// Import again - should not import duplicates
	imported, err = db.ImportFromIDList(ctx, idListPath)
	if err != nil {
		t.Fatalf("Failed to import from ID list: %v", err)
	}
	if imported != 0 {
		t.Errorf("Expected 0 new imports (all duplicates), got %d", imported)
	}
}

func TestImportFromDirectory(t *testing.T) {
	db, tempDir := setupTestDB(t)
	ctx := context.Background()

	// Create test files with bdfr-html naming patterns
	testFiles := []string{
		"abc123.jpg",
		"def456_1.mp4",
		"ghi789_2.png",
		"jkl012.gif",
		"mno345_1.gifv",
		"a.txt",
		"hello.txt",
	}

	for _, filename := range testFiles {
		path := filepath.Join(tempDir, filename)
		if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}
	}

	imported, err := db.ImportFromDirectory(ctx, tempDir)
	if err != nil {
		t.Fatalf("Failed to import from directory: %v", err)
	}
	if imported != 5 {
		t.Errorf("Expected 5 imported posts, got %d", imported)
	}

	// Verify posts were imported with correct media types
	testCases := []struct {
		id        string
		mediaType string
	}{
		{"abc123", "image"},
		{"def456", "video"},
		{"ghi789", "image"},
		{"jkl012", "image"},
		{"mno345", "gif"},
	}

	for _, tc := range testCases {
		post, err := db.GetPost(ctx, tc.id)
		if err != nil {
			t.Fatalf("Failed to get post: %v", err)
		}
		if post == nil {
			t.Errorf("Expected post %s to exist", tc.id)
			continue
		}
		if post.MediaType != tc.mediaType {
			t.Errorf("Expected post %s to have media type %s, got %s", tc.id, tc.mediaType, post.MediaType)
		}
	}

	// Import again - should not import duplicates
	imported, err = db.ImportFromDirectory(ctx, tempDir)
	if err != nil {
		t.Fatalf("Failed to import from directory: %v", err)
	}
	if imported != 0 {
		t.Errorf("Expected 0 new imports (all duplicates), got %d", imported)
	}
}

func TestImportFromDirectory_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	nonExistentPath := "/nonexistent/path/123456"
	_, err := db.ImportFromDirectory(ctx, nonExistentPath)
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestImportFromIDList_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	nonExistentPath := "/nonexistent/path/idList.txt"
	_, err := db.ImportFromIDList(ctx, nonExistentPath)
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestFilenamePattern(t *testing.T) {
	testCases := []struct {
		filename    string
		expectID    string
		shouldMatch bool
	}{
		{"abc123.jpg", "abc123", true},
		{"def456_1.mp4", "def456", true},
		{"ghi789_2.png", "ghi789", true},
		{"jkl012.gif", "jkl012", true},
		{"mno345_10.webp", "mno345", true},
		{"readme.txt", "readme", true},
		{"a.txt", "", false},
		{".hidden", "", false},
		{"noextension", "", false},
		{"_123.jpg", "", false},
		{"12345.jpg", "", false},
	}

	for _, tc := range testCases {
		matches := filenamePattern.FindStringSubmatch(tc.filename)
		if tc.shouldMatch {
			if matches == nil {
				t.Errorf("Expected %s to match pattern", tc.filename)
				continue
			}
			if matches[1] != tc.expectID {
				t.Errorf("Expected ID %s for %s, got %s", tc.expectID, tc.filename, matches[1])
			}
		} else {
			if matches != nil {
				t.Errorf("Expected %s to NOT match pattern", tc.filename)
			}
		}
	}
}

func TestClose(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "reddit-media-downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Errorf("Failed to close database: %v", err)
	}

	// Verify connection is closed by trying to use it
	ctx := context.Background()
	_, err = db.IsDownloaded(ctx, "test")
	if err == nil {
		t.Error("Expected error when using closed database")
	}
}

func TestMigration_AddsColumns(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Verify columns exist by querying PRAGMA table_info
	rows, err := db.conn.QueryContext(ctx, "PRAGMA table_info(posts)")
	if err != nil {
		t.Fatalf("Failed to query table info: %v", err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var type_ string
		var notnull int
		var dflt_value interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &type_, &notnull, &dflt_value, &pk); err != nil {
			t.Fatalf("Failed to scan table info: %v", err)
		}
		columns[name] = true
	}

	// Check that new columns exist
	requiredColumns := []string{"retry_count", "last_error", "last_attempt"}
	for _, col := range requiredColumns {
		if !columns[col] {
			t.Errorf("Expected column %s to exist after migration", col)
		}
	}
}

func TestMigration_Idempotent(t *testing.T) {
	db, _ := setupTestDB(t)

	// Run migration twice - should not fail
	if err := db.runMigrations(); err != nil {
		t.Fatalf("First migration run failed: %v", err)
	}
	if err := db.runMigrations(); err != nil {
		t.Fatalf("Second migration run failed (should be idempotent): %v", err)
	}
}

func TestMigration_PreservesExistingData(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Save a post before migration
	post := &Post{
		ID:           "test123",
		Title:        "Test Post",
		Subreddit:    "test",
		Author:       "testuser",
		URL:          "https://example.com/image.jpg",
		Permalink:    "/r/test/comments/test123/test_post/",
		CreatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DownloadedAt: time.Now(),
		MediaType:    "image",
		FilePath:     "/downloads/test123.jpg",
		Source:       "saved",
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Run migration (simulating a fresh database with existing data)
	if err := db.runMigrations(); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Verify the post still exists and data is preserved
	retrieved, err := db.GetPost(ctx, "test123")
	if err != nil {
		t.Fatalf("Failed to get post after migration: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Post lost after migration")
	}
	if retrieved.ID != post.ID {
		t.Errorf("Expected ID %s, got %s", post.ID, retrieved.ID)
	}
	if retrieved.Title != post.Title {
		t.Errorf("Expected Title %s, got %s", post.Title, retrieved.Title)
	}
}

func TestMigration_ExistingDatabase(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "reddit-media-downloader-migration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")

	// Create database with old schema (without new columns)
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer conn.Close()

	oldSchema := `
	CREATE TABLE IF NOT EXISTS posts (
		id TEXT PRIMARY KEY,
		title TEXT,
		subreddit TEXT,
		author TEXT,
		url TEXT,
		permalink TEXT,
		created_at INTEGER,
		downloaded_at INTEGER,
		media_type TEXT,
		file_path TEXT,
		source TEXT
	);
	`
	if _, err := conn.Exec(oldSchema); err != nil {
		t.Fatalf("Failed to create old schema: %v", err)
	}

	// Insert some test data
	if _, err := conn.Exec(`INSERT INTO posts (id, title, subreddit) VALUES (?, ?, ?)`,
		"oldpost1", "Old Post 1", "oldsub"); err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Now open with NewDB which should run migration
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database with migration: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Verify the old data still exists
	retrieved, err := db.GetPost(ctx, "oldpost1")
	if err != nil {
		t.Fatalf("Failed to get old post: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Old post lost after migration")
	}
	if retrieved.Title != "Old Post 1" {
		t.Errorf("Expected title 'Old Post 1', got %s", retrieved.Title)
	}

	// Verify new columns exist and have default values
	var retryCount int64
	var lastError sql.NullString
	var lastAttempt sql.NullInt64

	err = conn.QueryRowContext(ctx, `SELECT retry_count, last_error, last_attempt FROM posts WHERE id = ?`,
		"oldpost1").Scan(&retryCount, &lastError, &lastAttempt)
	if err != nil {
		t.Fatalf("Failed to query new columns: %v", err)
	}

	if retryCount != 0 {
		t.Errorf("Expected retry_count default 0, got %d", retryCount)
	}
}

func TestSetMetadata(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Set a new metadata value
	if err := db.SetMetadata(ctx, "migration_complete", "true"); err != nil {
		t.Fatalf("Failed to set metadata: %v", err)
	}

	// Verify it was set
	value, err := db.GetMetadata(ctx, "migration_complete")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if value != "true" {
		t.Errorf("Expected 'true', got %s", value)
	}
}

func TestSetMetadata_Update(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Set initial value
	if err := db.SetMetadata(ctx, "test_key", "initial"); err != nil {
		t.Fatalf("Failed to set metadata: %v", err)
	}

	// Update the value
	if err := db.SetMetadata(ctx, "test_key", "updated"); err != nil {
		t.Fatalf("Failed to update metadata: %v", err)
	}

	// Verify the update
	value, err := db.GetMetadata(ctx, "test_key")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if value != "updated" {
		t.Errorf("Expected 'updated', got %s", value)
	}
}

func TestGetMetadata_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Get a non-existent key
	value, err := db.GetMetadata(ctx, "nonexistent_key")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if value != "" {
		t.Errorf("Expected empty string for non-existent key, got %s", value)
	}
}

func TestMetadata_MultipleKeys(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Set multiple metadata values
	metadata := map[string]string{
		"migration_complete": "true",
		"full_sync_once":     "false",
		"last_sync":          "2024-01-01",
	}

	for key, value := range metadata {
		if err := db.SetMetadata(ctx, key, value); err != nil {
			t.Fatalf("Failed to set metadata for %s: %v", key, err)
		}
	}

	// Verify all values
	for key, expectedValue := range metadata {
		value, err := db.GetMetadata(ctx, key)
		if err != nil {
			t.Fatalf("Failed to get metadata for %s: %v", key, err)
		}
		if value != expectedValue {
			t.Errorf("Expected %s for key %s, got %s", expectedValue, key, value)
		}
	}
}

func TestIncrementRetry(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post first
	post := &Post{
		ID:           "retrytest1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// First increment
	if err := db.IncrementRetry(ctx, "retrytest1", "network error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// Verify retry count is 1
	count, err := db.GetRetryCount(ctx, "retrytest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected retry count 1, got %d", count)
	}

	// Verify post has error recorded
	retrieved, err := db.GetPost(ctx, "retrytest1")
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to exist")
	}
	if retrieved.LastError != "network error" {
		t.Errorf("Expected last_error 'network error', got %s", retrieved.LastError)
	}
	if retrieved.RetryCount != 1 {
		t.Errorf("Expected RetryCount 1, got %d", retrieved.RetryCount)
	}
	if retrieved.LastAttempt.IsZero() {
		t.Error("Expected LastAttempt to be set")
	}

	// Second increment
	if err := db.IncrementRetry(ctx, "retrytest1", "timeout error"); err != nil {
		t.Fatalf("Failed to increment retry second time: %v", err)
	}

	count, err = db.GetRetryCount(ctx, "retrytest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected retry count 2, got %d", count)
	}
}

func TestIncrementRetry_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	err := db.IncrementRetry(ctx, "nonexistent", "some error")
	if err == nil {
		t.Error("Expected error for non-existent post")
	}
}

func TestResetRetry(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post and increment retry
	post := &Post{
		ID:           "resettest1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry a few times
	for i := 0; i < 3; i++ {
		if err := db.IncrementRetry(ctx, "resettest1", fmt.Sprintf("error %d", i)); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// Verify retry count is 3
	count, err := db.GetRetryCount(ctx, "resettest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected retry count 3, got %d", count)
	}

	// Reset retry
	if err := db.ResetRetry(ctx, "resettest1"); err != nil {
		t.Fatalf("Failed to reset retry: %v", err)
	}

	// Verify retry count is 0
	count, err = db.GetRetryCount(ctx, "resettest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected retry count 0 after reset, got %d", count)
	}

	// Verify error fields are cleared
	retrieved, err := db.GetPost(ctx, "resettest1")
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to exist")
	}
	if retrieved.LastError != "" {
		t.Errorf("Expected last_error to be empty, got %s", retrieved.LastError)
	}
	if !retrieved.LastAttempt.IsZero() {
		t.Error("Expected LastAttempt to be zero after reset")
	}
}

func TestResetRetry_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	err := db.ResetRetry(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent post")
	}
}

func TestGetRetryCount(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Test non-existent post
	count, err := db.GetRetryCount(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 for non-existent post, got %d", count)
	}

	// Create a post
	post := &Post{
		ID:           "counttest1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// New post should have retry count 0
	count, err = db.GetRetryCount(ctx, "counttest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 for new post, got %d", count)
	}

	// Increment and verify
	if err := db.IncrementRetry(ctx, "counttest1", "error1"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}
	count, err = db.GetRetryCount(ctx, "counttest1")
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1, got %d", count)
	}
}

func TestGetPostsToRetry_Empty(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	posts, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	if len(posts) != 0 {
		t.Errorf("Expected 0 posts, got %d", len(posts))
	}
}

func TestGetPostsToRetry_NeverAttempted(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create posts that were never retried
	posts := []*Post{
		{ID: "newpost1", DownloadedAt: time.Now(), Source: "saved"},
		{ID: "newpost2", DownloadedAt: time.Now(), Source: "saved"},
		{ID: "newpost3", DownloadedAt: time.Now(), Source: "saved"},
	}
	for _, post := range posts {
		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	// All new posts should be eligible
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("Expected 3 posts, got %d", len(result))
	}
}

func TestGetPostsToRetry_ExceedsThreshold(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post and increment retry beyond threshold
	post := &Post{
		ID:           "threshold1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment beyond threshold (threshold = 3)
	for i := 0; i < 3; i++ {
		if err := db.IncrementRetry(ctx, "threshold1", fmt.Sprintf("error %d", i)); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// Should not be eligible (retry_count >= threshold)
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	for _, id := range result {
		if id == "threshold1" {
			t.Error("Post exceeding threshold should not be eligible for retry")
		}
	}
}

func TestGetPostsToRetry_Backoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "backoff1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry once
	if err := db.IncrementRetry(ctx, "backoff1", "error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// With 1 second base backoff, should not be eligible immediately
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	for _, id := range result {
		if id == "backoff1" {
			t.Error("Post should not be eligible immediately after attempt")
		}
	}
}

func TestGetPostsToRetry_AfterBackoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "backoff2",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry once with past timestamp
	if err := db.IncrementRetry(ctx, "backoff2", "error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// Manually update last_attempt to 3 seconds ago (backoff is 1s, with 1s margin requires 2s elapsed)
	pastTime := time.Now().Add(-3 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "backoff2"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "backoff2", err)
	}

	// Now should be eligible
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}

	found := false
	for _, id := range result {
		if id == "backoff2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Post should be eligible after backoff period")
	}
}

func TestGetPostsToRetry_ExponentialBackoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "exponential1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry twice (retry_count = 2)
	for i := 0; i < 2; i++ {
		if err := db.IncrementRetry(ctx, "exponential1", fmt.Sprintf("error %d", i)); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// With base backoff of 1 second and retry_count = 2:
	// backoff = 1s * 2^(2-1) = 2 seconds
	// With 1s margin, need 3s elapsed to be eligible
	// Update last_attempt to 2 seconds ago (less than 3 second requirement)
	pastTime := time.Now().Add(-2 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "exponential1"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "exponential1", err)
	}

	// Should NOT be eligible yet (2s < 3s requirement)
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	for _, id := range result {
		if id == "exponential1" {
			t.Error("Post should not be eligible before exponential backoff expires")
		}
	}

	// Update to 4 seconds ago (more than 3 second requirement)
	pastTime = time.Now().Add(-4 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "exponential1"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "exponential1", err)
	}

	// Now should be eligible
	result, err = db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	found := false
	for _, id := range result {
		if id == "exponential1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Post should be eligible after exponential backoff expires")
	}
}

func TestGetPostsToRetry_MaxBackoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "maxbackoff1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry 5 times (retry_count = 5, below threshold of 10)
	for i := 0; i < 5; i++ {
		if err := db.IncrementRetry(ctx, "maxbackoff1", fmt.Sprintf("error %d", i)); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// With base backoff of 1 second and retry_count = 5:
	// raw backoff = 1s * 2^(5-1) = 16 seconds
	// max backoff = 60 seconds, so backoff is 16s (not capped yet)
	// With 1s margin, need 17s elapsed to be eligible
	// Update last_attempt to 16 seconds ago (less than 17s requirement)
	pastTime := time.Now().Add(-16 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "maxbackoff1"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "maxbackoff1", err)
	}

	// Should NOT be eligible yet (16s < 17s requirement)
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 10)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	for _, id := range result {
		if id == "maxbackoff1" {
			t.Error("Post should not be eligible before backoff expires")
		}
	}

	// Update to 18 seconds ago (more than 17s requirement)
	pastTime = time.Now().Add(-18 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "maxbackoff1"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "maxbackoff1", err)
	}

	// Now should be eligible
	result, err = db.GetPostsToRetry(ctx, time.Second, time.Minute, 10)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}
	found := false
	for _, id := range result {
		if id == "maxbackoff1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Post should be eligible after backoff expires")
	}
}

func TestGetPostsToRetry_Mixed(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create various posts
	testCases := []struct {
		id           string
		retryCount   int
		lastAttempt  time.Duration
		shouldReturn bool
	}{
		// Never attempted - should return
		{"mixed1", 0, 0, true},
		// Recently attempted (500ms ago, backoff is 1s) - still in backoff window, should not return
		{"mixed2", 1, -500 * time.Millisecond, false},
		// Just outside backoff window (2s ago, backoff is 1s + 1s margin) - should return
		{"mixed3", 1, -2 * time.Second, true},
		// Old attempt (120s ago, backoff is 1s) - should return
		{"mixed4", 1, -120 * time.Second, true},
		// Exceeds threshold - should not return
		{"mixed5", 5, -300 * time.Second, false},
	}

	for _, tc := range testCases {
		post := &Post{
			ID:           tc.id,
			DownloadedAt: time.Now(),
			Source:       "saved",
		}
		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post %s: %v", tc.id, err)
		}

		if tc.retryCount > 0 {
			for i := 0; i < tc.retryCount; i++ {
				if err := db.IncrementRetry(ctx, tc.id, "error"); err != nil {
					t.Fatalf("Failed to increment retry for %s: %v", tc.id, err)
				}
			}
		}

		if tc.lastAttempt != 0 {
			pastTime := time.Now().Add(tc.lastAttempt).Unix()
			if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, tc.id); err != nil {
				t.Fatalf("failed to set last_attempt for id %s: %v", tc.id, err)
			}
		}
	}

	// Get posts to retry with 1 second base, 1 minute max, threshold 3
	result, err := db.GetPostsToRetry(ctx, time.Second, time.Minute, 3)
	if err != nil {
		t.Fatalf("Failed to get posts to retry: %v", err)
	}

	// Verify results
	returned := make(map[string]bool)
	for _, id := range result {
		returned[id] = true
	}

	for _, tc := range testCases {
		has := returned[tc.id]
		if tc.shouldReturn && !has {
			t.Errorf("Expected %s to be returned", tc.id)
		}
		if !tc.shouldReturn && has {
			t.Errorf("Expected %s to NOT be returned", tc.id)
		}
	}
}

func TestCheckPostStatus_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	status, err := db.CheckPostStatus(ctx, "nonexistent", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if status.Exists {
		t.Error("Expected Exists=false for non-existent post")
	}
	if !status.RetryEligible {
		t.Error("Expected RetryEligible=true for non-existent post")
	}
	if status.ShouldSkip {
		t.Error("Expected ShouldSkip=false for non-existent post")
	}
}

func TestCheckPostStatus_FileExists(t *testing.T) {
	db, tempDir := setupTestDB(t)
	ctx := context.Background()

	// Create a file on disk
	testFile := filepath.Join(tempDir, "testfile.jpg")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a post with file_path pointing to existing file
	post := &Post{
		ID:           "fileexists1",
		DownloadedAt: time.Now(),
		Source:       "saved",
		FilePath:     testFile,
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	status, err := db.CheckPostStatus(ctx, "fileexists1", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if !status.FileExists {
		t.Error("Expected FileExists=true")
	}
	if !status.ShouldSkip {
		t.Error("Expected ShouldSkip=true when file exists")
	}
	if status.RetryEligible {
		t.Error("Expected RetryEligible=false when file exists")
	}
}

func TestCheckPostStatus_FileMissing(t *testing.T) {
	db, tempDir := setupTestDB(t)
	ctx := context.Background()

	// Create a post with file_path pointing to non-existent file
	nonExistentFile := filepath.Join(tempDir, "missingfile.jpg")
	post := &Post{
		ID:           "filemissing1",
		DownloadedAt: time.Now(),
		Source:       "saved",
		FilePath:     nonExistentFile,
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	status, err := db.CheckPostStatus(ctx, "filemissing1", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if status.FileExists {
		t.Error("Expected FileExists=false for missing file")
	}
	if status.ShouldSkip {
		t.Error("Expected ShouldSkip=false when file is missing")
	}
	if !status.RetryEligible {
		t.Error("Expected RetryEligible=true when file is missing")
	}
}

func TestCheckPostStatus_ExceedsThreshold(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "threshold1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry beyond threshold (threshold = 3, so 4 retries = exceeded)
	for i := 0; i < 4; i++ {
		if err := db.IncrementRetry(ctx, "threshold1", fmt.Sprintf("error %d", i)); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	status, err := db.CheckPostStatus(ctx, "threshold1", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if status.RetryCount != 4 {
		t.Errorf("Expected RetryCount=4, got %d", status.RetryCount)
	}
	if !status.ShouldSkip {
		t.Error("Expected ShouldSkip=true when exceeding threshold")
	}
	if status.RetryEligible {
		t.Error("Expected RetryEligible=false when exceeding threshold")
	}
}

func TestCheckPostStatus_WithinBackoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "backoff1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry once (retry_count = 1, so backoff = 1s * 2^(1-1) = 1s)
	if err := db.IncrementRetry(ctx, "backoff1", "error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// Check immediately - should be within backoff
	status, err := db.CheckPostStatus(ctx, "backoff1", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if !status.ShouldSkip {
		t.Error("Expected ShouldSkip=true when within backoff window")
	}
	if status.RetryEligible {
		t.Error("Expected RetryEligible=false when within backoff window")
	}
}

func TestCheckPostStatus_AfterBackoff(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post
	post := &Post{
		ID:           "backoff2",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry once
	if err := db.IncrementRetry(ctx, "backoff2", "error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// Manually set last_attempt to 3 seconds ago (backoff is 1s, so 3s > 1s)
	pastTime := time.Now().Add(-3 * time.Second).Unix()
	if _, err := db.conn.ExecContext(ctx, "UPDATE posts SET last_attempt = ? WHERE id = ?", pastTime, "backoff2"); err != nil {
		t.Fatalf("failed to set last_attempt for id %s: %v", "backoff2", err)
	}

	status, err := db.CheckPostStatus(ctx, "backoff2", 3, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if status.ShouldSkip {
		t.Error("Expected ShouldSkip=false when after backoff window")
	}
	if !status.RetryEligible {
		t.Error("Expected RetryEligible=true when after backoff window")
	}
}

func TestCheckPostStatus_NoBackoffParams(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post with retry history
	post := &Post{
		ID:           "nobackoff1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry
	if err := db.IncrementRetry(ctx, "nobackoff1", "error"); err != nil {
		t.Fatalf("Failed to increment retry: %v", err)
	}

	// Check with zero backoff params - should not consider backoff
	status, err := db.CheckPostStatus(ctx, "nobackoff1", 0, 0, 0)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if status.ShouldSkip {
		t.Error("Expected ShouldSkip=false when no backoff params provided")
	}
	if !status.RetryEligible {
		t.Error("Expected RetryEligible=true when no backoff params provided")
	}
}

func TestCheckPostStatus_NoThreshold(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create a post with many retries
	post := &Post{
		ID:           "nothreshold1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Increment retry many times
	for i := 0; i < 10; i++ {
		if err := db.IncrementRetry(ctx, "nothreshold1", "error"); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// Check with zero threshold - should not skip due to threshold
	status, err := db.CheckPostStatus(ctx, "nothreshold1", 0, 0, 0)
	if err != nil {
		t.Fatalf("Failed to check post status: %v", err)
	}

	if !status.Exists {
		t.Error("Expected Exists=true")
	}
	if status.RetryCount != 10 {
		t.Errorf("Expected RetryCount=10, got %d", status.RetryCount)
	}
	if status.ShouldSkip {
		t.Error("Expected ShouldSkip=false when threshold is 0")
	}
	if !status.RetryEligible {
		t.Error("Expected RetryEligible=true when threshold is 0")
	}
}

func TestIsDownloaded_BackwardCompatibility(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Test 1: Non-existent post
	exists, err := db.IsDownloaded(ctx, "compat1")
	if err != nil {
		t.Fatalf("Failed to check IsDownloaded: %v", err)
	}
	if exists {
		t.Error("Expected false for non-existent post")
	}

	// Test 2: Post exists (simple case - no file, no retries)
	post := &Post{
		ID:           "compat1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	exists, err = db.IsDownloaded(ctx, "compat1")
	if err != nil {
		t.Fatalf("Failed to check IsDownloaded: %v", err)
	}
	// Post exists but has no file_path and no retry history
	// For backward compatibility, this is treated as downloaded (legacy behavior)
	if !exists {
		t.Error("Expected true for post without file_path (backward compatibility)")
	}
}

func TestIsDownloaded_WithFile(t *testing.T) {
	db, tempDir := setupTestDB(t)
	ctx := context.Background()

	// Create a file
	testFile := filepath.Join(tempDir, "test.jpg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create post with file
	post := &Post{
		ID:           "filedl1",
		DownloadedAt: time.Now(),
		Source:       "saved",
		FilePath:     testFile,
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	exists, err := db.IsDownloaded(ctx, "filedl1")
	if err != nil {
		t.Fatalf("Failed to check IsDownloaded: %v", err)
	}
	if !exists {
		t.Error("Expected true when file exists on disk")
	}
}

func TestIsDownloaded_ExceedsThreshold(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Create post with retries exceeding threshold
	post := &Post{
		ID:           "thresholddl1",
		DownloadedAt: time.Now(),
		Source:       "saved",
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Add 5 retries
	for i := 0; i < 5; i++ {
		if err := db.IncrementRetry(ctx, "thresholddl1", "error"); err != nil {
			t.Fatalf("Failed to increment retry: %v", err)
		}
	}

	// IsDownloaded uses CheckPostStatus with 0 params, so threshold=0
	// With threshold=0, retry count of 5 is NOT > 0, so it won't skip
	exists, err := db.IsDownloaded(ctx, "thresholddl1")
	if err != nil {
		t.Fatalf("Failed to check IsDownloaded: %v", err)
	}
	// Since threshold is 0, 5 is not > 0, so post is eligible for retry
	if exists {
		t.Error("Expected false when using default IsDownloaded (threshold=0)")
	}
}
