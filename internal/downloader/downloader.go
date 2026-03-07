package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"golang.org/x/sync/errgroup"
)

const (
	defaultConcurrency = 10
	defaultRetries     = 3
	defaultTimeout     = 30 * time.Second
	defaultOutputDir   = "output"
	defaultBackoffBase = 500 * time.Millisecond
)

type Config struct {
	OutputDir   string
	Concurrency int
	Timeout     time.Duration
	Retries     int
	BackoffBase time.Duration
	UserAgent   string
	HTTPClient  *http.Client
	Logger      *slog.Logger
}

type Downloader struct {
	config    Config
	extractor *Extractor
	logger    *slog.Logger
	db        *storage.DB
}

func NewDownloader(config Config, db *storage.DB) *Downloader {
	config = applyDefaults(config)

	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: config.Timeout}
	} else if config.HTTPClient.Timeout == 0 {
		config.HTTPClient.Timeout = config.Timeout
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	return &Downloader{
		config:    config,
		extractor: NewExtractorWithLogger(config.HTTPClient, config.UserAgent, logger),
		logger:    logger,
		db:        db,
	}
}

func (d *Downloader) Extract(ctx context.Context, posts []reddit.RedditPost) ([]Downloadable, error) {
	items := make([]Downloadable, 0)
	var errs []error

	for _, post := range posts {
		if err := ctx.Err(); err != nil {
			return items, err
		}

		extracted, err := d.extractor.Extract(ctx, post)
		if err != nil {
			d.logger.Error("extract failed", "post_id", post.ID, "error", err)
			errs = append(errs, fmt.Errorf("extract post %s: %w", post.ID, err))
			continue
		}
		items = append(items, extracted...)
	}

	if len(errs) > 0 {
		return items, joinErrors("extract errors", errs)
	}

	return items, nil
}

func (d *Downloader) Download(ctx context.Context, items []Downloadable) (map[string]string, error) {
	hashes := make(map[string]string)

	if len(items) == 0 {
		return hashes, nil
	}
	if err := os.MkdirAll(d.config.OutputDir, 0755); err != nil {
		return hashes, fmt.Errorf("create output directory: %w", err)
	}

	group := &errgroup.Group{}
	group.SetLimit(d.config.Concurrency)

	var mu sync.Mutex
	var errs []error

	for _, item := range items {
		item := item
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			hash, isDuplicate, err := d.downloadItem(ctx, item)
			if err != nil {
				d.logger.Error("download failed", "post_id", item.PostID, "url", item.URL, "error", err)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return err
			}
			// Record the post as handled, even if it's a duplicate
			// For duplicates, we'll store the hash with a sentinel marker
			mu.Lock()
			if hash != "" && isDuplicate {
				// For duplicates, send a sentinel value that indicates duplicate
				// Use a special hash prefix to mark duplicates
				hashes[item.PostID] = "DUPLICATE:" + hash
			} else if hash != "" {
				hashes[item.PostID] = hash
			}
			mu.Unlock()
			return nil
		})
	}

	groupErr := group.Wait()
	if len(errs) > 0 {
		return hashes, joinErrors("download errors", errs)
	}
	if groupErr != nil {
		return hashes, groupErr
	}

	return hashes, nil
}

func (d *Downloader) DownloadPosts(ctx context.Context, posts []reddit.RedditPost) ([]Downloadable, map[string]string, error) {
	items, extractErr := d.Extract(ctx, posts)
	hashes, downloadErr := d.Download(ctx, items)
	return items, hashes, combineErrors(extractErr, downloadErr)
}

