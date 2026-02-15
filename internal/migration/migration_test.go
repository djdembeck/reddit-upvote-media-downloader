package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
		{"TeenBlow", "TeenBlow"},
		{"r/TeenBlow", "r_TeenBlow"},
		{"u_milakittenx", "u_milakittenx"},
		{"body perfection", "body_perfection"},
		{"deep-throat", "deep-throat"},
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
    <span class="subreddit">r/TeenBlow</span>
    <span class="user">u/angrytoban</span>
</div>
<div class=post>
    <a href="1r0z7xp.html"><h1>User Post</h1></a>
    <span class="subreddit">r/u_milakittenx</span>
    <span class="user">u/milakittenx</span>
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
		if info.Subreddit != "TeenBlow" {
			t.Errorf("Subreddit = %s, want TeenBlow", info.Subreddit)
		}
		if info.Username != "angrytoban" {
			t.Errorf("Username = %s, want angrytoban", info.Username)
		}
		if info.IsUserPost {
			t.Error("IsUserPost should be false")
		}
	}

	// User post
	if info, ok := parser.PostMap["1r0z7xp"]; !ok {
		t.Error("Missing user post")
	} else {
		if info.Subreddit != "u_milakittenx" {
			t.Errorf("Subreddit = %s, want u_milakittenx", info.Subreddit)
		}
		if info.Username != "milakittenx" {
			t.Errorf("Username = %s, want milakittenx", info.Username)
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
		"1r0z7xp": {PostID: "1r0z7xp", Subreddit: "u_milakittenx", Username: "milakittenx", IsUserPost: true},
	}

	migrator := NewMigrator(sourceDir, destDir, postMap, false)
	if err := migrator.Execute(); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(destDir, "users", "milakittenx", "User_1r0z7xp.jpeg")
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
