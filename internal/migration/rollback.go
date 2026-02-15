package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Rollback struct {
	LogPath string
}

type RollbackLog struct {
	Timestamp    time.Time        `json:"timestamp"`
	OriginalLog  string           `json:"original_log"`
	SuccessCount int              `json:"success_count"`
	ErrorCount   int              `json:"error_count"`
	Operations   []RollbackRecord `json:"operations"`
}

type RollbackRecord struct {
	PostID     string    `json:"post_id"`
	SourcePath string    `json:"source_path"`
	DestPath   string    `json:"dest_path"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

func NewRollback(logPath string) *Rollback {
	return &Rollback{LogPath: logPath}
}

func (r *Rollback) Execute() (*RollbackLog, error) {
	// Load migration log
	file, err := os.Open(r.LogPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer file.Close()

	var migLog MigrationLog
	if err := json.NewDecoder(file).Decode(&migLog); err != nil {
		return nil, fmt.Errorf("decode log: %w", err)
	}

	rollbackLog := &RollbackLog{
		Timestamp:   time.Now(),
		OriginalLog: r.LogPath,
		Operations:  []RollbackRecord{},
	}

	// Process in reverse order
	for i := len(migLog.Operations) - 1; i >= 0; i-- {
		op := migLog.Operations[i]
		if op.Status != "moved" {
			continue
		}

		record := r.rollbackOperation(op)
		rollbackLog.Operations = append(rollbackLog.Operations, record)

		if record.Status == "success" {
			rollbackLog.SuccessCount++
		} else {
			rollbackLog.ErrorCount++
		}
	}

	return rollbackLog, nil
}

func (r *Rollback) rollbackOperation(op MigrationRecord) RollbackRecord {
	record := RollbackRecord{
		PostID:     op.PostID,
		SourcePath: op.DestPath,
		DestPath:   op.SourcePath,
		Timestamp:  time.Now(),
	}

	// Check file exists
	if _, err := os.Stat(op.DestPath); os.IsNotExist(err) {
		record.Status = "error"
		record.Error = "file not found at destination"
		return record
	}

	// Ensure source dir exists
	sourceDir := filepath.Dir(op.SourcePath)
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("create dir: %v", err)
		return record
	}

	if err := copyFile(op.DestPath, op.SourcePath); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("copy file: %v", err)
		return record
	}

	srcInfo, _ := os.Stat(op.DestPath)
	dstInfo, _ := os.Stat(op.SourcePath)
	if srcInfo.Size() != dstInfo.Size() {
		os.Remove(op.SourcePath)
		record.Status = "error"
		record.Error = "size mismatch after copy"
		return record
	}

	if err := os.Remove(op.DestPath); err != nil {
		record.Status = "error"
		record.Error = fmt.Sprintf("remove dest: %v", err)
		return record
	}

	destDir := filepath.Dir(op.DestPath)
	os.Remove(destDir)

	record.Status = "success"
	return record
}

func SaveRollbackLog(log *RollbackLog, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(log)
}
