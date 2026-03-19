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

// Status constants for migration operations
const (
	StatusMoved            = "moved"
	StatusMovedWithWarning = "moved_with_warning"
	StatusError            = "error"
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
	OriginalLog  string           `json:"originalLog"`
	SuccessCount int              `json:"successCount"`
	ErrorCount   int              `json:"errorCount"`
	Operations   []RollbackRecord `json:"operations"`
}

// RollbackRecord represents a single rollback operation.
type RollbackRecord struct {
	PostID     string    `json:"postId"`
	SourcePath string    `json:"sourcePath"`
	DestPath   string    `json:"destPath"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// NewRollback creates a new Rollback instance for reversing a previous migration.
// It loads the migration log from logPath and prepares for rollback operations.
// The sourceRoot and destRoot parameters should match the original migration paths.
func NewRollback(logPath string, db *storage.DB, sourceRoot, destRoot string) *Rollback {
	return &Rollback{
		LogPath:    logPath,
		DB:         db,
		SourceRoot: sourceRoot,
		DestRoot:   destRoot,
	}
}

func (r *Rollback) loadLog() (*Log, error) {
	file, err := os.Open(r.LogPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("failed to close rollback log file", "path", r.LogPath, "error", cerr)
		}
	}()

	var migLog Log
	if err := json.NewDecoder(file).Decode(&migLog); err != nil {
		return nil, fmt.Errorf("decode log: %w", err)
	}
	return &migLog, nil
}

// Execute performs the rollback operation, moving files back from the destination
// directory to their original source locations. It returns a RollbackLog containing
// the results of each rollback operation.
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
			return rollbackLog, fmt.Errorf("rollback canceled: %w", err)
		}

		op := migLog.Operations[i]
		if op.Status != StatusMoved && op.Status != StatusMovedWithWarning {
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

func (r *Rollback) rollbackOperation(ctx context.Context, op Record) RollbackRecord {
	record := RollbackRecord{
		PostID:     op.PostID,
		SourcePath: op.DestPath,
		DestPath:   op.SourcePath,
		Timestamp:  time.Now(),
	}

	// Validate paths are within trusted roots - source path must be under SourceRoot, dest path under DestRoot
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("invalid source path: %v", err)
		return record
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("invalid dest path: %v", err)
		return record
	}

	// Check file exists and is not a symlink
	if info, err := os.Lstat(op.DestPath); err != nil {
		if os.IsNotExist(err) {
			record.Status = StatusError
			record.Error = "file not found at destination"
			return record
		}
		record.Status = StatusError
		record.Error = fmt.Sprintf("stat dest: %v", err)
		return record
	} else if info.Mode()&os.ModeSymlink != 0 {
		record.Status = StatusError
		record.Error = "destination path is a symlink, aborting rollback"
		return record
	}

	// Ensure source dir exists
	sourceDir := filepath.Dir(op.SourcePath)
	if err := os.MkdirAll(sourceDir, 0750); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("create dir: %v", err)
		return record
	}

	// Re-validate paths after MkdirAll to prevent TOCTOU symlink attacks
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("source path validation failed after mkdir: %v", err)
		return record
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("dest path validation failed after mkdir: %v", err)
		return record
	}

	// Check if source file already exists (would overwrite)
	if _, err := os.Stat(op.SourcePath); err == nil {
		record.Status = StatusError
		record.Error = "source file exists, aborting rollback"
		return record
	} else if !os.IsNotExist(err) {
		record.Status = StatusError
		record.Error = fmt.Sprintf("stat source: %v", err)
		return record
	}

	if err := copyFile(op.DestPath, op.SourcePath); err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("copy file: %v", err)
		return record
	}

	srcInfo, err := os.Stat(op.DestPath)
	if err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("stat source file %s: %v", op.DestPath, err)
		return record
	}
	dstInfo, err := os.Stat(op.SourcePath)
	if err != nil {
		record.Status = StatusError
		record.Error = fmt.Sprintf("stat destination file %s: %v", op.SourcePath, err)
		return record
	}
	if srcInfo.Size() != dstInfo.Size() {
		cleanupErr := os.Remove(op.SourcePath)
		record.Status = StatusError
		record.Error = fmt.Sprintf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
		if cleanupErr != nil {
			record.Error += fmt.Sprintf("; cleanup failed: %v", cleanupErr)
		}
		return record
	}

	if err := os.Remove(op.DestPath); err != nil {
		record.Status = StatusError
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

// resolveExistingOrParent resolves a path to its canonical form.
// If the path exists, it fully resolves symlinks. If the path doesn't exist,
// it resolves the parent directory and reconstructs the path.
func resolveExistingOrParent(path string) (string, error) {
	// Try to resolve the path if it exists
	if _, err := os.Lstat(path); err == nil {
		// Path exists - resolve symlinks fully
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("resolve symlinks: %w", err)
		}
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat path: %w", err)
	}

	// Path doesn't exist - resolve parent directory
	pathDir := filepath.Dir(path)
	if dirInfo, err := os.Stat(pathDir); err == nil && dirInfo.IsDir() {
		resolvedDir, err := filepath.EvalSymlinks(pathDir)
		if err == nil {
			return filepath.Join(resolvedDir, filepath.Base(path)), nil
		}
	}

	// Return original path if parent couldn't be resolved
	return path, nil
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

	// First check lexical containment before any symlink resolution.
	// This catches obvious path escapes even for non-existent paths.
	rootAbsWithSep := rootAbs + string(filepath.Separator)
	if !strings.HasPrefix(absPath+string(filepath.Separator), rootAbsWithSep) {
		return fmt.Errorf("path %s escapes root %s", absPath, root)
	}

	// For symlink-aware validation, only resolve symlinks for existing paths.
	// Non-existent paths pass the lexical check above.
	rootInfo, err := os.Stat(rootAbs)
	if err != nil || !rootInfo.IsDir() {
		return fmt.Errorf("root directory %s does not exist or is not a directory: %w", root, err)
	}

	// Resolve symlinks for root directory
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve root symlinks: %w", err)
	}

	// Resolve the path (existing or parent-based)
	resolvedPath, err := resolveExistingOrParent(absPath)
	if err != nil {
		return fmt.Errorf("resolve path symlinks: %w", err)
	}

	// Check containment using resolved paths
	resolvedRootWithSep := resolvedRoot + string(filepath.Separator)
	if !strings.HasPrefix(resolvedPath+string(filepath.Separator), resolvedRootWithSep) {
		return fmt.Errorf("path %s escapes root %s via symlink", absPath, root)
	}

	return nil
}

// SaveRollbackLog saves the rollback log to a JSON file for audit purposes.
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