func (d *Downloader) downloadItem(ctx context.Context, item Downloadable) (string, bool, error) {
	if strings.TrimSpace(item.URL) == "" {
		return "", false, errors.New("download URL is empty")
	}
	if strings.TrimSpace(item.PostID) == "" {
		return "", false, errors.New("post ID is empty")
	}

	filename := item.Filename
	if filename == "" {
		ext, _, err := extensionAndType(item.URL, "")
		if err != nil {
			return "", false, fmt.Errorf("detect extension for %s: %w", item.URL, err)
		}
		filename = fmt.Sprintf("untitled_%s%s", item.PostID, ext)
	}

	subreddit := sanitizeSubreddit(item.Subreddit)
	outputDir := filepath.Join(d.config.OutputDir, subreddit)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", false, fmt.Errorf("create subreddit directory: %w", err)
	}

	// Check if any file containing this post ID already exists (bdfr-html style matching)
	hash, isLocalReuse, err := d.checkAndHandleExistingFile(outputDir, item.PostID)
	if err != nil {
		return "", false, err
	}
	if isLocalReuse {
		// Local file reuse is not a DB duplicate, so return false for isDuplicate
		return hash, false, nil
	}

	filePath := filepath.Join(outputDir, filename)
	var lastErr error
	for attempt := 1; attempt <= d.config.Retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		// Re-check for existing file before each attempt
		hash, isLocalReuse, err = d.checkAndHandleExistingFile(outputDir, item.PostID)
		if err != nil {
			return "", false, err
		}
		if isLocalReuse {
			return hash, false, nil
		}

		expectedExt := filepath.Ext(filename)
		err = d.downloadOnce(ctx, item.URL, filePath, expectedExt, item.PostID)
		if err == nil {
			// Download succeeded, now calculate hash and check for duplicates
			hash, hashErr := CalculateFileHash(filePath)
			if hashErr != nil {
				d.logger.Error("error calculating hash", "path", filePath, "error", hashErr)
				if removeErr := os.Remove(filePath); removeErr != nil {
					d.logger.Warn("failed to remove file", "path", filePath, "error", removeErr)
				}
				return "", false, fmt.Errorf("calculate hash: %w", hashErr)
			}

			// Check if hash already exists in database.
			// Note: There's a small race window where concurrent downloads of the same
			// content could both pass this check before either saves to the database.
			// This is acceptable for a media downloader given the low probability.
			if d.db != nil {
				exists, dbErr := d.db.HashExists(ctx, hash)
				if dbErr != nil {
					d.logger.Error("error checking hash in database", "error", dbErr)
					return "", false, fmt.Errorf("check hash exists: %w", dbErr)
				}
				if exists {
					d.logger.Info("skip duplicate hash", "hash", hash, "post_id", item.PostID)
					if removeErr := os.Remove(filePath); removeErr != nil {
						d.logger.Warn("failed to remove file", "path", filePath, "error", removeErr)
					}
					return hash, true, nil
				}
			}

			return hash, false, nil
		}
		lastErr = err
		// Don't retry on permanent validation errors
		var validationErr ValidationError
		if errors.As(err, &validationErr) && validationErr.Permanent {
			break
		}
		if attempt < d.config.Retries {
			if err := sleepWithContext(ctx, d.backoffDuration(attempt)); err != nil {
				return "", false, err
			}
		}
	}

	return "", false, fmt.Errorf("download failed after %d attempts: %w", d.config.Retries, lastErr)
}
func (d *Downloader) downloadOnce(ctx context.Context, url, filePath, expectedExt, postID string) (err error) {
	reqCtx, cancel := context.WithTimeout(ctx, d.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", d.config.UserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := d.config.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	if resp.ContentLength > 0 && resp.ContentLength < 1024 {
		return ValidationError{
			Permanent: true,
			Reason:    fmt.Sprintf("file too small (%d bytes)", resp.ContentLength),
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/html") {
		return ValidationError{
			Permanent: true,
			Reason:    "server returned HTML content-type",
		}
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			_, isLocalReuse, validateErr := d.checkAndHandleExistingFile(filepath.Dir(filePath), postID)
			if validateErr != nil {
				return fmt.Errorf("existing file validation failed: %w", validateErr)
			}
			if !isLocalReuse {
				return errors.New("existing file was corrupt and removed, retry needed")
			}
			return nil
		}
		return fmt.Errorf("create file: %w", err)
	}

	// Deferred cleanup: always close file, remove on failure
	success := false
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if err == nil {
				err = fmt.Errorf("close file: %w", closeErr)
			}
			success = false
		}
		if !success {
			if removeErr := os.Remove(filePath); removeErr != nil {
				d.logger.Warn("failed to remove partial file", "path", filePath, "error", removeErr)
			}
		}
	}()

	buf := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, buf)
	if err == io.EOF || (err == nil && n == 0) {
		return ValidationError{
			Permanent: true,
			Reason:    "empty response body",
		}
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("read response body: %w", err)
	}

	if validationErr := validateMagicBytes(buf[:n], expectedExt); validationErr != nil {
		return ValidationError{
			Permanent: true,
			Reason:    fmt.Sprintf("invalid magic bytes: %v", validationErr),
		}
	}

	if isHTMLContent(buf[:n]) {
		return ValidationError{
			Permanent: true,
			Reason:    "content is HTML, not media",
		}
	}

	written, writeErr := file.Write(buf[:n])
	if writeErr != nil {
		return fmt.Errorf("write buffered content: %w", writeErr)
	}

	copied, copyErr := io.Copy(file, resp.Body)
	if copyErr != nil {
		return fmt.Errorf("write file: %w", copyErr)
	}

	totalBytes := int64(written) + copied
	if totalBytes < 1024 {
		return ValidationError{
			Permanent: true,
			Reason:    fmt.Sprintf("file too small (%d bytes)", totalBytes),
		}
	}

	success = true
	return nil
}

