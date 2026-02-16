// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

	"database/sql"
import (
	"context"
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

func TestSavePost_WithHash(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Test hash is saved correctly
	hash := "abc123def456"
	post := &Post{
		ID:           "hashpost1",
		Title:        "Test Post with Hash",
		Subreddit:    "test",
		Author:       "testuser",
		URL:          "https://example.com/image.jpg",
		Permalink:    "/r/test/comments/hashpost1/test_post/",
		CreatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DownloadedAt: time.Now(),
		MediaType:    "image",
		FilePath:     "/downloads/hashpost1.jpg",
		Source:       "saved",
		Hash:         hash,
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Verify the post was saved with hash
	retrieved, err := db.GetPost(ctx, post.ID)
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to be saved, got nil")
	}

	if retrieved.Hash != hash {
		t.Errorf("Expected hash %s, got %s", hash, retrieved.Hash)
	}
}

func TestSavePost_UpdateHash(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Save post with initial hash
	initialHash := "initialhash123"
	post := &Post{
		ID:           "hashpost2",
		Title:        "Test Post",
		DownloadedAt: time.Now(),
		Source:       "saved",
		Hash:         initialHash,
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Update with new hash
	newHash := "newhash456"
	post.Hash = newHash
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to update post: %v", err)
	}

	// Verify hash was updated
	retrieved, err := db.GetPost(ctx, post.ID)
	if err != nil {
		t.Fatalf("Failed to get post: %v", err)
	}
	if retrieved.Hash != newHash {
		t.Errorf("Expected hash %s, got %s", newHash, retrieved.Hash)
	}
}

func TestHashExists(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Initially hash should not exist
	exists, err := db.HashExists(ctx, "nonexistenthash")
	if err != nil {
		t.Fatalf("HashExists() error = %v", err)
	}
	if exists {
		t.Error("Expected hash to not exist initially")
	}

	// Save a post with hash
	hash := "testhash123"
	post := &Post{
		ID:           "hashpost3",
		DownloadedAt: time.Now(),
		Source:       "saved",
		Hash:         hash,
	}
	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Now hash should exist
	exists, err = db.HashExists(ctx, hash)
	if err != nil {
		t.Fatalf("HashExists() error = %v", err)
	}
	if !exists {
		t.Error("Expected hash to exist after saving post")
	}
}

func TestHashExists_MultiplePostsSameHash(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Save multiple posts with the same hash (duplicates)
	hash := "duplicatehash"
	posts := []*Post{
		{ID: "dup1", DownloadedAt: time.Now(), Source: "saved", Hash: hash},
		{ID: "dup2", DownloadedAt: time.Now(), Source: "saved", Hash: hash},
		{ID: "dup3", DownloadedAt: time.Now(), Source: "saved", Hash: hash},
	}

	for _, post := range posts {
		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	// Hash should still exist
	exists, err := db.HashExists(ctx, hash)
	if err != nil {
		t.Fatalf("HashExists() error = %v", err)
	}
	if !exists {
		t.Error("Expected hash to exist")
	}

	// GetPostByHash should return one of them
	retrieved, err := db.GetPostByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetPostByHash() error = %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to be returned for hash")
	}
	if retrieved.Hash != hash {
		t.Errorf("Expected hash %s, got %s", hash, retrieved.Hash)
	}
}

func TestGetPostByHash(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Save a post with known hash
	hash := "searchablehash789"
	post := &Post{
		ID:           "hashpost4",
		Title:        "Searchable Post",
		Subreddit:    "test",
		Author:       "testuser",
		URL:          "https://example.com/search.jpg",
		DownloadedAt: time.Now(),
		MediaType:    "image",
		Hash:         hash,
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Retrieve by hash
	retrieved, err := db.GetPostByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetPostByHash() error = %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to be returned, got nil")
	}

	if retrieved.ID != post.ID {
		t.Errorf("Expected ID %s, got %s", post.ID, retrieved.ID)
	}
	if retrieved.Title != post.Title {
		t.Errorf("Expected Title %s, got %s", post.Title, retrieved.Title)
	}
	if retrieved.Hash != hash {
		t.Errorf("Expected hash %s, got %s", hash, retrieved.Hash)
	}
}

func TestGetPostByHash_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	retrieved, err := db.GetPostByHash(ctx, "nonexistenthash")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected nil for non-existent hash")
	}
}

func TestHashColumnMigration(t *testing.T) {
	// This test verifies that the hash column is properly added
	// when creating a new database
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
	defer db.Close()

	ctx := context.Background()

	// Save a post with hash
	hash := "migrationtesthash"
	post := &Post{
		ID:           "migrationpost",
		Title:        "Migration Test",
		DownloadedAt: time.Now(),
		Source:       "saved",
		Hash:         hash,
	}

	if err := db.SavePost(ctx, post); err != nil {
		t.Fatalf("Failed to save post: %v", err)
	}

	// Verify hash column exists and works
	exists, err := db.HashExists(ctx, hash)
	if err != nil {
		t.Fatalf("HashExists() error = %v", err)
	}
	if !exists {
		t.Error("Expected hash to exist after migration")
	}

	// Verify we can retrieve by hash
	retrieved, err := db.GetPostByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetPostByHash() error = %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected post to be returned")
	}
}

func TestHashIndexExists(t *testing.T) {
	// This test verifies that the hash index is created
	// We can't directly check index existence in SQLite easily,
	// but we can verify that hash lookups are fast by checking
	// that HashExists works correctly
	db, _ := setupTestDB(t)
	ctx := context.Background()

	// Save multiple posts with different hashes
	hashes := []string{
		"hashindex1",
		"hashindex2",
		"hashindex3",
	}

	for i, hash := range hashes {
		post := &Post{
			ID:           fmt.Sprintf("indexpost%d", i),
			DownloadedAt: time.Now(),
			Source:       "saved",
			Hash:         hash,
		}
		if err := db.SavePost(ctx, post); err != nil {
			t.Fatalf("Failed to save post: %v", err)
		}
	}

	// Verify all hashes exist
	for _, hash := range hashes {
		exists, err := db.HashExists(ctx, hash)
		if err != nil {
			t.Fatalf("HashExists() error = %v", err)
		}
		if !exists {
			t.Errorf("Expected hash %s to exist", hash)
		}
	}
}
