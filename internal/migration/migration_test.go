package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPostID(t *testing.T) {
	tests := []struct {
		filename string
		want     string
		wantErr  bool
	}{
		{"Test Post_1lgwfyg.jpg", "1lgwfyg", false},
		{"So sweet 🤗_188x0kd.jpg", "188x0kd", false},
		{"I couldn't post this_1r0z7xp.jpeg", "1r0z7xp", false},
		{"Going all the way_1r4m3qf.mp4", "1r4m3qf", false},
		{"Celebrating 2 years_1r42ye0_1.jpg", "1r42ye0", false},
		{"test_file.jpg", "", true},
		{"test.jpg", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got, err := ExtractPostID(tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractPostID(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractPostID(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example_sub", "example_sub"},
		{"r/example_sub", "r_example_sub"},
		{"u_example_user", "u_example_user"},
		{"example_sub2", "example_sub2"},
		{"example_sub3", "example_sub3"},
		{"special!@#chars", "special___chars"},
		{"", "unknown"},
		{"___", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizePath(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHTMLParser(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "index.html")

	htmlContent := `<html><body>
<div class=post>
    <a href="1r4wjj5.html"><h1>Test Post</h1></a>
    <span class="subreddit">r/example_sub</span>
    <span class="user">u/example_user2</span>
</div>
<div class=post>
    <a href="1r0z7xp.html"><h1>User Post</h1></a>
    <span class="subreddit">r/u_example_user</span>
    <span class="user">u/example_user</span>
</div>
</body></html>`

	if err := os.WriteFile(indexPath, []byte(htmlContent), 0644); err != nil {
		t.Fatal(err)
	}

	parser := NewHTMLParser()
	if err := parser.ParseIndexHTML(indexPath); err != nil {
		t.Fatal(err)
	}

	// Regular post
	if info, ok := parser.PostMap["1r4wjj5"]; !ok {
		t.Error("Missing regular post")
	} else {
		if info.Subreddit != "example_sub" {
			t.Errorf("Subreddit = %s, want example_sub", info.Subreddit)
		}
		if info.Username != "example_user2" {
			t.Errorf("Username = %s, want example_user2", info.Username)
		}
		if info.IsUserPost {
			t.Error("IsUserPost should be false")
		}
	}

	// User post
	if info, ok := parser.PostMap["1r0z7xp"]; !ok {
		t.Error("Missing user post")
	} else {
		if info.Subreddit != "u_example_user" {
			t.Errorf("Subreddit = %s, want u_example_user", info.Subreddit)
		}
		if info.Username != "example_user" {
			t.Errorf("Username = %s, want example_user", info.Username)
		}
		if !info.IsUserPost {
			t.Error("IsUserPost should be true")
		}
	}
}

func TestMigratorDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}
	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, true)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	// File should still exist in dry-run
	if _, err := os.Stat(testFile); err != nil {
		t.Error("Source file should exist in dry-run")
	}

	// Should have dry_run status
	if len(migrator.Log.Operations) != 1 {
		t.Fatalf("Expected 1 operation, got %d", len(migrator.Log.Operations))
	}
	if migrator.Log.Operations[0].Status != "dry_run" {
		t.Errorf("Status = %s, want dry_run", migrator.Log.Operations[0].Status)
	}
}

func TestMigratorActualMove(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}
	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	content := []byte("test content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(destDir, "pics", "Test_abc123.jpg")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("Dest file should exist: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("Source file should be removed")
	}

	movedContent, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("Failed to read moved file: %v", err)
	}
	if string(movedContent) != string(content) {
		t.Error("Content should match")
	}
}

func TestMigratorOrphaned(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}
	testFile := filepath.Join(sourceDir, "Orphaned_xyz789.jpg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(testFile); err != nil {
		t.Error("Orphaned file should remain")
	}

	if migrator.Log.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", migrator.Log.SkippedCount)
	}
}

