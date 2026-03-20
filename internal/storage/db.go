// Package storage provides SQLite database operations for the Reddit Media Downloader.
package storage

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // Required for SQLite driver registration
)

// ErrPostNotFound is returned when a post is not found in the database.
var ErrPostNotFound = errors.New("post not found")

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

// migration statements to add new columns if they don't exist.
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
//
//nolint:cyclop
func (db *DB) runMigrations(ctx context.Context) error {
	// Get existing columns
	rows, err := db.conn.QueryContext(ctx, "PRAGMA table_info(posts)")
	if err != nil {
		return fmt.Errorf("failed to query table info: %w", err)
	}
	defer func() {
		// Ignoring error - rows.Close() in cleanup, read-only operation
		_ = rows.Close() //nolint:errcheck
	}()

	existingColumns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var type_ string
		var notnull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &type_, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("failed to scan table info: %w", err)
		}
		existingColumns[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating table info: %w", err)
	}

	// Add retry_count column if it doesn't exist
	if !existingColumns["retry_count"] {
		if _, err := db.conn.ExecContext(ctx, addRetryCountColumn); err != nil {
			return fmt.Errorf("failed to add retry_count column: %w", err)
		}
	}

	// Add last_error column if it doesn't exist
	if !existingColumns["last_error"] {
		if _, err := db.conn.ExecContext(ctx, addLastErrorColumn); err != nil {
			return fmt.Errorf("failed to add last_error column: %w", err)
		}
	}

	// Add last_attempt column if it doesn't exist
	if !existingColumns["last_attempt"] {
		if _, err := db.conn.ExecContext(ctx, addLastAttemptColumn); err != nil {
			return fmt.Errorf("failed to add last_attempt column: %w", err)
		}
	}

	return nil
}

func NewDB(ctx context.Context, dbPath string) (*DB, error) {
	conn, err := openAndInitializeDB(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	db := &DB{conn: conn}

	if err := db.runMigrations(ctx); err != nil {
		cerr := conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w; close error: %v", err, cerr)
	}

	if err := ensureHashColumn(ctx, conn); err != nil {
		cerr := conn.Close()
		return nil, fmt.Errorf("failed to ensure hash column: %w; close error: %v", err, cerr)
	}

	return db, nil
}

func openAndInitializeDB(ctx context.Context, dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.PingContext(ctx); err != nil {
		cerr := conn.Close()
		return nil, fmt.Errorf("failed to ping database: %w; close error: %v", err, cerr)
	}

	if _, err := conn.ExecContext(ctx, schema); err != nil {
		cerr := conn.Close()
		return nil, fmt.Errorf("failed to create schema: %w; close error: %v", err, cerr)
	}

	return conn, nil
}

// closeConnOnError closes the database connection and logs any error.
func closeConnOnError(conn *sql.DB) {
	if err := conn.Close(); err != nil {
		slog.Error("failed to close database connection", "error", err)
	}
}

// ensureHashColumn adds the hash column and index if they don't exist.
func ensureHashColumn(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, `ALTER TABLE posts ADD COLUMN hash TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			closeConnOnError(conn)
			return fmt.Errorf("failed to add hash column: %w", err)
		}
	}

	if _, err := conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_hash ON posts(hash)`); err != nil {
		closeConnOnError(conn)
		return fmt.Errorf("failed to create hash index: %w", err)
	}

	return nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if db.conn != nil {
		if err := db.conn.Close(); err != nil {
			return fmt.Errorf("close database connection: %w", err)
		}
	}
	return nil
}

