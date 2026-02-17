// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"math"
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

CREATE TABLE IF NOT EXISTS metadata (
    key TEXT PRIMARY KEY,
    value TEXT
);
`

// migration statements to add new columns if they don't exist
const addRetryCountColumn = `
ALTER TABLE posts ADD COLUMN retry_count INTEGER DEFAULT 0;
`

const addLastErrorColumn = `
ALTER TABLE posts ADD COLUMN last_error TEXT;
`

const addLastAttemptColumn = `
ALTER TABLE posts ADD COLUMN last_attempt INTEGER;
`

// runMigrations adds new columns to the posts table if they don't exist.
// This is idempotent - safe to run multiple times.
func (db *DB) runMigrations() error {
	// Get existing columns
	rows, err := db.conn.Query("PRAGMA table_info(posts)")
	if err != nil {
		return fmt.Errorf("failed to query table info: %w", err)
	}
	defer rows.Close()

	existingColumns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var type_ string
		var notnull int
		var dflt_value interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &type_, &notnull, &dflt_value, &pk); err != nil {
			return fmt.Errorf("failed to scan table info: %w", err)
		}
		existingColumns[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating table info: %w", err)
	}

	// Add retry_count column if it doesn't exist
	if !existingColumns["retry_count"] {
		if _, err := db.conn.Exec(addRetryCountColumn); err != nil {
			return fmt.Errorf("failed to add retry_count column: %w", err)
		}
	}

	// Add last_error column if it doesn't exist
	if !existingColumns["last_error"] {
		if _, err := db.conn.Exec(addLastErrorColumn); err != nil {
			return fmt.Errorf("failed to add last_error column: %w", err)
		}
	}

	// Add last_attempt column if it doesn't exist
	if !existingColumns["last_attempt"] {
		if _, err := db.conn.Exec(addLastAttemptColumn); err != nil {
			return fmt.Errorf("failed to add last_attempt column: %w", err)
		}
	}

	return nil
}

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

	db := &DB{conn: conn}

	// Run migrations to add new columns
	if err := db.runMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
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
		SELECT id, title, subreddit, author, url, permalink, created_at, downloaded_at, media_type, file_path, source, retry_count, last_error, last_attempt
		FROM posts
		WHERE id = ?
	`

	row := db.conn.QueryRowContext(ctx, query, id)

	var post Post
	var title, subreddit, author, url, permalink, mediaType, filePath, source sql.NullString
	var createdAtUnix, downloadedAtUnix sql.NullInt64
	var retryCount sql.NullInt64
	var lastError sql.NullString
	var lastAttempt sql.NullInt64

	err := row.Scan(
		&post.ID,
		&title,
		&subreddit,
		&author,
		&url,
		&permalink,
		&createdAtUnix,
		&downloadedAtUnix,
		&mediaType,
		&filePath,
		&source,
		&retryCount,
		&lastError,
		&lastAttempt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get post: %w", err)
	}

	if createdAtUnix.Valid {
		post.CreatedAt = time.Unix(createdAtUnix.Int64, 0)
	}
	if downloadedAtUnix.Valid {
		post.DownloadedAt = time.Unix(downloadedAtUnix.Int64, 0)
	}

	if title.Valid {
		post.Title = title.String
	}
	if subreddit.Valid {
		post.Subreddit = subreddit.String
	}
	if author.Valid {
		post.Author = author.String
	}
	if url.Valid {
		post.URL = url.String
	}
	if permalink.Valid {
		post.Permalink = permalink.String
	}
	if mediaType.Valid {
		post.MediaType = mediaType.String
	}
	if filePath.Valid {
		post.FilePath = filePath.String
	}
	if source.Valid {
		post.Source = source.String
	}
	if retryCount.Valid {
		post.RetryCount = int(retryCount.Int64)
	}
	if lastError.Valid {
		post.LastError = lastError.String
	}
	if lastAttempt.Valid {
		post.LastAttempt = time.Unix(lastAttempt.Int64, 0)
	}

	return &post, nil
}

// IsDownloaded checks if a post should be treated as downloaded (skip downloading).
// This is a convenience wrapper around CheckPostStatus that preserves backward compatibility.
// Returns true if the post should be skipped: it exists and either has a file on disk,
// is within a backoff period, or has exceeded retry threshold.
func (db *DB) IsDownloaded(ctx context.Context, id string) (bool, error) {
	status, err := db.CheckPostStatus(ctx, id, 0, 0, 0)
	if err != nil {
		return false, err
	}
	// Treat as downloaded if it exists but shouldn't be retried
	return status.Exists && !status.RetryEligible, nil
}

