package migration

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Migrator struct {
	SourceDir string
	DestDir   string
	PostMap   map[string]PostInfo
	DryRun    bool
	Log       *MigrationLog
}

func NewMigrator(sourceDir, destDir string, postMap map[string]PostInfo, dryRun bool) *Migrator {
	return &Migrator{
		SourceDir: sourceDir,
		DestDir:   destDir,
		PostMap:   postMap,
		DryRun:    dryRun,
		Log: &MigrationLog{
			Version:    "1.0",
			Timestamp:  time.Now(),
			SourceDir:  sourceDir,
			DestDir:    destDir,
			Operations: []MigrationRecord{},
		},
	}
}

func (m *Migrator) Execute() error {
	entries, err := os.ReadDir(m.SourceDir)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m.processFile(entry.Name())
	}
	return nil
}

func (m *Migrator) processFile(filename string) {
	m.Log.TotalFiles++

	// Extract POSTID
	postID, err := ExtractPostID(filename)
	if err != nil {
		m.recordError(filename, "", "extract_postid", err)
		return
	}

	// Lookup in PostMap
	postInfo, exists := m.PostMap[postID]
	if !exists {
		m.recordSkipped(filename, postID, "no matching POSTID in index.html")
		return
	}

	// Build destination
	destPath := m.buildDestPath(filename, postInfo)
	sourcePath := filepath.Join(m.SourceDir, filename)

	// Check if destination exists
	if _, err := os.Stat(destPath); err == nil {
		m.recordSkipped(filename, postID, "destination already exists")
		return
	}

	// Get file size
	fileInfo, err := os.Stat(sourcePath)
	if err != nil {
		m.recordError(filename, postID, "stat_file", err)
		return
	}

	if m.DryRun {
		m.recordDryRun(filename, postID, destPath, postInfo, fileInfo.Size())
		return
	}

	// Move file
	if err := m.moveFile(sourcePath, destPath); err != nil {
		m.recordError(filename, postID, "move_file", err)
		return
	}

	m.recordSuccess(filename, postID, destPath, postInfo, fileInfo.Size())
}

func (m *Migrator) buildDestPath(filename string, info PostInfo) string {
	var subdir string
	if info.IsUserPost && info.Username != "" {
		subdir = filepath.Join("users", info.Username)
	} else if info.Subreddit != "" {
		subdir = SanitizePath(info.Subreddit)
	} else {
		subdir = "unknown"
	}
	return filepath.Join(m.DestDir, subdir, filename)
}

func (m *Migrator) moveFile(src, dst string) error {
	// Create directory
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Copy
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	// Verify
	srcInfo, err := os.Stat(src)
	if err != nil {
		os.Remove(dst)
		return fmt.Errorf("stat source file %s: %w", src, err)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		os.Remove(dst)
		return fmt.Errorf("stat destination file %s: %w", dst, err)
	}
	if srcInfo.Size() != dstInfo.Size() {
		os.Remove(dst)
		return fmt.Errorf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
	}

	// Delete source
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source %s: %w", src, err)
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}

func (m *Migrator) SaveLog(logPath string) error {
	file, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(m.Log)
}

// Recording methods
func (m *Migrator) recordSuccess(filename, postID, destPath string, info PostInfo, size int64) {
	m.Log.Operations = append(m.Log.Operations, MigrationRecord{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		DestPath:   destPath,
		Subreddit:  info.Subreddit,
		Username:   info.Username,
		IsUserPost: info.IsUserPost,
		Status:     "moved",
		Timestamp:  time.Now(),
		FileSize:   size,
	})
	m.Log.MovedCount++
}

func (m *Migrator) recordSkipped(filename, postID, reason string) {
	m.Log.Operations = append(m.Log.Operations, MigrationRecord{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		Status:     "skipped",
		Error:      reason,
		Timestamp:  time.Now(),
	})
	m.Log.SkippedCount++
}

func (m *Migrator) recordError(filename, postID, operation string, err error) {
	m.Log.Operations = append(m.Log.Operations, MigrationRecord{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		Status:     "error",
		Error:      fmt.Sprintf("%s: %v", operation, err),
		Timestamp:  time.Now(),
	})
	m.Log.ErrorCount++
}

func (m *Migrator) recordDryRun(filename, postID, destPath string, info PostInfo, size int64) {
	m.Log.Operations = append(m.Log.Operations, MigrationRecord{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		DestPath:   destPath,
		Subreddit:  info.Subreddit,
		Username:   info.Username,
		IsUserPost: info.IsUserPost,
		Status:     "dry_run",
		Timestamp:  time.Now(),
		FileSize:   size,
	})
}
