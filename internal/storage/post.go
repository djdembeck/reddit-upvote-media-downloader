// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import "time"

// Post represents a downloaded Reddit post.
type Post struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Subreddit    string    `json:"subreddit"`
	Author       string    `json:"author"`
	URL          string    `json:"url"`
	Permalink    string    `json:"permalink"`
	CreatedAt    time.Time `json:"created_at"`
	DownloadedAt time.Time `json:"downloaded_at"`
	MediaType    string    `json:"media_type"`
	FilePath     string    `json:"file_path"`
	Source       string    `json:"source"` // 'upvoted' or 'saved'
	RetryCount   int       `json:"retry_count"`
	LastError    string    `json:"last_error"`
	LastAttempt  time.Time `json:"last_attempt"`
}

// Stats represents download statistics.
type Stats struct {
	TotalPosts       int64            `json:"total_posts"`
	PostsBySource    map[string]int64 `json:"posts_by_source"`
	PostsBySubreddit map[string]int64 `json:"posts_by_subreddit"`
	PostsByMediaType map[string]int64 `json:"posts_by_media_type"`
}

// PostStatus represents the detailed status of a post for download eligibility checking.
type PostStatus struct {
	Exists        bool      // Post exists in DB
	FileExists    bool      // File exists on disk (only valid if FilePath is set)
	RetryCount    int       // Current retry count
	ShouldSkip    bool      // Should be skipped (permanent failure or within backoff)
	RetryEligible bool      // Eligible for retry (file missing, backoff passed, or never attempted)
	LastAttempt   time.Time // Last attempt time (zero if never attempted)
	LastError     string    // Last error message (if any)
	FilePath      string    // File path from database (if set)
}
