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

const (
	statusMoved            = "moved"
	statusMovedWithWarning = "moved_with_warning"
	statusError            = "error"
)

// Rollback handles reverting file migrations.
//
//nolint:govet // Field order kept for readability (LogPath first)
type Rollback struct {
	LogPath    string
	DB         *storage.DB
	SourceRoot string
	DestRoot   string
}

// RollbackLog contains rollback operation results.
//
//nolint:govet // Field order kept for readability (Timestamp first)
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

// NewRollback creates a new Rollback instance.
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
		if err := file.Close(); err != nil {
			slog.Debug("failed to close log file", "error", err)
		}
	}()

	var migLog MigrationLog
	if err := json.NewDecoder(file).Decode(&migLog); err != nil {
		return nil, fmt.Errorf("decode log: %w", err)
	}
	return &migLog, nil
}

// Execute performs the rollback operation.
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
		op := migLog.Operations[i]
		if op.Status != statusMoved && op.Status != statusMovedWithWarning {
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
		record.Status = statusError
		record.Error = fmt.Sprintf("invalid source path: %v", err)
		return record
	}
	if err := r.validatePathAgainstRoot(op.DestPath, r.DestRoot); err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("invalid dest path: %v", err)
		return record
	}

	// Check file exists
	if _, err := os.Stat(op.DestPath); os.IsNotExist(err) {
		record.Status = statusError
		record.Error = "file not found at destination"
		return record
	} else if err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("stat dest: %v", err)
		return record
	}

	// Ensure source dir exists
	sourceDir := filepath.Dir(op.SourcePath)
	if err := os.MkdirAll(sourceDir, 0750); err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("create dir: %v", err)
		return record
	}

	// Check if source file already exists (would overwrite)
	if _, err := os.Stat(op.SourcePath); err == nil {
		record.Status = statusError
		record.Error = "source file exists, aborting rollback"
		return record
	} else if !os.IsNotExist(err) {
		record.Status = statusError
		record.Error = fmt.Sprintf("stat source: %v", err)
		return record
	}

	if err := copyFile(op.DestPath, op.SourcePath); err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("copy file: %v", err)
		return record
	}

	srcInfo, err := os.Stat(op.DestPath)
	if err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("stat source file %s: %v", op.DestPath, err)
		return record
	}
	dstInfo, err := os.Stat(op.SourcePath)
	if err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("stat destination file %s: %v", op.SourcePath, err)
		return record
	}
	if srcInfo.Size() != dstInfo.Size() {
		if rmErr := os.Remove(op.SourcePath); rmErr != nil {
			slog.Debug("failed to remove source file after size mismatch", "error", rmErr)
		}
		record.Status = statusError
		record.Error = fmt.Sprintf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
		return record
	}

	if err := os.Remove(op.DestPath); err != nil {
		record.Status = statusError
		record.Error = fmt.Sprintf("remove dest: %v", err)
		return record
	}

	// Attempt to remove empty destination directory (ignore errors if not empty)
	destDir := filepath.Dir(op.DestPath)
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) == 0 {
		if rmErr := os.Remove(destDir); rmErr != nil {
			slog.Debug("failed to remove empty destination directory", "error", rmErr)
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
	rootAbs += string(filepath.Separator)
	if !strings.HasPrefix(absPath+string(filepath.Separator), rootAbs) {
		return fmt.Errorf("path %s escapes root %s", absPath, root)
	}
	return nil
}

// SaveRollbackLog saves the rollback log to the specified path.
func SaveRollbackLog(log *RollbackLog, path string) error {
	file, err := os.Create(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("create rollback log: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Debug("failed to close rollback log file", "error", err)
		}
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(log); err != nil {
		return fmt.Errorf("encode rollback log: %w", err)
	}

	return nil
}
