package migration

import "time"

// PostInfo holds metadata extracted from HTML files.
type PostInfo struct {
	PostID     string
	Subreddit  string
	Username   string
	IsUserPost bool
	Hash       string // Optional: pre-computed hash for deduplication
}

// MigrationRecord represents a single file migration operation.
type MigrationRecord struct {
	PostID     string    `json:"post_id"`
	SourcePath string    `json:"source_path"`
	DestPath   string    `json:"dest_path"`
	Subreddit  string    `json:"subreddit"`
	Username   string    `json:"username"`
	IsUserPost bool      `json:"is_user_post"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	FileSize   int64     `json:"file_size"`
	Hash       string    `json:"hash,omitempty"`
}

// MigrationLog contains all migration operations and statistics.
type MigrationLog struct {
	Version      string            `json:"version"`
	Timestamp    time.Time         `json:"timestamp"`
	SourceDir    string            `json:"source_dir"`
	DestDir      string            `json:"dest_dir"`
	TotalFiles   int               `json:"total_files"`
	MovedCount   int               `json:"moved_count"`
	SkippedCount int               `json:"skipped_count"`
	ErrorCount   int               `json:"error_count"`
	WarningCount int               `json:"warning_count"`
	Operations   []MigrationRecord `json:"operations"`
}
