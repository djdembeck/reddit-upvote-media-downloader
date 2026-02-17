package migration

import (
	"encoding/hex"
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

// assertHasDuplicateSkip is a test helper that asserts operations contain a duplicate skip.
func assertHasDuplicateSkip(t *testing.T, operations []MigrationRecord) {
	t.Helper()
	for _, op := range operations {
		if op.Status == "skipped" && op.Error != "" && op.Error != "no matching POSTID in index.html" {
			return
		}
	}
	t.Error("Expected to find duplicate skip in operations, but none was found")
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
			name:    "empty_content",
			content: []byte{},
		},
		{
			name:    "large_content",
			content: make([]byte, 1024*100),
		},
		{
			name:    "different_extension",
			content: []byte("content with extension"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceDir, destDir, file1, file2, postMap := setupDuplicateScenario(t, tt.content)

			if tt.name == "different_extension" {
				newFile1 := filepath.Join(filepath.Dir(file1), "Post1_abc123.png")
				newFile2 := filepath.Join(filepath.Dir(file2), "Post2_def456.png")

				require.NoError(t, os.Rename(file1, newFile1), "Failed to rename file1")
				require.NoError(t, os.Rename(file2, newFile2), "Failed to rename file2")

				file1 = newFile1
				file2 = newFile2
			}

			migrator := NewMigrator(sourceDir, destDir, postMap, false)
			require.NoError(t, migrator.Execute(), "migrator.Execute failed")

			ext := ".jpg"
			if tt.name == "different_extension" {
				ext = ".png"
			}
			destFile1 := filepath.Join(destDir, "pics", "Post1_abc123"+ext)
			_, err := os.Stat(destFile1)
			require.NoError(t, err, "First file should be moved")

			destFile2 := filepath.Join(destDir, "pics", "Post2_def456"+ext)
			_, err = os.Stat(destFile2)
			assert.True(t, os.IsNotExist(err), "Duplicate file should not be moved")

			_, err = os.Stat(file2)
			require.NoError(t, err, "Duplicate source file should remain")

			assert.Equal(t, 1, migrator.Log.MovedCount, "Expected 1 moved file")
			assert.Equal(t, 1, migrator.Log.SkippedCount, "Expected 1 skipped file")

			assertHasDuplicateSkip(t, migrator.Log.Operations)
		})
	}
}

func TestIdempotentReRun(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")
	logPath := filepath.Join(tmpDir, "migration_log.json")

	require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644), "Failed to write test file")

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	// First run
	migrator1 := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator1.Execute())
	require.NoError(t, migrator1.SaveLog(logPath), "Failed to save log")

	// Second run with existing log - source file is gone, so it should skip
	migrator2 := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator2.LoadExistingLog(logPath), "Failed to load existing log")
	require.NoError(t, migrator2.Execute())

	// Second run should have no operations since source file is gone
	assert.Equal(t, 0, migrator2.Log.TotalFiles, "Second run should have no files to process")
}

func TestIdempotentReRunWithDuplicateSource(t *testing.T) {
	content := []byte("identical content")
	sourceDir, destDir, _, file2, postMap := setupDuplicateScenario(t, content)
	logPath := filepath.Join(filepath.Dir(sourceDir), "migration_log.json")

	// First run - should move file1, skip file2 as duplicate
	migrator1 := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator1.Execute())
	require.NoError(t, migrator1.SaveLog(logPath), "Failed to save log")

	// Verify first run results
	destFile1 := filepath.Join(destDir, "pics", "Post1_abc123.jpg")
	_, err := os.Stat(destFile1)
	assert.NoError(t, err, "First file should be moved")

	// file2 should still exist as duplicate
	_, err = os.Stat(file2)
	assert.NoError(t, err, "Duplicate source file should remain")

	// Second run - should skip file2 because hash is already in log
	migrator2 := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator2.LoadExistingLog(logPath), "Failed to load existing log")
	require.NoError(t, migrator2.Execute())

	// Second run should skip file2 as duplicate (hash already in log)
	assert.Equal(t, 1, migrator2.Log.SkippedCount, "Second run should skip duplicate")

	assertHasDuplicateSkip(t, migrator2.Log.Operations)
}

