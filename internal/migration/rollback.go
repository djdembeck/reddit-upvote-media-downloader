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

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/ownutil"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

// Status constants for migration operations
const (
	StatusMoved            = "moved"
	StatusMovedWithWarning = "moved_with_warning"
	StatusError            = "error"
	StatusSuccess          = "success"
	StatusFailed           = "failed"
)

// Rollback handles reverting file migrations.
//
//nolint:fieldalignment
type Rollback struct {
	DB         *storage.DB
	LogPath    string
	SourceRoot string
	DestRoot   string
	Owner      *ownutil.Owner
}

// RollbackLog contains rollback operation results.
//
//nolint:fieldalignment
type RollbackLog struct {
	Timestamp    time.Time        `json:"timestamp"`
	Operations   []RollbackRecord `json:"operations"`
	OriginalLog  string           `json:"originalLog"`
	SuccessCount int              `json:"successCount"`
	ErrorCount   int              `json:"errorCount"`
}

// RollbackRecord represents a single rollback operation.
//
//nolint:fieldalignment
type RollbackRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	PostID     string    `json:"postId"`
	SourcePath string    `json:"sourcePath"`
	DestPath   string    `json:"destPath"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

// NewRollback creates a new Rollback instance for reversing a previous migration.
// It loads the migration log from logPath and prepares for rollback operations.
// The sourceRoot and destRoot parameters should match the original migration paths.
func NewRollback(logPath string, db *storage.DB, sourceRoot, destRoot string, owner *ownutil.Owner) *Rollback {
	return &Rollback{
		LogPath:    logPath,
		DB:         db,
		SourceRoot: sourceRoot,
		DestRoot:   destRoot,
		Owner:      owner,
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

		if record.Status == StatusSuccess {
			rollbackLog.SuccessCount++
		} else {
			rollbackLog.ErrorCount++
		}
	}

	return rollbackLog, nil
}

// validateRollbackPaths validates that the source and destination paths are within allowed roots.
func (r *Rollback) validateRollbackPaths(op Record) error {
	// Validate paths are within trusted roots - source path must be under SourceRoot, dest path under DestRoot
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		return fmt.Errorf("invalid source path: %v", err)
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		return fmt.Errorf("invalid dest path: %v", err)
	}
	return nil
}

// performFileRollback performs the actual file rollback operations.
//
//nolint:cyclop,gocyclo
func (r *Rollback) performFileRollback(ctx context.Context, op Record) error {
	// Check file exists and is not a symlink
	if info, err := os.Lstat(op.DestPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found at destination")
		}
		return fmt.Errorf("stat dest: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination path is a symlink, aborting rollback")
	}

	// Ensure source dir exists
	sourceDir := filepath.Dir(op.SourcePath)
	if err := r.Owner.ChownMkdirAllContext(ctx, sourceDir, 0750, slog.Default()); err != nil {
		return fmt.Errorf("create and chown source dir: %w", err)
	}

	// Re-validate paths after MkdirAll to prevent TOCTOU symlink attacks
	if err := r.validatePathAgainstRoot(op.SourcePath, r.SourceRoot); err != nil {
		return fmt.Errorf("source path validation failed after mkdir: %v", err)
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		return fmt.Errorf("dest path validation failed after mkdir: %v", err)
	}

	// Check if source file already exists (would overwrite)
	if _, err := os.Stat(op.SourcePath); err == nil {
		return fmt.Errorf("source file exists, aborting rollback")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat source: %v", err)
	}

	if err := copyFile(op.DestPath, op.SourcePath, r.Owner); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	srcInfo, err := os.Stat(op.DestPath)
	if err != nil {
		return fmt.Errorf("stat source file %s: %v", op.DestPath, err)
	}
	dstInfo, err := os.Stat(op.SourcePath)
	if err != nil {
		return fmt.Errorf("stat destination file %s: %v", op.SourcePath, err)
	}
	if srcInfo.Size() != dstInfo.Size() {
		cleanupErr := os.Remove(op.SourcePath)
		if cleanupErr != nil {
			return fmt.Errorf("size mismatch after copy: expected %d, got %d; cleanup failed: %w", srcInfo.Size(), dstInfo.Size(), cleanupErr)
		}
		return fmt.Errorf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
	}

	if err := os.Remove(op.DestPath); err != nil {
		return fmt.Errorf("remove dest: %v", err)
	}

	// Attempt to remove empty destination directory
	destDir := filepath.Dir(op.DestPath)
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) == 0 {
		if removeErr := os.Remove(destDir); removeErr != nil {
			slog.Warn("failed to remove empty destination directory",
				"dir", destDir, "error", removeErr)
		}
	}

	return nil
}

// cleanupRollbackDB cleans up the database after a successful file rollback.
func (r *Rollback) cleanupRollbackDB(ctx context.Context, postID string) error {
	if r.DB == nil {
		return nil
	}
	// Use a timeout context for DB operations
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.DB.DeletePost(ctxTimeout, postID); err != nil {
		return fmt.Errorf("db delete failed: %v", err)
	}
	return nil
}

//nolint:cyclop
func (r *Rollback) rollbackOperation(ctx context.Context, op Record) RollbackRecord {
	record := RollbackRecord{
		PostID:     op.PostID,
		SourcePath: op.DestPath,
		DestPath:   op.SourcePath,
		Timestamp:  time.Now(),
	}

	// Validate paths
	if err := r.validateRollbackPaths(op); err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}

	// Perform file rollback
	if err := r.performFileRollback(ctx, op); err != nil {
		record.Status = StatusError
		record.Error = err.Error()
		return record
	}

	record.Status = StatusSuccess

	// DB cleanup happens after file rollback. If DB delete fails, the file
	// is still rolled back but the database record remains.
	if err := r.cleanupRollbackDB(ctx, op.PostID); err != nil {
		record.Status = StatusFailed
		record.Error = err.Error()
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
	if err != nil {
		return fmt.Errorf("root directory %s does not exist: %w", rootAbs, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("root path %s is not a directory", rootAbs)
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
func SaveRollbackLog(log *RollbackLog, path string, owner *ownutil.Owner) error {
	//nolint:gosec // G304: path is validated by caller before this function
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create rollback log file: %w", err)
	}
	if err := owner.Chown(path); err != nil {
		slog.Warn("failed to chown rollback log file", "path", path, "error", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("failed to close rollback log file", "path", path, "error", cerr)
		}
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(log); err != nil {
		return fmt.Errorf("encode rollback log: %w", err)
	}
	return nil
}
