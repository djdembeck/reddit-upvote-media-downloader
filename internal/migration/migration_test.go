package migration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertHasDuplicateSkip(t *testing.T, operations []MigrationRecord) {
	t.Helper()
	for _, op := range operations {
		if op.Status == "skipped" &&
			strings.Contains(op.Error, "duplicate") &&
			strings.Contains(op.Error, "hash") {
			return
		}
	}
	t.Error("Expected to find duplicate skip in operations, but none was found")
}

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
		{"1lgwfyg.jpg", "1lgwfyg", false},
		{"188x0kd.png", "188x0kd", false},
		{"1r0z7xp.jpeg", "1r0z7xp", false},
		{"1r4m3qf.mp4", "1r4m3qf", false},
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
	if err := parser.ParseIndexHTML(context.Background(), indexPath); err != nil {
		t.Fatal(err)
	}

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

	migrator := NewMigrator(sourceDir, destDir, postMap, true, nil)
	if err := migrator.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(testFile); err != nil {
		t.Error("Source file should exist in dry-run")
	}

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

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	if err := migrator.Execute(context.Background()); err != nil {
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

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	if err := migrator.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(destDir, "unknown", "Orphaned_xyz789.jpg")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("Orphaned file should be moved to dest/unknown/: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("Source file should be removed")
	}

	if migrator.Log.MovedCount != 1 {
		t.Errorf("MovedCount = %d, want 1", migrator.Log.MovedCount)
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

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	if err := migrator.Execute(context.Background()); err != nil {
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

	rb := NewRollback(logPath, nil)
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

	rb := NewRollback(logPath, nil)
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

			migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
			require.NoError(t, migrator.Execute(context.Background()), "migrator.Execute failed")

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

	migrator1 := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator1.Execute(context.Background()))
	require.NoError(t, migrator1.SaveLog(context.Background(), logPath), "Failed to save log")

	migrator2 := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator2.LoadExistingLog(context.Background(), logPath), "Failed to load existing log")
	require.NoError(t, migrator2.Execute(context.Background()))

	assert.Equal(t, 0, migrator2.Log.TotalFiles, "Second run should have no files to process")
}

func TestIdempotentReRunWithDuplicateSource(t *testing.T) {
	content := []byte("identical content")
	sourceDir, destDir, _, file2, postMap := setupDuplicateScenario(t, content)
	logPath := filepath.Join(filepath.Dir(sourceDir), "migration_log.json")

	migrator1 := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator1.Execute(context.Background()))
	require.NoError(t, migrator1.SaveLog(context.Background(), logPath), "Failed to save log")

	destFile1 := filepath.Join(destDir, "pics", "Post1_abc123.jpg")
	_, err := os.Stat(destFile1)
	assert.NoError(t, err, "First file should be moved")

	_, err = os.Stat(file2)
	assert.NoError(t, err, "Duplicate source file should remain")

	migrator2 := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator2.LoadExistingLog(context.Background(), logPath), "Failed to load existing log")
	require.NoError(t, migrator2.Execute(context.Background()))

	assert.Equal(t, 1, migrator2.Log.SkippedCount, "Second run should skip duplicate")

	assertHasDuplicateSkip(t, migrator2.Log.Operations)
}