// SavePost saves a post to the database. If the post already exists, it updates the record.
// Also saves retry-related fields: retry_count, last_error, last_attempt.
func (db *DB) SavePost(ctx context.Context, post *Post) error {
	query := `
		INSERT INTO posts (
			id, title, subreddit, author, url, permalink, created_at,
			downloaded_at, media_type, file_path, source,
			retry_count, last_error, last_attempt, hash
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			source = excluded.source,
			retry_count = excluded.retry_count,
			last_error = excluded.last_error,
			last_attempt = excluded.last_attempt,
			hash = excluded.hash
	`

	var lastError sql.NullString
	if post.LastError != "" {
		lastError.Valid = true
		lastError.String = post.LastError
	}

	var lastAttempt sql.NullInt64
	if !post.LastAttempt.IsZero() {
		lastAttempt.Valid = true
		lastAttempt.Int64 = post.LastAttempt.Unix()
	}

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
		post.RetryCount,
		lastError,
		lastAttempt,
		post.Hash,
	)

	if err != nil {
		return fmt.Errorf("failed to save post: %w", err)
	}

	return nil
}

// GetPost retrieves a post from the database by ID.
//
//nolint:cyclop,gocyclo
func (db *DB) GetPost(ctx context.Context, id string) (*Post, error) {
	query := `
		SELECT
			id, title, subreddit, author, url, permalink, created_at,
			downloaded_at, media_type, file_path, source,
			retry_count, last_error, last_attempt, hash
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
	var hash sql.NullString

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
		&hash,
	)

	if err == sql.ErrNoRows {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get post: %w", err)
	}

	db.populatePostFromNullFields(&post, title, subreddit, author, url, permalink, mediaType, filePath, source, createdAtUnix, downloadedAtUnix, retryCount, lastError, lastAttempt, hash)

	return &post, nil
}

// populatePostFromNullFields populates a Post from nullable database fields.
//
//nolint:cyclop
func (db *DB) populatePostFromNullFields(post *Post, title, subreddit, author, url, permalink, mediaType, filePath, source sql.NullString, createdAtUnix, downloadedAtUnix, retryCount sql.NullInt64, lastError sql.NullString, lastAttempt sql.NullInt64, hash sql.NullString) {
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
	if hash.Valid {
		post.Hash = hash.String
	}
}

// IsDownloaded checks if a post has been downloaded.
func (db *DB) IsDownloaded(ctx context.Context, id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM posts WHERE id = ?)`

	var exists bool
	err := db.conn.QueryRowContext(ctx, query, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if post exists: %w", err)
	}

	return exists, nil
}

// CheckPostStatus returns detailed status of a post for download eligibility checking.
// It checks file existence on disk (if file_path is set), retry count against threshold,
// and last_attempt against backoff window.
// Parameters:
//   - threshold: max retry count before permanent skip (0 = ignore)
//   - backoffBase: base delay for exponential backoff calculation (0 = ignore)
//   - backoffMax: max delay cap for backoff calculation (0 = ignore)
//
//nolint:cyclop,gocyclo
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

	if db.checkRetryThreshold(status, threshold) {
		return status, nil
	}

	if db.checkFileExists(status) {
		return status, nil
	}

	if db.checkBackoffWindow(status, backoffBase, backoffMax) {
		return status, nil
	}

	status.RetryEligible = true
	return status, nil
}

// checkRetryThreshold checks if retry count exceeds threshold.
func (db *DB) checkRetryThreshold(status *PostStatus, threshold int) bool {
	if threshold > 0 && status.RetryCount >= threshold {
		status.ShouldSkip = true
		status.RetryEligible = false
		return true
	}
	return false
}

// checkFileExists checks if file exists on disk.
func (db *DB) checkFileExists(status *PostStatus) bool {
	if status.FilePath != "" {
		if _, err := os.Stat(status.FilePath); err == nil {
			status.FileExists = true
			status.ShouldSkip = true
			status.RetryEligible = false
			return true
		}
	}
	return false
}

func (db *DB) checkBackoffWindow(status *PostStatus, backoffBase, backoffMax time.Duration) bool {
	if backoffBase > 0 && !status.LastAttempt.IsZero() {
		exponent := status.RetryCount - 1
		if exponent < 0 {
			exponent = 0
		}
		backoffDelay := time.Duration(float64(backoffBase) * math.Pow(2, float64(exponent)))
		if backoffMax > 0 && backoffDelay > backoffMax {
			backoffDelay = backoffMax
		}

		elapsed := time.Since(status.LastAttempt)
		if elapsed < backoffDelay {
			status.ShouldSkip = true
			status.RetryEligible = false
			return true
		}
	}
	return false
}