func TestMigratorUserRouting(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}
	testFile := filepath.Join(sourceDir, "User_1r0z7xp.jpeg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"1r0z7xp": {PostID: "1r0z7xp", Subreddit: "u_example_user", Username: "example_user", IsUserPost: true},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(destDir, "users", "example_user", "User_1r0z7xp.jpeg")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("Should be in users/{username}/: %v", err)
	}
}

func TestRollback(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "migration_log.json")
	destDir := filepath.Join(tmpDir, "dest")
	originalDir := filepath.Join(tmpDir, "original")

	if err := os.MkdirAll(filepath.Join(destDir, "pics"), 0755); err != nil {
		t.Fatalf("Failed to create dest directory: %v", err)
	}
	if err := os.MkdirAll(originalDir, 0755); err != nil {
		t.Fatalf("Failed to create original directory: %v", err)
	}

	destFile := filepath.Join(destDir, "pics", "Test_abc123.jpg")
	originalFile := filepath.Join(originalDir, "Test_abc123.jpg")
	if err := os.WriteFile(destFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to write dest file: %v", err)
	}

	log := MigrationLog{
		Version:   "1.0",
		SourceDir: originalDir,
		DestDir:   destDir,
		Operations: []MigrationRecord{
			{
				PostID:     "abc123",
				SourcePath: originalFile,
				DestPath:   destFile,
				Status:     "moved",
			},
		},
	}

	logData, err := json.Marshal(log)
	if err != nil {
		t.Fatalf("Failed to marshal log: %v", err)
	}
	if err := os.WriteFile(logPath, logData, 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	rb := NewRollback(logPath)
	rollbackLog, err := rb.Execute()
	if err != nil {
		t.Fatal(err)
	}

	if rollbackLog.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", rollbackLog.SuccessCount)
	}

	if _, err := os.Stat(originalFile); err != nil {
		t.Errorf("Original file should be restored: %v", err)
	}

	if _, err := os.Stat(destFile); !os.IsNotExist(err) {
		t.Error("Dest file should be removed")
	}
}

func TestRollbackMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "migration_log.json")

	log := MigrationLog{
		Version: "1.0",
		Operations: []MigrationRecord{
			{
				PostID:     "abc123",
				SourcePath: "/nonexistent/source.jpg",
				DestPath:   "/nonexistent/dest.jpg",
				Status:     "moved",
			},
		},
	}

	logData, _ := json.Marshal(log)
	if err := os.WriteFile(logPath, logData, 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	rb := NewRollback(logPath)
	rollbackLog, err := rb.Execute()
	if err != nil {
		t.Fatal(err)
	}

	if rollbackLog.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", rollbackLog.ErrorCount)
	}
}

func setupDuplicateScenario(t *testing.T, content []byte) (sourceDir, destDir, file1, file2 string, postMap map[string]PostInfo) {
	tmpDir := t.TempDir()
	sourceDir = filepath.Join(tmpDir, "source")
	destDir = filepath.Join(tmpDir, "dest")

	require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

	file1 = filepath.Join(sourceDir, "Post1_abc123.jpg")
	file2 = filepath.Join(sourceDir, "Post2_def456.jpg")

	require.NoError(t, os.WriteFile(file1, content, 0644), "Failed to write file1")
	require.NoError(t, os.WriteFile(file2, content, 0644), "Failed to write file2")

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(file1, baseTime, baseTime), "Failed to set file1 time")
	require.NoError(t, os.Chtimes(file2, baseTime.Add(time.Second), baseTime.Add(time.Second)), "Failed to set file2 time")

	postMap = map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user1", IsUserPost: false},
		"def456": {PostID: "def456", Subreddit: "pics", Username: "user2", IsUserPost: false},
	}

	return
}