func (d *Downloader) backoffDuration(attempt int) time.Duration {
	if attempt <= 0 {
		return d.config.BackoffBase
	}
	return d.config.BackoffBase * time.Duration(1<<uint(attempt-1))
}

func applyDefaults(config Config) Config {
	if config.OutputDir == "" {
		config.OutputDir = defaultOutputDir
	}
	if config.Concurrency <= 0 {
		config.Concurrency = defaultConcurrency
	}
	if config.Retries <= 0 {
		config.Retries = defaultRetries
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.BackoffBase <= 0 {
		config.BackoffBase = defaultBackoffBase
	}
	if config.UserAgent == "" {
		config.UserAgent = defaultUserAgent
	}
	return config
}

func sanitizeSubreddit(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "unknown"
	}

	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, trimmed)

	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		return "unknown"
	}
	return sanitized
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}

func joinErrors(prefix string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	var builder strings.Builder
	builder.WriteString(prefix)
	for _, err := range errs {
		builder.WriteString("\n - ")
		builder.WriteString(err.Error())
	}

	return errors.New(builder.String())
}

func combineErrors(errs ...error) error {
	var combined []error
	for _, err := range errs {
		if err != nil {
			combined = append(combined, err)
		}
	}
	if len(combined) == 0 {
		return nil
	}
	if len(combined) == 1 {
		return combined[0]
	}

	return joinErrors("multiple errors", combined)
}

func (d *Downloader) checkAndHandleExistingFile(outputDir, postID string) (hash string, isLocalReuse bool, err error) {
	existingFile := findExistingFile(outputDir, postID)
	if existingFile == "" {
		return "", false, nil
	}

	ext := filepath.Ext(existingFile)
	if validateErr := validateExistingFile(existingFile, ext); validateErr != nil {
		// Only delete on permanent validation errors (size, magic, HTML)
		// Transient I/O errors should not cause deletion
		var validationErr ValidationError
		if errors.As(validateErr, &validationErr) && validationErr.Permanent {
			d.logger.Warn("existing file is corrupt, re-downloading",
				"path", existingFile, "error", validateErr)
			if removeErr := os.Remove(existingFile); removeErr != nil {
				d.logger.Error("failed to remove corrupt file",
					"path", existingFile, "error", removeErr)
				return "", false, fmt.Errorf("failed to remove corrupt file %s: %w", existingFile, removeErr)
			}
			return "", false, nil
		}
		// Transient I/O errors - return wrapped error without deleting
		return "", false, fmt.Errorf("failed to validate existing file %s: %w", existingFile, validateErr)
	}

	d.logger.Info("skip existing file", "path", existingFile)
	hash, err = CalculateFileHash(existingFile)
	if err != nil {
		d.logger.Error("failed to hash existing file", "path", existingFile, "error", err)
		return "", false, err
	}
	return hash, true, nil
}

func validateExistingFile(filePath, ext string) (err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", filePath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close %s: %w", filePath, closeErr)
		}
	}()

	// Get file info for size check
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", filePath, err)
	}

	// Check minimum size
	if sizeErr := validateMinimumSize(info.Size()); sizeErr != nil {
		return ValidationError{
			Permanent: true,
			Reason:    sizeErr.Error(),
		}
	}

	// Read first 512 bytes for magic byte check
	buf := make([]byte, 512)
	n, readErr := io.ReadFull(file, buf)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return fmt.Errorf("failed to read %s: %w", filePath, readErr)
	}

	// Validate magic bytes
	if magicErr := validateMagicBytes(buf[:n], ext); magicErr != nil {
		return ValidationError{
			Permanent: true,
			Reason:    fmt.Sprintf("invalid magic bytes: %v", magicErr),
		}
	}

	// Check for HTML
	if isHTMLContent(buf[:n]) {
		return ValidationError{
			Permanent: true,
			Reason:    "file contains HTML",
		}
	}

	return nil
}

func findExistingFile(dir, postID string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		matches := storage.FilenamePattern.FindStringSubmatch(filename)
		if matches != nil && strings.ToLower(matches[1]) == strings.ToLower(postID) {
			return filepath.Join(dir, filename)
		}
	}
	return ""
}