func TestMigration_SortsByModTime(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

	// PostIDs chosen so they are NOT alphabetically ordered with mod-times
	fileOldest := filepath.Join(sourceDir, "Oldest_zzzzzz.jpg")
	fileMiddle := filepath.Join(sourceDir, "Middle_aaaaaa.jpg")
	fileNewest := filepath.Join(sourceDir, "Newest_mmmmmm.jpg")

	require.NoError(t, os.WriteFile(fileOldest, []byte("oldest content"), 0644), "Failed to write fileOldest")
	require.NoError(t, os.WriteFile(fileMiddle, []byte("middle content"), 0644), "Failed to write fileMiddle")
	require.NoError(t, os.WriteFile(fileNewest, []byte("newest content"), 0644), "Failed to write fileNewest")

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(fileOldest, baseTime, baseTime), "Failed to set fileOldest time")
	require.NoError(t, os.Chtimes(fileMiddle, baseTime.Add(time.Second), baseTime.Add(time.Second)), "Failed to set fileMiddle time")
	require.NoError(t, os.Chtimes(fileNewest, baseTime.Add(2*time.Second), baseTime.Add(2*time.Second)), "Failed to set fileNewest time")

	postMap := map[string]PostInfo{
		"zzzzzz": {PostID: "zzzzzz", Subreddit: "pics", Username: "user1", IsUserPost: false},
		"aaaaaa": {PostID: "aaaaaa", Subreddit: "pics", Username: "user2", IsUserPost: false},
		"mmmmmm": {PostID: "mmmmmm", Subreddit: "pics", Username: "user3", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator.Execute(context.Background()), "migrator.Execute failed")

	require.Equal(t, 3, migrator.Log.MovedCount, "Expected 3 moved files")

	for _, postID := range []string{"zzzzzz", "aaaaaa", "mmmmmm"} {
		destFile := filepath.Join(destDir, "pics", fmt.Sprintf("%s_%s.jpg", map[string]string{
			"zzzzzz": "Oldest",
			"aaaaaa": "Middle",
			"mmmmmm": "Newest",
		}[postID], postID))
		_, err := os.Stat(destFile)
		assert.NoError(t, err, "Dest file should exist for %s", postID)
	}

	var opPostIDs []string
	for _, op := range migrator.Log.Operations {
		if op.Status == "moved" || op.Status == "moved_with_warning" {
			opPostIDs = append(opPostIDs, op.PostID)
		}
	}

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

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	require.NoError(t, migrator.Execute(context.Background()), "migrator.Execute failed")

	require.Len(t, migrator.Log.Operations, 1, "Expected 1 operation")

	op := migrator.Log.Operations[0]
	assert.Equal(t, "moved", op.Status, "Expected status 'moved'")
	assert.NotEmpty(t, op.Hash, "Hash should be recorded in migration log")
	assert.Len(t, op.Hash, 64, "Expected hash length 64")

	decoded, err := hex.DecodeString(op.Hash)
	require.NoError(t, err, "Hash should be valid hex string")
	assert.Len(t, decoded, 32, "BLAKE3-256 should produce 32 bytes")
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		content := files[name]
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file %s: %v", name, err)
		}
	}
}

func assertPostMapContains(t *testing.T, parser *HTMLParser, expectedIDs []string) {
	t.Helper()
	for _, id := range expectedIDs {
		if _, ok := parser.PostMap[id]; !ok {
			t.Errorf("Missing post %s in PostMap", id)
		}
	}
}

func TestParseHTMLFiles_TableDriven(t *testing.T) {
	validHTML := `<html><body>
<div class="post">
    <span class="subreddit">r/TestSub</span>
    <span class="user">u/testuser</span>
</div>
</body></html>`

	tests := []struct {
		name            string
		files           map[string]string
		expectedLen     int
		expectedPostIDs []string
	}{
		{
			name: "valid_files_only",
			files: map[string]string{
				"post1.html": validHTML,
				"post2.html": validHTML,
				"post3.html": validHTML,
			},
			expectedLen:     3,
			expectedPostIDs: []string{"post1", "post2", "post3"},
		},
		{
			name: "with_corrupted_file",
			files: map[string]string{
				"valid1.html":    validHTML,
				"valid2.html":    validHTML,
				"corrupted.html": "invalid html content",
			},
			expectedLen:     3,
			expectedPostIDs: []string{"valid1", "valid2", "corrupted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeFiles(t, tmpDir, tt.files)

			parser := NewHTMLParser()
			if err := parser.ParseHTMLFiles(context.Background(), tmpDir); err != nil {
				t.Fatalf("ParseHTMLFiles failed: %v", err)
			}

			if len(parser.PostMap) != tt.expectedLen {
				t.Errorf("Expected %d posts in PostMap, got %d", tt.expectedLen, len(parser.PostMap))
			}

			assertPostMapContains(t, parser, tt.expectedPostIDs)
		})
	}
}