// CheckPostStatus returns detailed status of a post for download eligibility checking.
// It checks file existence on disk (if file_path is set), retry count against threshold,
// and last_attempt against backoff window.
// Parameters:
//   - threshold: max retry count before permanent skip (0 = ignore)
//   - backoffBase: base delay for exponential backoff calculation (0 = ignore)
//   - backoffMax: max delay cap for backoff calculation (0 = ignore)
func (db *DB) CheckPostStatus(ctx context.Context, id string, threshold int, backoffBase, backoffMax time.Duration) (*PostStatus, error) {
	query := `
		SELECT retry_count, last_error, last_attempt, file_path
		FROM posts
		WHERE id = ?
	`

	status := &PostStatus{
		Exists:        false,
		FileExists:    false,
		RetryCount:    0,
		ShouldSkip:    false,
		RetryEligible: true,
	}

	var lastError sql.NullString
	var lastAttempt sql.NullInt64
	var filePath sql.NullString
	var retryCount sql.NullInt64

	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&retryCount,
		&lastError,
		&lastAttempt,
		&filePath,
	)

	if err == sql.ErrNoRows {
		// Post doesn't exist - eligible for download
		return status, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to check post status: %w", err)
	}

	// Post exists in DB
	status.Exists = true
	status.RetryEligible = false // Will be set to true if eligible for retry

	// Extract values from NULLable columns
	if retryCount.Valid {
		status.RetryCount = int(retryCount.Int64)
	}
	if lastError.Valid {
		status.LastError = lastError.String
	}
	if lastAttempt.Valid {
		status.LastAttempt = time.Unix(lastAttempt.Int64, 0)
	}
	if filePath.Valid {
		status.FilePath = filePath.String
	}

	// Check 1: Retry count exceeds threshold (permanent skip)
	if threshold > 0 && status.RetryCount > threshold {
		status.ShouldSkip = true
		status.RetryEligible = false
		return status, nil
	}

	// Check 2: File existence on disk (if file_path is set)
	if status.FilePath != "" {
		_, err := os.Stat(status.FilePath)
		if err == nil {
			// File exists on disk - no need to retry
			status.FileExists = true
			status.ShouldSkip = true
			status.RetryEligible = false
			return status, nil
		}
		// File doesn't exist - could be eligible for retry, continue to check backoff
		status.FileExists = false
	} else {
		// No file_path set - for backward compatibility:
		// If never attempted (retry_count == 0), treat as downloaded (legacy behavior)
		// If has retry history, need to check backoff/threshold below
		if status.RetryCount == 0 {
			status.ShouldSkip = true
			status.RetryEligible = false
			return status, nil
		}
		// Has retry history but no file_path - continue to check backoff
	}

	// Check 3: Backoff window (only if retry history exists)
	// If last_attempt is set and backoff parameters are provided, check if within backoff
	if !status.LastAttempt.IsZero() && backoffBase > 0 && status.RetryCount > 0 {
		// Calculate backoff delay: min(backoffBase * 2^retryCount, backoffMax)
		backoffDelay := time.Duration(float64(backoffBase) * math.Pow(2, float64(status.RetryCount)))
		if backoffMax > 0 && backoffDelay > backoffMax {
			backoffDelay = backoffMax
		}

		elapsed := time.Since(status.LastAttempt)
		if elapsed < backoffDelay {
			// Still within backoff window - should skip for now
			status.ShouldSkip = true
			status.RetryEligible = false
			return status, nil
		}
	}

	// Post is eligible for retry if we got here:
	// - Has file_path but file is missing, OR
	// - Has retry history and backoff has passed, OR
	// - Has retry history but no backoff params (immediate retry)
	status.RetryEligible = true
	status.ShouldSkip = false
	return status, nil
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

// SetMetadata sets a metadata key-value pair. If the key already exists, it updates the value.
func (db *DB) SetMetadata(ctx context.Context, key, value string) error {
	query := `
		INSERT OR REPLACE INTO metadata (key, value)
		VALUES (?, ?)
	`

	_, err := db.conn.ExecContext(ctx, query, key, value)
	if err != nil {
		return fmt.Errorf("failed to set metadata: %w", err)
	}

	return nil
}

// GetMetadata retrieves a metadata value by key. Returns empty string if key doesn't exist.
func (db *DB) GetMetadata(ctx context.Context, key string) (string, error) {
	query := `
		SELECT value FROM metadata
		WHERE key = ?
	`

	var value string
	err := db.conn.QueryRowContext(ctx, query, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get metadata: %w", err)
	}

	return value, nil
}

