package migration

import "time"

// PostInfo holds metadata extracted from HTML files.
//
//nolint:govet // Field order kept for readability (related fields grouped)
type PostInfo struct {
	PostID     string
	Subreddit  string
	Username   string
	IsUserPost bool
	Hash       string // Optional: pre-computed hash for deduplication
}

// MigrationRecord represents a single file migration operation.
//
//nolint:revive
type MigrationRecord struct {
	PostID     string    `json:"postId"`
	SourcePath string    `json:"sourcePath"`
	DestPath   string    `json:"destPath"`
	Subreddit  string    `json:"subreddit"`
	Username   string    `json:"username"`
	IsUserPost bool      `json:"isUserPost"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	FileSize   int64     `json:"fileSize"`
	Hash       string    `json:"hash,omitempty"`
}

// MigrationLog contains all migration operations and statistics.
//
//nolint:revive
type MigrationLog struct {
	Version      string            `json:"version"`
	Timestamp    time.Time         `json:"timestamp"`
	SourceDir    string            `json:"sourceDir"`
	DestDir      string            `json:"destDir"`
	TotalFiles   int               `json:"totalFiles"`
	MovedCount   int               `json:"movedCount"`
	SkippedCount int               `json:"skippedCount"`
	ErrorCount   int               `json:"errorCount"`
	WarningCount int               `json:"warningCount"`
	Operations   []MigrationRecord `json:"operations"`
}