func TestParseHTMLFile(t *testing.T) {
	tests := []struct {
		name           string
		htmlContent    string
		filename       string
		wantPostID     string
		wantSubreddit  string
		wantUsername   string
		wantIsUserPost bool
	}{
		{
			name: "regular_post",
			htmlContent: `<html><body>
<div class="post">
    <span class="subreddit">r/TestSub</span>
    <span class="user">u/testuser</span>
</div>
</body></html>`,
			filename:       "1r4wjj5.html",
			wantPostID:     "1r4wjj5",
			wantSubreddit:  "TestSub",
			wantUsername:   "testuser",
			wantIsUserPost: false,
		},
		{
			name: "user_post",
			htmlContent: `<html><body>
<div class="post">
    <span class="subreddit">r/u_exampleuser</span>
    <span class="user">u/exampleuser</span>
</div>
</body></html>`,
			filename:       "1r0z7xp.html",
			wantPostID:     "1r0z7xp",
			wantSubreddit:  "u_exampleuser",
			wantUsername:   "exampleuser",
			wantIsUserPost: true,
		},
		{
			name: "missing_username",
			htmlContent: `<html><body>
<div class="post">
    <span class="subreddit">r/TestSub</span>
</div>
</body></html>`,
			filename:       "1abc123.html",
			wantPostID:     "1abc123",
			wantSubreddit:  "TestSub",
			wantUsername:   "",
			wantIsUserPost: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, tt.filename)

			if err := os.WriteFile(filePath, []byte(tt.htmlContent), 0644); err != nil {
				t.Fatalf("Failed to write HTML file: %v", err)
			}

			parser := NewHTMLParser()
			postInfo, err := parser.ParseHTMLFile(context.Background(), filePath)

			if err != nil {
				t.Fatalf("ParseHTMLFile failed: %v", err)
			}

			if postInfo.PostID != tt.wantPostID {
				t.Errorf("PostID = %s, want %s", postInfo.PostID, tt.wantPostID)
			}
			if postInfo.Subreddit != tt.wantSubreddit {
				t.Errorf("Subreddit = %s, want %s", postInfo.Subreddit, tt.wantSubreddit)
			}
			if postInfo.Username != tt.wantUsername {
				t.Errorf("Username = %s, want %s", postInfo.Username, tt.wantUsername)
			}
			if postInfo.IsUserPost != tt.wantIsUserPost {
				t.Errorf("IsUserPost = %v, want %v", postInfo.IsUserPost, tt.wantIsUserPost)
			}
		})
	}
}

func TestMigratorUnknownFiles(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "source")
	destDir := filepath.Join(tmpDir, "dest")

	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	testFile := filepath.Join(sourceDir, "Test_missing123.jpg")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	postMap := map[string]PostInfo{
		"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false, nil)
	if err := migrator.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(destDir, "unknown", "Test_missing123.jpg")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("File should be moved to dest/unknown/: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("Source file should be removed")
	}

	if len(migrator.Log.Operations) != 1 {
		t.Fatalf("Expected 1 operation, got %d", len(migrator.Log.Operations))
	}

	op := migrator.Log.Operations[0]
	if op.Status != "moved" {
		t.Errorf("Status = %s, want moved", op.Status)
	}
	if op.Subreddit != "unknown" {
		t.Errorf("Subreddit = %s, want unknown", op.Subreddit)
	}
}