// HashExists checks if a file hash already exists in the database.
// Returns true if the hash exists, false otherwise, along with any error.
func (db *DB) HashExists(ctx context.Context, hash string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM posts WHERE hash = ?)`

	var exists bool
	err := db.conn.QueryRowContext(ctx, query, hash).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if hash exists: %w", err)
	}

	return exists, nil
}

// GetPostByHash retrieves a post by its hash.
//
//nolint:cyclop
func (db *DB) GetPostByHash(ctx context.Context, hash string) (*Post, error) {
	query := `
		SELECT
			id, title, subreddit, author, url, permalink, created_at,
			downloaded_at, media_type, file_path, source, hash
		FROM posts
		WHERE hash = ?
	`

	row := db.conn.QueryRowContext(ctx, query, hash)

	var post Post
	var title, subreddit, author, url, permalink, mediaType, filePath, source sql.NullString
	var createdAtUnix, downloadedAtUnix sql.NullInt64
	var hashValue sql.NullString

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
		&hashValue,
	)

	if err == sql.ErrNoRows {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get post by hash: %w", err)
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
	if hashValue.Valid {
		post.Hash = hashValue.String
	}

	return &post, nil
}

// GetStats returns download statistics.
func (db *DB) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		PostsBySource:    make(map[string]int64),
		PostsBySubreddit: make(map[string]int64),
		PostsByMediaType: make(map[string]int64),
	}

	row := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts`)
	if err := row.Scan(&stats.TotalPosts); err != nil {
		return nil, fmt.Errorf("failed to get total count: %w", err)
	}

	getCounts := func(query string, setter func(string, int64)) error {
		rows, err := db.conn.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}
		defer func() {
			_ = rows.Close()
		}()

		for rows.Next() {
			var key string
			var count int64
			if err := rows.Scan(&key, &count); err != nil {
				return fmt.Errorf("scan failed: %w", err)
			}
			setter(key, count)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iteration failed: %w", err)
		}
		return nil
	}

	if err := getCounts(`SELECT source, COUNT(*) FROM posts GROUP BY source`,
		func(k string, c int64) { stats.PostsBySource[k] = c }); err != nil {
		return nil, fmt.Errorf("failed to get source counts: %w", err)
	}

	if err := getCounts(`SELECT subreddit, COUNT(*) FROM posts GROUP BY subreddit`,
		func(k string, c int64) { stats.PostsBySubreddit[k] = c }); err != nil {
		return nil, fmt.Errorf("failed to get subreddit counts: %w", err)
	}

	if err := getCounts(`SELECT media_type, COUNT(*) FROM posts GROUP BY media_type`,
		func(k string, c int64) { stats.PostsByMediaType[k] = c }); err != nil {
		return nil, fmt.Errorf("failed to get media type counts: %w", err)
	}

	return stats, nil
}

// ImportFromIDList imports post IDs from an idList.txt file.
// The file format is one post ID per line. Empty lines and comments (starting with #) are ignored.
//
//nolint:cyclop
func (db *DB) ImportFromIDList(ctx context.Context, filePath string) (int, error) {
	//nolint:gosec // G304: intentional file reading from user-provided idList file for migration
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open idList file: %w", err)
	}
	defer func() {
		// Ignoring error - file.Close() in cleanup, read-only operation
		_ = file.Close() //nolint:errcheck
	}()

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

// FilenamePattern matches bdfr-html filenames with POSTID extraction.
// Supports: {POSTID}.ext, {POSTID}_{index}.ext, {title}_{POSTID}.ext, {title}_{index}_{POSTID}.ext
// Examples: abc123.jpg, abc123_1.mp4, My_Post_abc123.jpg, My_Post_1_def456.mp4
// Reddit post IDs are typically 6-7 alphanumeric characters.
var FilenamePattern = regexp.MustCompile(`^(?:.+_)?([a-zA-Z0-9]{6,})(?:_\d+)?\.\w+$`)

