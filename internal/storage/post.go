// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import "time"

// Metadata value constants.
const (
	MetadataValuePending = "pending"
)

// Post represents a downloaded Reddit post.
type Post struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Subreddit    string    `json:"subreddit"`
	Author       string    `json:"author"`
	URL          string    `json:"url"`
	Permalink    string    `json:"permalink"`
	CreatedAt    time.Time `json:"createdAt"`
	DownloadedAt time.Time `json:"downloadedAt"`
	MediaType    string    `json:"mediaType"`
	FilePath     string    `json:"filePath"`
	Source       string    `json:"source"` // 'upvoted' or 'saved'
	RetryCount   int       `json:"retryCount"`
	LastError    string    `json:"lastError"`
	LastAttempt  time.Time `json:"lastAttempt"`
	Hash         string    `json:"hash"` // hash of the media file for deduplication
}

// Stats represents download statistics.
type Stats struct {
	TotalPosts       int64            `json:"totalPosts"`
	PostsBySource    map[string]int64 `json:"postsBySource"`
	PostsBySubreddit map[string]int64 `json:"postsBySubreddit"`
	PostsByMediaType map[string]int64 `json:"postsByMediaType"`
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
