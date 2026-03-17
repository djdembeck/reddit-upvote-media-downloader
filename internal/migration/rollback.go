package migration

import (
	"context"
	"encoding/json"
	"fmt"
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
		_ = file.Close()
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
		_ = os.Remove(op.SourcePath)
		record.Status = "error"
		record.Error = fmt.Sprintf("size mismatch after copy: expected %d, got %d", srcInfo.Size(), dstInfo.Size())
		return record
	}

	if err := os.Remove(op.DestPath); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("remove dest: %v", err)
		return record
	}

	// Attempt to remove empty destination directory (ignore errors if not empty)
	destDir := filepath.Dir(op.DestPath)
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) == 0 {
		_ = os.Remove(destDir)
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

func SaveRollbackLog(log *RollbackLog, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(log)
}