func TestMigration_SortsByModTime(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

	// Create files with different modification times
	// PostIDs chosen so they are NOT alphabetically ordered with mod-times
	fileOldest := filepath.Join(sourceDir, "Oldest_zzzzzz.jpg")
	fileMiddle := filepath.Join(sourceDir, "Middle_aaaaaa.jpg")
	fileNewest := filepath.Join(sourceDir, "Newest_mmmmmm.jpg")

	// Write in order with small delays to ensure different mod times
	require.NoError(t, os.WriteFile(fileOldest, []byte("oldest content"), 0644), "Failed to write fileOldest")
	require.NoError(t, os.WriteFile(fileMiddle, []byte("middle content"), 0644), "Failed to write fileMiddle")
	require.NoError(t, os.WriteFile(fileNewest, []byte("newest content"), 0644), "Failed to write fileNewest")

	// Set deterministic modification times
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(fileOldest, baseTime, baseTime), "Failed to set fileOldest time")
	require.NoError(t, os.Chtimes(fileMiddle, baseTime.Add(time.Second), baseTime.Add(time.Second)), "Failed to set fileMiddle time")
	require.NoError(t, os.Chtimes(fileNewest, baseTime.Add(2*time.Second), baseTime.Add(2*time.Second)), "Failed to set fileNewest time")

	postMap := map[string]PostInfo{
		"zzzzzz": {PostID: "zzzzzz", Subreddit: "pics", Username: "user1", IsUserPost: false},
		"aaaaaa": {PostID: "aaaaaa", Subreddit: "pics", Username: "user2", IsUserPost: false},
		"mmmmmm": {PostID: "mmmmmm", Subreddit: "pics", Username: "user3", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator.Execute(), "migrator.Execute failed")

	// All files should be moved
	require.Equal(t, 3, migrator.Log.MovedCount, "Expected 3 moved files")

	// Verify all destination files exist
	for _, postID := range []string{"zzzzzz", "aaaaaa", "mmmmmm"} {
		destFile := filepath.Join(destDir, "pics", fmt.Sprintf("%s_%s.jpg", map[string]string{
			"zzzzzz": "Oldest",
			"aaaaaa": "Middle",
			"mmmmmm": "Newest",
		}[postID], postID))
		_, err := os.Stat(destFile)
		assert.NoError(t, err, "Dest file should exist for %s", postID)
	}

	// Verify operations are sorted by modification time (not PostID)
	var opPostIDs []string
	for _, op := range migrator.Log.Operations {
		if op.Status == "moved" {
			opPostIDs = append(opPostIDs, op.PostID)
		}
	}

	// Files should be processed in mod-time order: Oldest (zzzzzz), Middle (aaaaaa), Newest (mmmmmm)
	expectedOrder := []string{"zzzzzz", "aaaaaa", "mmmmmm"}
	assert.Equal(t, expectedOrder, opPostIDs, "Operations should process files by modification time")
}

func TestMigration_HashLogging(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

	testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
	content := []byte("content for hashing")
	require.NoError(t, os.WriteFile(testFile, content, 0644), "Failed to write test file")

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	require.NoError(t, migrator.Execute(), "migrator.Execute failed")

	// Verify hash was recorded in log
	require.Len(t, migrator.Log.Operations, 1, "Expected 1 operation")

	op := migrator.Log.Operations[0]
	assert.Equal(t, "moved", op.Status, "Expected status 'moved'")
	assert.NotEmpty(t, op.Hash, "Hash should be recorded in migration log")

	// Verify hash is valid hex (64 characters for BLAKE3-256)
	assert.Len(t, op.Hash, 64, "Expected hash length 64")

	decoded, err := hex.DecodeString(op.Hash)
	require.NoError(t, err, "Hash should be valid hex string")
	assert.Len(t, decoded, 32, "BLAKE3-256 should produce 32 bytes")
}
