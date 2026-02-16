package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/user/reddit-media-downloader/internal/reddit"
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
	Logger      *log.Logger
}

type Downloader struct {
	config    Config
	extractor *Extractor
	logger    *log.Logger
}

func NewDownloader(config Config) *Downloader {
	config = applyDefaults(config)

	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: config.Timeout}
	} else if config.HTTPClient.Timeout == 0 {
		config.HTTPClient.Timeout = config.Timeout
	}

	logger := config.Logger
	if logger == nil {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}

	return &Downloader{
		config:    config,
		extractor: NewExtractor(config.HTTPClient, config.UserAgent),
		logger:    logger,
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
			d.logger.Printf("extract failed for post %s: %v", post.ID, err)
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
	if len(items) == 0 {
		return make(map[string]string), nil
	}
	if err := os.MkdirAll(d.config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	group := &errgroup.Group{}
	group.SetLimit(d.config.Concurrency)

	hashes := make(map[string]string)
	var mu sync.Mutex
	var errs []error

	for _, item := range items {
		item := item
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			hash, err := d.downloadItemWithHash(ctx, item)
			if err != nil {
				d.logger.Printf("download failed for post %s (%s): %v", item.PostID, item.URL, err)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return err
			}
			mu.Lock()
			hashes[item.PostID] = hash
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

func (d *Downloader) DownloadPosts(ctx context.Context, posts []reddit.RedditPost) ([]Downloadable, error) {
	items, extractErr := d.Extract(ctx, posts)
	_, downloadErr := d.Download(ctx, items)
	return items, combineErrors(extractErr, downloadErr)
}

func (d *Downloader) downloadItem(ctx context.Context, item Downloadable) error {
	_, err := d.downloadItemWithHash(ctx, item)
	return err
}

func (d *Downloader) downloadItemWithHash(ctx context.Context, item Downloadable) (string, error) {
	if strings.TrimSpace(item.URL) == "" {
		return "", errors.New("download URL is empty")
	}
	if strings.TrimSpace(item.PostID) == "" {
		return "", errors.New("post ID is empty")
	}

	filename := item.Filename
	if filename == "" {
		ext, _, err := extensionAndType(item.URL, "")
		if err != nil {
			return "", fmt.Errorf("detecting extension for %s: %w", item.URL, err)
		}
		filename = fmt.Sprintf("untitled_%s%s", item.PostID, ext)
	}

	subreddit := sanitizeSubreddit(item.Subreddit)
	outputDir := filepath.Join(d.config.OutputDir, subreddit)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("create subreddit directory: %w", err)
	}

	// Check if any file containing this post ID already exists (bdfr-html style matching)
	existingFile := findExistingFile(outputDir, item.PostID)
	if existingFile != "" {
		d.logger.Printf("skip existing file %s", existingFile)
		hash, err := CalculateFileHash(existingFile)
		if err != nil {
			d.logger.Printf("failed to hash existing file %s: %v", existingFile, err)
		}
		return hash, nil
	}

	filePath := filepath.Join(outputDir, filename)
	var lastErr error
	for attempt := 1; attempt <= d.config.Retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Re-check for existing file before each attempt
		existingFile = findExistingFile(outputDir, item.PostID)
		if existingFile != "" {
			d.logger.Printf("skip existing file %s", existingFile)
			hash, err := CalculateFileHash(existingFile)
			if err != nil {
				d.logger.Printf("failed to hash existing file %s: %v", existingFile, err)
			}
			return hash, nil
		}

		err := d.downloadOnce(ctx, item.URL, filePath)
		if err == nil {
			hash, err := CalculateFileHash(filePath)
			if err != nil {
				d.logger.Printf("failed to hash downloaded file %s: %v", filePath, err)
			}
			return hash, nil
		}
		lastErr = err
		if attempt < d.config.Retries {
			if err := sleepWithContext(ctx, d.backoffDuration(attempt)); err != nil {
				return "", err
			}
		}
	}

	return "", fmt.Errorf("download failed after %d attempts: %w", d.config.Retries, lastErr)
}

func (d *Downloader) downloadOnce(ctx context.Context, url, filePath string) error {
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

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(filePath)
			return
		}
		file.Close()
	}()

	if _, err = io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

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

func findExistingFile(dir, postID string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(entry.Name(), postID) {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}
