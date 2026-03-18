package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

// Rollback handles reverting file migrations.
type Rollback struct {
	LogPath    string
	DB         *storage.DB
	SourceRoot string
	DestRoot   string
}

// RollbackLog contains rollback operation results.
type RollbackLog struct {
	Timestamp    time.Time        `json:"timestamp"`
	OriginalLog  string           `json:"original_log"`
	SuccessCount int              `json:"success_count"`
	ErrorCount   int              `json:"error_count"`
	Operations   []RollbackRecord `json:"operations"`
}

// RollbackRecord represents a single rollback operation.
type RollbackRecord struct {
	PostID     string    `json:"post_id"`
	SourcePath string    `json:"source_path"`
	DestPath   string    `json:"dest_path"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

func NewRollback(logPath string, db *storage.DB, sourceRoot, destRoot string) *Rollback {
	return &Rollback{
		LogPath:    logPath,
		DB:         db,
		SourceRoot: sourceRoot,
		DestRoot:   destRoot,
	}
}

func (r *Rollback) loadLog() (*MigrationLog, error) {
	file, err := os.Open(r.LogPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("failed to close rollback log file", "path", r.LogPath, "error", cerr)
		}
	}()

	var migLog MigrationLog
	if err := json.NewDecoder(file).Decode(&migLog); err != nil {
		return nil, fmt.Errorf("decode log: %w", err)
	}
	return &migLog, nil
}

func (r *Rollback) Execute(ctx context.Context) (*RollbackLog, error) {
	migLog, err := r.loadLog()
	if err != nil {
		return nil, err
	}

	if r.SourceRoot == "" {
		return nil, fmt.Errorf("source root must be provided")
	}
	if r.DestRoot == "" {
		return nil, fmt.Errorf("dest root must be provided")
	}

	rollbackLog := &RollbackLog{
		Timestamp:   time.Now(),
		OriginalLog: r.LogPath,
		Operations:  []RollbackRecord{},
	}

	for i := len(migLog.Operations) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return rollbackLog, fmt.Errorf("rollback cancelled: %w", err)
		}

		op := migLog.Operations[i]
		if op.Status != "moved" && op.Status != "moved_with_warning" {
			continue
		}

		record := r.rollbackOperation(ctx, op)
		rollbackLog.Operations = append(rollbackLog.Operations, record)

		if record.Status == "success" {
			rollbackLog.SuccessCount++
		} else {
			rollbackLog.ErrorCount++
		}
	}

	return rollbackLog, nil
}

func (r *Rollback) rollbackOperation(ctx context.Context, op MigrationRecord) RollbackRecord {
	record := RollbackRecord{
		PostID:     op.PostID,
		SourcePath: op.DestPath,
		DestPath:   op.SourcePath,
		Timestamp:  time.Now(),
	}

	// Validate paths are within trusted roots - source path must be under SourceRoot, dest path under DestRoot
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("invalid source path: %v", err)
		return record
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("invalid dest path: %v", err)
		return record
	}

	// Check file exists
	if _, err := os.Stat(op.DestPath); os.IsNotExist(err) {
		record.Status = "error"
		record.Error = "file not found at destination"
		return record
	} else if err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("stat dest: %v", err)
		return record
	}

	// Ensure source dir exists
	sourceDir := filepath.Dir(op.SourcePath)
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("create dir: %v", err)
		return record
	}

	// Re-validate paths after MkdirAll to prevent TOCTOU symlink attacks
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("source path validation failed after mkdir: %v", err)
		return record
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("dest path validation failed after mkdir: %v", err)
		return record
	}

	// Check if source file already exists (would overwrite)
	if _, err := os.Stat(op.SourcePath); err == nil {
		record.Status = "error"
		record.Error = "source file exists, aborting rollback"
		return record
	} else if !os.IsNotExist(err) {
		record.Status = "error"
		record.Error = fmt.Sprintf("stat source: %v", err)
		return record
	}

	if err := copyFile(op.DestPath, op.SourcePath); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("copy file: %v", err)
		return record
	}

	srcInfo, err := os.Stat(op.DestPath)
	if err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("stat source file %s: %v", op.DestPath, err)
		return record
	}
	dstInfo, err := os.Stat(op.SourcePath)
	if err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("stat destination file %s: %v", op.SourcePath, err)
		return record
	}
	if srcInfo.Size() != dstInfo.Size() {
		cleanupErr := os.Remove(op.SourcePath)
		record.Status = "error"
		record.Error = fmt.Sprintf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
		if cleanupErr != nil {
			record.Error += fmt.Sprintf("; cleanup failed: %v", cleanupErr)
		}
		return record
	}

	if err := os.Remove(op.DestPath); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("remove dest: %v", err)
		return record
	}

	// Attempt to remove empty destination directory
	destDir := filepath.Dir(op.DestPath)
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) == 0 {
		if removeErr := os.Remove(destDir); removeErr != nil {
			slog.Warn("failed to remove empty destination directory",
				"dir", destDir, "error", removeErr)
		}
	}

	record.Status = "success"

	// DB cleanup happens after file rollback. If DB delete fails, the file
	// is still rolled back but the database record remains.
	if r.DB != nil {
		// Use a timeout context for DB operations
		ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := r.DB.DeletePost(ctxTimeout, op.PostID); err != nil {
			record.Status = "failed"
			record.Error = fmt.Sprintf("db delete failed: %v", err)
		}
	}

	return record
}

func (r *Rollback) validatePathAgainstRoot(pathStr, root string) error {
	absPath, err := filepath.Abs(filepath.Clean(pathStr))
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}

	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}

	// Resolve symlinks for root directory
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve symlinks for root: %w", err)
	}

	// For the path, we need to handle cases where the file doesn't exist yet
	// (during rollback, the source file path doesn't exist until we restore it).
	// First check the non-resolved path to catch obvious escapes
	resolvedRoot += string(filepath.Separator)
	if !strings.HasPrefix(absPath+string(filepath.Separator), resolvedRoot) {
		return fmt.Errorf("path %s escapes root %s", absPath, root)
	}

	// Also validate the directory containing the file to handle symlink attacks
	// Only resolve symlinks if the directory exists
	pathDir := filepath.Dir(absPath)
	if dirInfo, err := os.Stat(pathDir); err == nil && dirInfo.IsDir() {
		// Directory exists, resolve symlinks for additional security
		resolvedPathDir, err := filepath.EvalSymlinks(pathDir)
		if err != nil {
			return fmt.Errorf("resolve symlinks for path directory: %w", err)
		}
		if !strings.HasPrefix(resolvedPathDir+string(filepath.Separator), resolvedRoot) {
			return fmt.Errorf("path %s escapes root %s", absPath, root)
		}
	}
	// If directory doesn't exist, the prefix check above is sufficient
	// (we'll create the directory later if needed)

	return nil
}

func SaveRollbackLog(log *RollbackLog, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("failed to close rollback log file", "path", path, "error", cerr)
		}
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(log)
}