// ImportFromDirectory scans a directory for media files and imports post IDs from filenames.
// Supports bdfr-html filename patterns: {title}_{POSTID}.ext, {title}_{index}_{POSTID}.ext
//
//nolint:cyclop
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
		matches := FilenamePattern.FindStringSubmatch(filename)
		if matches == nil {
			// Not a bdfr-html filename pattern
			continue
		}

		postID := matches[1]

		// Validate extension before marking as seen
		ext := strings.ToLower(filepath.Ext(filename))
		mediaType := ""
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
			mediaType = "image"
		case ".mp4", ".webm", ".mov", ".avi", ".mkv":
			mediaType = "video"
		case ".gifv":
			mediaType = "gif"
		default:
			// Skip unsupported extensions
			continue
		}

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
		SELECT
			id, title, subreddit, author, url, permalink, created_at,
			downloaded_at, media_type, file_path, source,
			retry_count, last_error, last_attempt, hash
		FROM posts
	`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all posts: %w", err)
	}
	defer func() {
		_ = rows.Close() //nolint:errcheck
	}()

	var posts []Post

	for rows.Next() {
		post, err := db.scanPostRow(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating posts: %w", err)
	}

	return posts, nil
}

// scanPostRow scans a single row from the posts table into a Post struct.
func (db *DB) scanPostRow(rows *sql.Rows) (Post, error) {
	var post Post
	var title, subreddit, author, url, permalink, mediaType, filePath, source sql.NullString
	var createdAtUnix, downloadedAtUnix sql.NullInt64
	var retryCount sql.NullInt64
	var lastError sql.NullString
	var lastAttempt sql.NullInt64
	var hash sql.NullString

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
		&hash,
	)
	if err != nil {
		return post, fmt.Errorf("failed to scan post: %w", err)
	}

	db.populatePostFromNullFields(&post, title, subreddit, author, url, permalink, mediaType, filePath, source, createdAtUnix, downloadedAtUnix, retryCount, lastError, lastAttempt, hash)

	return post, nil
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

// DeletePost removes a post from the database.
// Returns nil if the post doesn't exist (idempotent).
func (db *DB) DeletePost(ctx context.Context, id string) error {
	query := `DELETE FROM posts WHERE id = ?`

	result, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete post: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	// Return nil if post doesn't exist (idempotent)
	if rowsAffected == 0 {
		return nil
	}

	return nil
}

// GetPostsToRetry returns post IDs that are eligible for retry based on backoff settings.
// It considers posts where:
// - retry_count < threshold (not permanently skipped)
// - Either retry_count == 0 (never tried) OR enough time has passed since last_attempt
// backoffDelay = min(backoffBase * 2^(retryCount-1), backoffMax)
//
//nolint:cyclop // complexity required for backoff calculation and eligibility logic
func (db *DB) GetPostsToRetry(ctx context.Context, backoffBase, backoffMax time.Duration, threshold int) ([]string, error) {
	query := `
		SELECT id, retry_count, last_attempt FROM posts
		WHERE retry_count < ?
	`

	rows, err := db.conn.QueryContext(ctx, query, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to query posts to retry: %w", err)
	}
	defer func() {
		_ = rows.Close() //nolint:errcheck
	}()

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

		// Calculate backoff delay: min(backoffBase * 2^(retryCount-1), backoffMax)
		backoffRetryCount := retryCount - 1
		if backoffRetryCount < 0 {
			backoffRetryCount = 0
		}
		backoffDelay := time.Duration(float64(backoffBase) * math.Pow(2, float64(backoffRetryCount)))
		if backoffMax > 0 && backoffDelay > backoffMax {
			backoffDelay = backoffMax
		}

		// Check if enough time has passed since last attempt
		lastAttemptTime := time.Unix(lastAttempt.Int64, 0)
		elapsed := time.Since(lastAttemptTime)

		// Post is eligible if elapsed time is >= backoff delay
		// Add 1 second margin to account for Unix timestamp precision loss
		if elapsed >= backoffDelay+time.Second {
			eligiblePosts = append(eligiblePosts, postID)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating posts: %w", err)
	}

	return eligiblePosts, nil
}
