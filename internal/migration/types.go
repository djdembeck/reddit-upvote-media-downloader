package migration

import "time"

// Default values for migration operations
const (
	UnknownSubreddit = "unknown"
)

// PostInfo holds metadata extracted from HTML files.
//
//nolint:fieldalignment
type PostInfo struct {
	PostID     string
	Subreddit  string
	Username   string
	Hash       string // Optional: pre-computed hash for deduplication
	IsUserPost bool
}

// Record represents a single file migration operation.
//
//nolint:fieldalignment
type Record struct {
	Timestamp  time.Time `json:"timestamp"`
	PostID     string    `json:"postId"`
	SourcePath string    `json:"sourcePath"`
	DestPath   string    `json:"destPath"`
	Subreddit  string    `json:"subreddit"`
	Username   string    `json:"username"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Hash       string    `json:"hash,omitempty"`
	FileSize   int64     `json:"fileSize"`
	IsUserPost bool      `json:"isUserPost"`
}

// Log contains all migration operations and statistics.
//
//nolint:fieldalignment
type Log struct {
	Timestamp    time.Time `json:"timestamp"`
	Operations   []Record  `json:"operations"`
	Version      string    `json:"version"`
	SourceDir    string    `json:"sourceDir"`
	DestDir      string    `json:"destDir"`
	TotalFiles   int       `json:"totalFiles"`
	MovedCount   int       `json:"movedCount"`
	SkippedCount int       `json:"skippedCount"`
	ErrorCount   int       `json:"errorCount"`
	WarningCount int       `json:"warningCount"`
}
