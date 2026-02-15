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
}

// Stats represents download statistics.
type Stats struct {
	TotalPosts       int64            `json:"total_posts"`
	PostsBySource    map[string]int64 `json:"posts_by_source"`
	PostsBySubreddit map[string]int64 `json:"posts_by_subreddit"`
	PostsByMediaType map[string]int64 `json:"posts_by_media_type"`
}
