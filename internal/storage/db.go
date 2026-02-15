// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the database connection.
type DB struct {
	conn *sql.DB
}

// schema is the database schema SQL.
const schema = `
CREATE TABLE IF NOT EXISTS posts (
    id TEXT PRIMARY KEY,
    title TEXT,
    subreddit TEXT,
    author TEXT,
    url TEXT,
    permalink TEXT,
    created_at INTEGER,
    downloaded_at INTEGER,
    media_type TEXT,
    file_path TEXT,
    source TEXT
);
`

// NewDB creates a new database connection and initializes the schema.
func NewDB(dbPath string) (*DB, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create the schema
	if _, err := conn.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// SavePost saves a post to the database. If the post already exists, it updates the record.
func (db *DB) SavePost(ctx context.Context, post *Post) error {
	query := `
		INSERT INTO posts (id, title, subreddit, author, url, permalink, created_at, downloaded_at, media_type, file_path, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			subreddit = excluded.subreddit,
			author = excluded.author,
			url = excluded.url,
			permalink = excluded.permalink,
			created_at = excluded.created_at,
			downloaded_at = excluded.downloaded_at,
			media_type = excluded.media_type,
			file_path = excluded.file_path,
			source = excluded.source
	`

	_, err := db.conn.ExecContext(ctx, query,
		post.ID,
		post.Title,
		post.Subreddit,
		post.Author,
		post.URL,
		post.Permalink,
		post.CreatedAt.Unix(),
		post.DownloadedAt.Unix(),
		post.MediaType,
		post.FilePath,
		post.Source,
	)

	if err != nil {
		return fmt.Errorf("failed to save post: %w", err)
	}

	return nil
}

// GetPost retrieves a post by its ID.
func (db *DB) GetPost(ctx context.Context, id string) (*Post, error) {
	query := `
		SELECT id, title, subreddit, author, url, permalink, created_at, downloaded_at, media_type, file_path, source
		FROM posts
		WHERE id = ?
	`

	row := db.conn.QueryRowContext(ctx, query, id)

	var post Post
	var createdAtUnix, downloadedAtUnix int64

	err := row.Scan(
		&post.ID,
		&post.Title,
		&post.Subreddit,
		&post.Author,
		&post.URL,
		&post.Permalink,
		&createdAtUnix,
		&downloadedAtUnix,
		&post.MediaType,
		&post.FilePath,
		&post.Source,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get post: %w", err)
	}

	post.CreatedAt = time.Unix(createdAtUnix, 0)
	post.DownloadedAt = time.Unix(downloadedAtUnix, 0)

	return &post, nil
}

// IsDownloaded checks if a post has been downloaded (exists in database).
func (db *DB) IsDownloaded(ctx context.Context, id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM posts WHERE id = ?)`

	var exists bool
	err := db.conn.QueryRowContext(ctx, query, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if post exists: %w", err)
	}

	return exists, nil
}

// GetStats returns download statistics.
func (db *DB) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		PostsBySource:    make(map[string]int64),
		PostsBySubreddit: make(map[string]int64),
		PostsByMediaType: make(map[string]int64),
	}

	// Get total count
	row := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts`)
	if err := row.Scan(&stats.TotalPosts); err != nil {
		return nil, fmt.Errorf("failed to get total count: %w", err)
	}

	// Get counts by source
	rows, err := db.conn.QueryContext(ctx, `SELECT source, COUNT(*) FROM posts GROUP BY source`)
	if err != nil {
		return nil, fmt.Errorf("failed to get source counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			return nil, fmt.Errorf("failed to scan source count: %w", err)
		}
		stats.PostsBySource[source] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating source rows: %w", err)
	}

	// Get counts by subreddit
	rows, err = db.conn.QueryContext(ctx, `SELECT subreddit, COUNT(*) FROM posts GROUP BY subreddit`)
	if err != nil {
		return nil, fmt.Errorf("failed to get subreddit counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var subreddit string
		var count int64
		if err := rows.Scan(&subreddit, &count); err != nil {
			return nil, fmt.Errorf("failed to scan subreddit count: %w", err)
		}
		stats.PostsBySubreddit[subreddit] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating subreddit rows: %w", err)
	}

	// Get counts by media type
	rows, err = db.conn.QueryContext(ctx, `SELECT media_type, COUNT(*) FROM posts GROUP BY media_type`)
	if err != nil {
		return nil, fmt.Errorf("failed to get media type counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var mediaType string
		var count int64
		if err := rows.Scan(&mediaType, &count); err != nil {
			return nil, fmt.Errorf("failed to scan media type count: %w", err)
		}
		stats.PostsByMediaType[mediaType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating media type rows: %w", err)
	}

	return stats, nil
}

// ImportFromIDList imports post IDs from an idList.txt file.
// The file format is one post ID per line. Empty lines and comments (starting with #) are ignored.
func (db *DB) ImportFromIDList(ctx context.Context, filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open idList file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	imported := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments (lines starting with #)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Remove inline comments (anything after #)
		if idx := strings.Index(line, "#"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		// Skip if empty after removing comments
		if line == "" {
			continue
		}

		// Extract post ID (remove any t3_ prefix if present)
		postID := strings.TrimPrefix(line, "t3_")

		// Check if already exists
		exists, err := db.IsDownloaded(ctx, postID)
		if err != nil {
			return imported, fmt.Errorf("failed to check if post exists: %w", err)
		}

		if !exists {
			// Create a minimal post entry
			post := &Post{
				ID:           postID,
				DownloadedAt: time.Now(),
				Source:       "imported",
			}

			if err := db.SavePost(ctx, post); err != nil {
				return imported, fmt.Errorf("failed to save imported post: %w", err)
			}
			imported++
		}
	}

	if err := scanner.Err(); err != nil {
		return imported, fmt.Errorf("error reading idList file: %w", err)
	}

	return imported, nil
}

// filenamePattern matches bdfr-html filenames like {POSTID}.ext or {POSTID}_1.ext
// Examples: abc123.jpg, def456_1.mp4, xyz789_2.png
// Reddit post IDs are typically 6-7 alphanumeric characters.
var filenamePattern = regexp.MustCompile(`^([a-zA-Z0-9]{6,})(?:_\d+)?\.\w+$`)

// ImportFromDirectory scans a directory for media files and imports post IDs from filenames.
// Supports bdfr-html filename patterns: {POSTID}.ext, {POSTID}_1.ext
func (db *DB) ImportFromDirectory(ctx context.Context, dirPath string) (int, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read directory: %w", err)
	}

	imported := 0
	seenIDs := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		matches := filenamePattern.FindStringSubmatch(filename)
		if matches == nil {
			// Not a bdfr-html filename pattern
			continue
		}

		postID := matches[1]

		// Skip if we've already processed this ID in this import
		if seenIDs[postID] {
			continue
		}
		seenIDs[postID] = true

		// Check if already exists in database
		exists, err := db.IsDownloaded(ctx, postID)
		if err != nil {
			return imported, fmt.Errorf("failed to check if post exists: %w", err)
		}

		if !exists {
			// Determine media type from extension
			ext := strings.ToLower(filepath.Ext(filename))
			mediaType := "unknown"
			switch ext {
			case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
				mediaType = "image"
			case ".mp4", ".webm", ".mov", ".avi", ".mkv":
				mediaType = "video"
			case ".gifv":
				mediaType = "gif"
			}

			// Create a post entry with file path
			post := &Post{
				ID:           postID,
				MediaType:    mediaType,
				FilePath:     filepath.Join(dirPath, filename),
				DownloadedAt: time.Now(),
				Source:       "imported",
			}

			if err := db.SavePost(ctx, post); err != nil {
				return imported, fmt.Errorf("failed to save imported post: %w", err)
			}
			imported++
		}
	}

	return imported, nil
}
