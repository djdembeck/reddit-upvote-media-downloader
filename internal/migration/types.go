package migration

import "time"

type PostInfo struct {
	PostID     string
	Subreddit  string
	Username   string
	IsUserPost bool
}

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
}

type MigrationLog struct {
	Version      string            `json:"version"`
	Timestamp    time.Time         `json:"timestamp"`
	SourceDir    string            `json:"source_dir"`
	DestDir      string            `json:"dest_dir"`
	TotalFiles   int               `json:"total_files"`
	MovedCount   int               `json:"moved_count"`
	SkippedCount int               `json:"skipped_count"`
	ErrorCount   int               `json:"error_count"`
	Operations   []MigrationRecord `json:"operations"`
}