// IncrementRetry increments the retry count for a post and records the error.
// If the post doesn't exist, it returns an error.
func (db *DB) IncrementRetry(ctx context.Context, postID string, errorMsg string) error {
	query := `
		UPDATE posts
		SET retry_count = retry_count + 1,
		    last_error = ?,
		    last_attempt = ?
		WHERE id = ?
	`

	now := time.Now().Unix()
	result, err := db.conn.ExecContext(ctx, query, errorMsg, now, postID)
	if err != nil {
		return fmt.Errorf("failed to increment retry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("post not found: %s", postID)
	}

	return nil
}

// ResetRetry resets the retry count for a post to 0 and clears error fields.
// If the post doesn't exist, it returns an error.
func (db *DB) ResetRetry(ctx context.Context, postID string) error {
	query := `
		UPDATE posts
		SET retry_count = 0,
		    last_error = NULL,
		    last_attempt = NULL
		WHERE id = ?
	`

	result, err := db.conn.ExecContext(ctx, query, postID)
	if err != nil {
		return fmt.Errorf("failed to reset retry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("post not found: %s", postID)
	}

	return nil
}

// GetAllPosts returns all posts from the database.
// Used for re-check mode to verify file existence on disk.
func (db *DB) GetAllPosts(ctx context.Context) ([]Post, error) {
	query := `
		SELECT id, title, subreddit, author, url, permalink, created_at, downloaded_at, media_type, file_path, source, retry_count, last_error, last_attempt
		FROM posts
	`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all posts: %w", err)
	}
	defer rows.Close()

	var posts []Post

	for rows.Next() {
		var post Post
		var title, subreddit, author, url, permalink, mediaType, filePath, source sql.NullString
		var createdAtUnix, downloadedAtUnix sql.NullInt64
		var retryCount sql.NullInt64
		var lastError sql.NullString
		var lastAttempt sql.NullInt64

		err := rows.Scan(
			&post.ID,
			&title,
			&subreddit,
			&author,
			&url,
			&permalink,
			&createdAtUnix,
			&downloadedAtUnix,
			&mediaType,
			&filePath,
			&source,
			&retryCount,
			&lastError,
			&lastAttempt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}

		if createdAtUnix.Valid {
			post.CreatedAt = time.Unix(createdAtUnix.Int64, 0)
		}
		if downloadedAtUnix.Valid {
			post.DownloadedAt = time.Unix(downloadedAtUnix.Int64, 0)
		}
		if title.Valid {
			post.Title = title.String
		}
		if subreddit.Valid {
			post.Subreddit = subreddit.String
		}
		if author.Valid {
			post.Author = author.String
		}
		if url.Valid {
			post.URL = url.String
		}
		if permalink.Valid {
			post.Permalink = permalink.String
		}
		if mediaType.Valid {
			post.MediaType = mediaType.String
		}
		if filePath.Valid {
			post.FilePath = filePath.String
		}
		if source.Valid {
			post.Source = source.String
		}
		if retryCount.Valid {
			post.RetryCount = int(retryCount.Int64)
		}
		if lastError.Valid {
			post.LastError = lastError.String
		}
		if lastAttempt.Valid {
			post.LastAttempt = time.Unix(lastAttempt.Int64, 0)
		}

		posts = append(posts, post)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating posts: %w", err)
	}

	return posts, nil
}

// GetRetryCount returns the current retry count for a post.
// Returns 0 if the post doesn't exist.
func (db *DB) GetRetryCount(ctx context.Context, postID string) (int, error) {
	query := `
		SELECT retry_count FROM posts
		WHERE id = ?
	`

	var retryCount int
	err := db.conn.QueryRowContext(ctx, query, postID).Scan(&retryCount)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get retry count: %w", err)
	}

	return retryCount, nil
}

// GetPostsToRetry returns post IDs that are eligible for retry based on backoff settings.
// It considers posts where:
// - retry_count < threshold (not permanently skipped)
// - Either retry_count == 0 (never tried) OR enough time has passed since last_attempt
// backoffDelay = min(backoffBase * 2^retry_count, backoffMax)
func (db *DB) GetPostsToRetry(ctx context.Context, backoffBase, backoffMax time.Duration, threshold int) ([]string, error) {
	query := `
		SELECT id, retry_count, last_attempt FROM posts
		WHERE retry_count < ?
	`

	rows, err := db.conn.QueryContext(ctx, query, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to query posts to retry: %w", err)
	}
	defer rows.Close()

	var eligiblePosts []string

	for rows.Next() {
		var postID string
		var retryCount int
		var lastAttempt sql.NullInt64

		if err := rows.Scan(&postID, &retryCount, &lastAttempt); err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}

		// If retry_count is 0, post was never attempted - always eligible
		if retryCount == 0 {
			eligiblePosts = append(eligiblePosts, postID)
			continue
		}

		// If last_attempt is NULL, treat as never attempted
		if !lastAttempt.Valid {
			eligiblePosts = append(eligiblePosts, postID)
			continue
		}

		// Calculate backoff delay: min(backoffBase * 2^retry_count, backoffMax)
		backoffDelay := time.Duration(float64(backoffBase) * math.Pow(2, float64(retryCount)))
		if backoffDelay > backoffMax {
			backoffDelay = backoffMax
		}

		// Check if enough time has passed since last attempt
		lastAttemptTime := time.Unix(lastAttempt.Int64, 0)
		eligibleAt := lastAttemptTime.Add(backoffDelay)

		if time.Now().After(eligibleAt) {
			eligiblePosts = append(eligiblePosts, postID)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating posts: %w", err)
	}

	return eligiblePosts, nil
}