func TestDuplicateHandling(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{
			name:    "identical_content",
			content: []byte("identical content"),
		},
		{
			name:    "same_content_in_both_files",
			content: []byte("same content in both files"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceDir, destDir, _, file2, postMap := setupDuplicateScenario(t, tt.content)

			migrator := NewMigrator(sourceDir, destDir, postMap, false)
			require.NoError(t, migrator.Execute(), "migrator.Execute failed")

			destFile1 := filepath.Join(destDir, "pics", "Post1_abc123.jpg")
			_, err := os.Stat(destFile1)
			require.NoError(t, err, "First file should be moved")

			destFile2 := filepath.Join(destDir, "pics", "Post2_def456.jpg")
			_, err = os.Stat(destFile2)
			assert.True(t, os.IsNotExist(err), "Duplicate file should not be moved")

			_, err = os.Stat(file2)
			require.NoError(t, err, "Duplicate source file should remain")

			assert.Equal(t, 1, migrator.Log.MovedCount, "Expected 1 moved file")
			assert.Equal(t, 1, migrator.Log.SkippedCount, "Expected 1 skipped file")

			foundDuplicateSkip := false
			for _, op := range migrator.Log.Operations {
				if op.Status == "skipped" && op.Error != "" && op.Error != "no matching POSTID in index.html" {
					foundDuplicateSkip = true
					break
				}
			}
			assert.True(t, foundDuplicateSkip, "Should have logged duplicate hash skip")
		})
	}
}

func TestIdempotentReRun(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")
	logPath := filepath.Join(tmpDir, "migration_log.json")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	// First run
	migrator1 := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator1.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := migrator1.SaveLog(logPath); err != nil {
		t.Fatalf("Failed to save log: %v", err)
	}

	// Second run with existing log - source file is gone, so it should skip
	migrator2 := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator2.LoadExistingLog(logPath); err != nil {
		t.Fatalf("Failed to load existing log: %v", err)
	}
	if err := migrator2.Execute(); err != nil {
		t.Fatal(err)
	}

	// Second run should have no operations since source file is gone
	if migrator2.Log.TotalFiles != 0 {
		t.Errorf("Second run should have no files to process, TotalFiles = %d", migrator2.Log.TotalFiles)
	}
}

func TestIdempotentReRunWithDuplicateSource(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")
	logPath := filepath.Join(tmpDir, "migration_log.json")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	// Create two identical files
	content := []byte("identical content")
	file1 := filepath.Join(sourceDir, "Post1_abc123.jpg")
	file2 := filepath.Join(sourceDir, "Post2_def456.jpg")

	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatalf("Failed to write file1: %v", err)
	}
	if err := os.WriteFile(file2, content, 0644); err != nil {
		t.Fatalf("Failed to write file2: %v", err)
	}

	// Set deterministic modification times
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file1, baseTime, baseTime); err != nil {
		t.Fatalf("Failed to set file1 time: %v", err)
	}
	if err := os.Chtimes(file2, baseTime.Add(time.Second), baseTime.Add(time.Second)); err != nil {
		t.Fatalf("Failed to set file2 time: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user1", IsUserPost: false},
		"def456": {PostID: "def456", Subreddit: "pics", Username: "user2", IsUserPost: false},
	}

	// First run - should move file1, skip file2 as duplicate
	migrator1 := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator1.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := migrator1.SaveLog(logPath); err != nil {
		t.Fatalf("Failed to save log: %v", err)
	}

	// Verify first run results
	destFile1 := filepath.Join(destDir, "pics", "Post1_abc123.jpg")
	if _, err := os.Stat(destFile1); err != nil {
		t.Fatalf("First file should be moved: %v", err)
	}

	// file2 should still exist as duplicate
	if _, err := os.Stat(file2); err != nil {
		t.Errorf("Duplicate source file should remain: %v", err)
	}

	// Second run - should skip file2 because hash is already in log
	migrator2 := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator2.LoadExistingLog(logPath); err != nil {
		t.Fatalf("Failed to load existing log: %v", err)
	}
	if err := migrator2.Execute(); err != nil {
		t.Fatal(err)
	}

	// Second run should skip file2 as duplicate (hash already in log)
	if migrator2.Log.SkippedCount != 1 {
		t.Errorf("Second run should skip duplicate, SkippedCount = %d", migrator2.Log.SkippedCount)
	}

	// Verify skipped reason mentions duplicate
	foundDuplicate := false
	for _, op := range migrator2.Log.Operations {
		if op.Status == "skipped" && op.Error != "" && op.Error != "no matching POSTID in index.html" {
			foundDuplicate = true
			break
		}
	}
	if !foundDuplicate {
		t.Error("Second run should log duplicate skip")
	}
}