func TestMigrationSuite(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name                    string
		files                   map[string]string
		postMap                 map[string]PostInfo
		prePopulateDB           func(t *testing.T, db *storage.DB, sourceDir string)
		wantMoved               int
		wantSkipped             int
		wantDestFiles           []string
		wantSourceRemoved       []string
		wantSourceRemain        []string
		wantDBPostIDs           []string
		wantDBCheck             func(t *testing.T, db *storage.DB)
		runRollback             bool
		wantRollbackSuccess     int
		wantRollbackError       int
		wantPostRollbackDBCheck func(t *testing.T, db *storage.DB)
		checkLog                func(t *testing.T, migrator *Migrator)
	}

	tests := []testCase{
		{
			name: "basic_migration_with_db",
			files: map[string]string{
				"Test_abc123.jpg": "test content for hashing",
			},
			postMap: map[string]PostInfo{
				"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
			},
			wantMoved:         1,
			wantSkipped:       0,
			wantDestFiles:     []string{"pics/Test_abc123.jpg"},
			wantSourceRemoved: []string{"Test_abc123.jpg"},
			wantDBPostIDs:     []string{"abc123"},
			wantDBCheck: func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				post, err := db.GetPost(ctx, "abc123")
				require.NoError(t, err)
				require.NotNil(t, post)
				assert.Equal(t, "527c58ca1bc0f681d553bef8b6c88bf5c4486829a524272c9a6026ba0658d739", post.Hash)
				assert.Equal(t, "Migrated from bdfr-html", post.Title)
				assert.Equal(t, "pics", post.Subreddit)
				assert.Equal(t, "user", post.Author)
				assert.Equal(t, "migrated", post.Source)
				assert.Equal(t, "image", post.MediaType)
			},
		},
		{
			name: "hash_exists_in_db_skips_file",
			files: map[string]string{
				"Test_abc123.jpg": "duplicate content",
			},
			postMap: map[string]PostInfo{
				"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
			},
			prePopulateDB: func(t *testing.T, db *storage.DB, sourceDir string) {
				testFile := filepath.Join(sourceDir, "Test_abc123.jpg")
				hash, err := calculateHash(testFile)
				require.NoError(t, err)
				ctx := context.Background()
				existingPost := &storage.Post{
					ID:           "existing123",
					Title:        "Existing Post",
					Subreddit:    "existing",
					Author:       "existing_user",
					DownloadedAt: time.Now(),
					Source:       "migrated",
					Hash:         hash,
				}
				require.NoError(t, db.SavePost(ctx, existingPost))
			},
			wantMoved:        0,
			wantSkipped:      1,
			wantDestFiles:    nil,
			wantSourceRemain: []string{"Test_abc123.jpg"},
			checkLog: func(t *testing.T, migrator *Migrator) {
				assert.Equal(t, 1, migrator.Log.SkippedCount)
				assert.Equal(t, 0, migrator.Log.MovedCount)
				require.Len(t, migrator.Log.Operations, 1)
				assert.Equal(t, "skipped", migrator.Log.Operations[0].Status)
				assert.Contains(t, migrator.Log.Operations[0].Error, "duplicate hash")
			},
		},
		{
			name: "skip_duplicate_files_same_migration",
			files: map[string]string{
				"File1_abc123.jpg": "identical duplicate content",
				"File2_def456.jpg": "identical duplicate content",
			},
			postMap: map[string]PostInfo{
				"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user1", IsUserPost: false},
				"def456": {PostID: "def456", Subreddit: "pics", Username: "user2", IsUserPost: false},
			},
			wantMoved:         1,
			wantSkipped:       1,
			wantDestFiles:     []string{"pics/File1_abc123.jpg"},
			wantSourceRemoved: []string{"File1_abc123.jpg"},
			wantSourceRemain:  []string{"File2_def456.jpg"},
			wantDBPostIDs:     []string{"abc123"},
			checkLog: func(t *testing.T, migrator *Migrator) {
				assert.Equal(t, 1, migrator.Log.MovedCount)
				assert.Equal(t, 1, migrator.Log.SkippedCount)
				assertHasDuplicateSkip(t, migrator.Log.Operations)
			},
		},
		{
			name: "placeholder_values_in_db",
			files: map[string]string{
				"Test_abc123.jpg": "test content",
			},
			postMap: map[string]PostInfo{
				"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
			},
			wantMoved:         1,
			wantSkipped:       0,
			wantDestFiles:     []string{"pics/Test_abc123.jpg"},
			wantSourceRemoved: []string{"Test_abc123.jpg"},
			wantDBPostIDs:     []string{"abc123"},
			wantDBCheck: func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				post, err := db.GetPost(ctx, "abc123")
				require.NoError(t, err)
				require.NotNil(t, post)
				assert.Equal(t, "Migrated from bdfr-html", post.Title)
				assert.Equal(t, "pics", post.Subreddit)
				assert.Equal(t, "user", post.Author)
				assert.Equal(t, "migrated", post.Source)
				assert.Equal(t, "abc123", post.ID)
				assert.Equal(t, "image", post.MediaType)
				assert.NotEmpty(t, post.FilePath)
				assert.NotEmpty(t, post.Hash)
				assert.False(t, post.DownloadedAt.IsZero())
			},
		},
		{
			name: "rollback_removes_db_entry",
			files: map[string]string{
				"Test_abc123.jpg": "test content",
			},
			postMap: map[string]PostInfo{
				"abc123": {PostID: "abc123", Subreddit: "pics", Username: "user", IsUserPost: false},
			},
			wantMoved:           1,
			wantSkipped:         0,
			wantDestFiles:       []string{"pics/Test_abc123.jpg"},
			wantSourceRemoved:   []string{"Test_abc123.jpg"},
			wantDBPostIDs:       []string{"abc123"},
			runRollback:         true,
			wantRollbackSuccess: 1,
			wantRollbackError:   0,
			wantPostRollbackDBCheck: func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				post, err := db.GetPost(ctx, "abc123")
				require.NoError(t, err)
				assert.Nil(t, post)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			sourceDir := filepath.Join(tmpDir, "source")
			destDir := filepath.Join(tmpDir, "dest")
			dbPath := filepath.Join(tmpDir, "test.db")
			logPath := filepath.Join(tmpDir, "migration_log.json")

			require.NoError(t, os.MkdirAll(sourceDir, 0755), "Failed to create source directory")

			keys := make([]string, 0, len(tt.files))
			for filename := range tt.files {
				keys = append(keys, filename)
			}
			sort.Strings(keys)

			for _, filename := range keys {
				content := tt.files[filename]
				path := filepath.Join(sourceDir, filename)
				require.NoError(t, os.WriteFile(path, []byte(content), 0644), "Failed to write %s", filename)
			}

			db, err := storage.NewDB(dbPath)
			require.NoError(t, err, "Failed to create database")
			defer db.Close()

			if tt.prePopulateDB != nil {
				tt.prePopulateDB(t, db, sourceDir)
			}

			migrator := NewMigrator(sourceDir, destDir, tt.postMap, false, db)
			require.NoError(t, migrator.Execute(context.Background()), "migrator.Execute failed")

			assert.Equal(t, tt.wantMoved, migrator.Log.MovedCount, "Moved count mismatch")
			assert.Equal(t, tt.wantSkipped, migrator.Log.SkippedCount, "Skipped count mismatch")

			for _, relPath := range tt.wantDestFiles {
				destFile := filepath.Join(destDir, relPath)
				_, err := os.Stat(destFile)
				assert.NoError(t, err, "Dest file should exist: %s", relPath)
			}

			for _, filename := range tt.wantSourceRemoved {
				srcFile := filepath.Join(sourceDir, filename)
				_, err := os.Stat(srcFile)
				assert.True(t, os.IsNotExist(err), "Source file should be removed: %s", filename)
			}

			for _, filename := range tt.wantSourceRemain {
				srcFile := filepath.Join(sourceDir, filename)
				_, err := os.Stat(srcFile)
				assert.NoError(t, err, "Source file should remain: %s", filename)
			}

			ctx := context.Background()
			for _, postID := range tt.wantDBPostIDs {
				post, err := db.GetPost(ctx, postID)
				require.NoError(t, err, "Failed to get post %s from DB", postID)
				require.NotNil(t, post, "Post %s should exist in DB", postID)
			}

			if tt.wantDBCheck != nil {
				tt.wantDBCheck(t, db)
			}

			if tt.checkLog != nil {
				tt.checkLog(t, migrator)
			}

			if tt.runRollback {
				require.NoError(t, migrator.SaveLog(context.Background(), logPath), "Failed to save log")

				rb := NewRollback(logPath, db)
				rollbackLog, err := rb.Execute()
				require.NoError(t, err, "Rollback failed")

				assert.Equal(t, tt.wantRollbackSuccess, rollbackLog.SuccessCount, "Rollback success count mismatch")
				assert.Equal(t, tt.wantRollbackError, rollbackLog.ErrorCount, "Rollback error count mismatch")

				for _, filename := range tt.wantSourceRemoved {
					srcFile := filepath.Join(sourceDir, filename)
					_, err := os.Stat(srcFile)
					assert.NoError(t, err, "Source file should be restored: %s", filename)
				}

				for _, relPath := range tt.wantDestFiles {
					destFile := filepath.Join(destDir, relPath)
					_, err := os.Stat(destFile)
					assert.True(t, os.IsNotExist(err), "Dest file should be removed: %s", relPath)
				}

				if tt.wantPostRollbackDBCheck != nil {
					tt.wantPostRollbackDBCheck(t, db)
				}
			}
		})
	}
}