func TestMigration_SortsByModTime(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	// Create files with different modification times
	// Oldest file first
	fileOldest := filepath.Join(sourceDir, "Oldest_abc123.jpg")
	fileMiddle := filepath.Join(sourceDir, "Middle_def456.jpg")
	fileNewest := filepath.Join(sourceDir, "Newest_ghi789.jpg")

	// Write in order with small delays to ensure different mod times
	if err := os.WriteFile(fileOldest, []byte("oldest content"), 0644); err != nil {
		t.Fatalf("Failed to write fileOldest: %v", err)
	}

	if err := os.WriteFile(fileMiddle, []byte("middle content"), 0644); err != nil {
		t.Fatalf("Failed to write fileMiddle: %v", err)
	}

	if err := os.WriteFile(fileNewest, []byte("newest content"), 0644); err != nil {
		t.Fatalf("Failed to write fileNewest: %v", err)
	}

	// Set deterministic modification times
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(fileOldest, baseTime, baseTime); err != nil {
		t.Fatalf("Failed to set fileOldest time: %v", err)
	}
	if err := os.Chtimes(fileMiddle, baseTime.Add(time.Second), baseTime.Add(time.Second)); err != nil {
		t.Fatalf("Failed to set fileMiddle time: %v", err)
	}
	if err := os.Chtimes(fileNewest, baseTime.Add(2*time.Second), baseTime.Add(2*time.Second)); err != nil {
		t.Fatalf("Failed to set fileNewest time: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user1", IsUserPost: false},
		"def456": {PostID: "def456", Subreddit: "pics", Username: "user2", IsUserPost: false},
		"ghi789": {PostID: "ghi789", Subreddit: "pics", Username: "user3", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	// All files should be moved
	if migrator.Log.MovedCount != 3 {
		t.Errorf("Expected 3 moved files, got %d", migrator.Log.MovedCount)
	}

	// Verify all destination files exist
	for _, postID := range []string{"abc123", "def456", "ghi789"} {
		destFile := filepath.Join(destDir, "pics", fmt.Sprintf("%s_%s.jpg", map[string]string{
			"abc123": "Oldest",
			"def456": "Middle",
			"ghi789": "Newest",
		}[postID], postID))
		if _, err := os.Stat(destFile); err != nil {
			t.Errorf("Dest file should exist for %s: %v", postID, err)
		}
	}

	// Verify operations are in order of mod time (oldest first)
	var opTimestamps []time.Time
	for _, op := range migrator.Log.Operations {
		if op.Status == "moved" {
			opTimestamps = append(opTimestamps, op.Timestamp)
		}
	}

	// Operations should be in chronological order (oldest file processed first)
	for i := 1; i < len(opTimestamps); i++ {
		if opTimestamps[i].Before(opTimestamps[i-1]) {
			t.Error("Operations should be in order of file mod time (oldest first)")
		}
	}
}

func TestMigration_HashLogging(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	content := []byte("content for hashing")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	// Verify hash was recorded in log
	if len(migrator.Log.Operations) != 1 {
		t.Fatalf("Expected 1 operation, got %d", len(migrator.Log.Operations))
	}

	op := migrator.Log.Operations[0]
	if op.Status != "moved" {
		t.Errorf("Expected status 'moved', got '%s'", op.Status)
	}

	if op.Hash == "" {
		t.Error("Hash should be recorded in migration log")
	}

	// Verify hash is valid hex (64 characters for BLAKE3-256)
	if len(op.Hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(op.Hash))
	}

	for _, c := range op.Hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			t.Errorf("Hash contains invalid character: %c", c)
		}
	}
}
