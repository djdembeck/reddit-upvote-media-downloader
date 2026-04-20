package migration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeebo/blake3"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/ownutil"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

func contextChecker(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
		return nil
	}
}

// Migrator handles file reorganization from flat to subreddit-based structure.
//
//nolint:fieldalignment // Internal struct.
type Migrator struct {
	Log        *Log
	DB         *storage.DB
	PostMap    map[string]PostInfo
	seenHashes map[string]FileHashInfo
	SourceDir  string
	DestDir    string
	DryRun     bool
	Owner      *ownutil.Owner
	logger     *slog.Logger
}

// FileHashInfo tracks file hash information for duplicate detection.
//
//nolint:fieldalignment // Internal struct.
type FileHashInfo struct {
	Timestamp  time.Time
	PostID     string
	SourcePath string
}

// NewMigrator creates a new Migrator instance.
func NewMigrator(sourceDir, destDir string, postMap map[string]PostInfo, dryRun bool, db *storage.DB, owner *ownutil.Owner, logger *slog.Logger) *Migrator {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Migrator{
		SourceDir: sourceDir,
		DestDir:   destDir,
		PostMap:   postMap,
		DryRun:    dryRun,
		DB:        db,
		Owner:     owner,
		logger:    logger,
		Log: &Log{
			Version:    "1.0",
			Timestamp:  time.Now(),
			SourceDir:  sourceDir,
			DestDir:    destDir,
			Operations: []Record{},
		},
		seenHashes: make(map[string]FileHashInfo),
	}
	return m
}

// LoadExistingLog populates seenHashes from an existing migration log for idempotent re-runs.
func (m *Migrator) LoadExistingLog(ctx context.Context, logPath string) error {
	if err := contextChecker(ctx); err != nil {
		return err
	}

	//nolint:gosec // G304: intentional file reading from user-provided path
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing log, first run
		}
		return fmt.Errorf("read existing log: %w", err)
	}

	var existingLog Log
	if err := json.Unmarshal(data, &existingLog); err != nil {
		return fmt.Errorf("parse existing log: %w", err)
	}

	for _, op := range existingLog.Operations {
		if op.Hash != "" && (op.Status == StatusMoved || op.Status == StatusMovedWithWarning) {
			m.seenHashes[op.Hash] = FileHashInfo{
				PostID:     op.PostID,
				SourcePath: op.SourcePath,
				Timestamp:  op.Timestamp,
			}
		}
	}

	return nil
}

// shouldLogProgress determines if progress should be logged for the given file index.
// Logs on first file, every 100th file, and the last file.
func shouldLogProgress(i, total int) bool {
	if total == 0 {
		return false
	}
	return (i+1)%100 == 0 || i == 0 || i == total-1
}

// Execute runs the migration process.
//
//nolint:cyclop
func (m *Migrator) Execute(ctx context.Context) error {
	if err := contextChecker(ctx); err != nil {
		return err
	}

	entries, err := os.ReadDir(m.SourceDir)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}

	// Collect file info for sorting by modification time
	//nolint:fieldalignment // Internal local struct.
	type fileEntry struct {
		modTime time.Time
		name    string
	}
	files := make([]fileEntry, 0, len(entries))
	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".html") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			m.Log.TotalFiles++
			m.recordError(entry.Name(), "", "stat_file", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("stat file %s: %w", entry.Name(), err)
			}
			continue
		}
		files = append(files, fileEntry{
			name:    entry.Name(),
			modTime: info.ModTime(),
		})
	}

	// Sort by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	total := len(files)
	for i, f := range files {
		if err := contextChecker(ctx); err != nil {
			return err
		}
		if shouldLogProgress(i, total) {
			slog.Info("Processing file", "current", i+1, "total", total, "filename", f.name)
		}
		if err := m.processFile(ctx, f.name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// checkHashDuplicate checks if the file hash is a duplicate (either in memory or database).
// Returns true if duplicate, false otherwise. Returns error if DB check fails.
func (m *Migrator) checkHashDuplicate(ctx context.Context, fileHash, sourcePath, _ string) (bool, error) {
	// Check if hash already seen (duplicate detection) - includes idempotent re-run check
	if _, hashSeen := m.seenHashes[fileHash]; hashSeen {
		return true, nil
	}

	// Check if hash exists in database (if DB is available and not dry-run)
	if m.DB != nil && !m.DryRun {
		exists, dbErr := m.DB.HashExists(ctx, fileHash)
		if dbErr != nil {
			return false, fmt.Errorf("check hash exists for %s: %w", sourcePath, dbErr)
		}
		if exists {
			return true, nil
		}
	}

	return false, nil
}

// saveMigratedPost saves the migrated post to the database.
func (m *Migrator) saveMigratedPost(ctx context.Context, postID, filename string, postInfo PostInfo, fileInfo os.FileInfo, fileHash string) error {
	// Detect media type from file extension
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

	// Use parsed metadata with fallbacks for empty values
	subreddit := postInfo.Subreddit
	if subreddit == "" {
		subreddit = "migrated"
	}
	author := postInfo.Username
	if author == "" {
		author = UnknownSubreddit
	}

	destPath := m.buildDestPath(filename, postInfo)

	post := &storage.Post{
		ID:           postID,
		Title:        "Migrated from bdfr-html",
		Subreddit:    subreddit,
		Author:       author,
		URL:          "",
		Permalink:    "",
		CreatedAt:    fileInfo.ModTime(),
		DownloadedAt: time.Now(),
		MediaType:    mediaType,
		FilePath:     destPath,
		Source:       "migrated",
		Hash:         fileHash,
	}

	if saveErr := m.DB.SavePost(ctx, post); saveErr != nil {
		return fmt.Errorf("save post to db: %w", saveErr)
	}

	return nil
}

//nolint:cyclop
func (m *Migrator) processFile(ctx context.Context, filename string) error {
	m.Log.TotalFiles++

	// Extract POSTID
	postID, err := ExtractPostID(filename)
	if err != nil {
		m.recordError(filename, "", "extract_postid", err)
		return fmt.Errorf("extract postid from %s: %w", filename, err)
	}

	// Lookup in PostMap
	postInfo, exists := m.PostMap[postID]
	if !exists {
		postInfo = PostInfo{
			Subreddit:  UnknownSubreddit,
			Username:   "",
			IsUserPost: false,
		}
	}

	// Build destination
	destPath := m.buildDestPath(filename, postInfo)
	sourcePath := filepath.Join(m.SourceDir, filename)

	// Get file info
	fileInfo, err := os.Stat(sourcePath)
	if err != nil {
		m.recordError(filename, postID, "stat_file", err)
		return fmt.Errorf("stat file %s: %w", sourcePath, err)
	}

	// Calculate hash for duplicate detection
	fileHash, err := calculateHash(sourcePath)
	if err != nil {
		m.recordError(filename, postID, "calculate_hash", err)
		return fmt.Errorf("calculate hash for %s: %w", sourcePath, err)
	}

	// Check if hash is a duplicate
	isDuplicate, checkErr := m.checkHashDuplicate(ctx, fileHash, sourcePath, postID)
	if checkErr != nil {
		m.recordError(filename, postID, "check_hash_exists", checkErr)
		return checkErr
	}
	if isDuplicate {
		if _, hashSeen := m.seenHashes[fileHash]; hashSeen {
			existingInfo := m.seenHashes[fileHash]
			m.recordSkipped(filename, postID, fmt.Sprintf("duplicate hash (first seen: %s)", existingInfo.SourcePath))
		} else {
			m.recordSkipped(filename, postID, "duplicate hash (exists in database)")
		}
		return nil
	}

	// Check if destination exists
	if _, err := os.Stat(destPath); err == nil {
		// Mark the hash as seen so subsequent duplicates are detected in-memory
		m.seenHashes[fileHash] = FileHashInfo{SourcePath: sourcePath, PostID: postID}
		m.recordSkipped(filename, postID, "destination already exists")
		return nil
	} else if !os.IsNotExist(err) {
		m.recordError(filename, postID, "stat destination", err)
		return fmt.Errorf("stat destination %s: %w", destPath, err)
	}

	if m.DryRun {
		m.recordDryRun(filename, postID, destPath, postInfo, fileInfo.Size(), fileHash)
		return nil
	}

	// Move file
	if err := m.moveFile(ctx, sourcePath, destPath); err != nil {
		m.recordError(filename, postID, "move_file", err)
		return fmt.Errorf("move file %s to %s: %w", sourcePath, destPath, err)
	}

	// Save post to database (if DB is available and not dry-run)
	if m.DB != nil && !m.DryRun {
		if saveErr := m.saveMigratedPost(ctx, postID, filename, postInfo, fileInfo, fileHash); saveErr != nil {
			m.recordSuccessWithWarning(
				filename,
				postID,
				destPath,
				postInfo,
				fileInfo.Size(),
				fileHash,
				fmt.Errorf("save post to db: %w", saveErr),
			)
			m.seenHashes[fileHash] = FileHashInfo{
				PostID:     postID,
				SourcePath: sourcePath,
				Timestamp:  time.Now(),
			}
			return saveErr
		}
	}

	m.seenHashes[fileHash] = FileHashInfo{
		PostID:     postID,
		SourcePath: sourcePath,
		Timestamp:  time.Now(),
	}

	m.recordSuccess(filename, postID, destPath, postInfo, fileInfo.Size(), fileHash)
	return nil
}

func (m *Migrator) buildDestPath(filename string, info PostInfo) string {
	var subdir string
	switch {
	case info.IsUserPost && info.Username != "":
		subdir = filepath.Join("users", info.Username)
	case info.Subreddit != "":
		subdir = SanitizePath(info.Subreddit)
	default:
		subdir = UnknownSubreddit
	}
	return filepath.Join(m.DestDir, subdir, filename)
}

// wrapWithCleanup wraps an error with context and attempts to clean up the destination file.
// It returns the wrapped error after attempting to remove dst (ignoring os.IsNotExist).
func wrapWithCleanup(err error, dst, contextFmt string, args ...any) error {
	contextMsg := fmt.Sprintf(contextFmt, args...)
	wrappedErr := fmt.Errorf("%s: %w", contextMsg, err)
	if removeErr := os.Remove(dst); removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("%w; cleanup also failed: %v", wrappedErr, removeErr)
	}
	return wrappedErr
}

func (m *Migrator) moveFile(ctx context.Context, src, dst string) error {
	dir := filepath.Dir(dst)
	if err := m.Owner.ChownMkdirAllContext(ctx, dir, 0750, m.logger); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := copyFile(src, dst, m.Owner); err != nil {
		return wrapWithCleanup(err, dst, "copy file")
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return wrapWithCleanup(err, dst, "stat source file %s", src)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return wrapWithCleanup(err, dst, "stat destination file %s", dst)
	}
	if srcInfo.Size() != dstInfo.Size() {
		return wrapWithCleanup(
			fmt.Errorf("size mismatch"),
			dst,
			"size mismatch after copy: expected %d, got %d",
			srcInfo.Size(),
			dstInfo.Size(),
		)
	}

	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source %s: %w", src, err)
	}

	return nil
}

func copyFile(src, dst string, owner *ownutil.Owner) (err error) {
	//nolint:gosec // G304: intentional file reading from user-provided migration paths
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer func() {
		if cerr := sourceFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close source %s: %w", src, cerr)
		}
	}()

	//nolint:gosec // G304: intentional file creation at user-provided destination
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dest %s: %w", dst, err)
	}
	defer func() {
		if cerr := destFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close dest %s: %w", dst, cerr)
		}
	}()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("copy %s->%s: %w", src, dst, err)
	}

	if err := destFile.Sync(); err != nil {
		return fmt.Errorf("sync dest %s: %w", dst, err)
	}

	if chownErr := owner.Chown(dst); chownErr != nil {
		return fmt.Errorf("chown %s: %w", dst, chownErr)
	}

	return nil
}

// SaveLog saves the migration log to a JSON file for audit and rollback purposes.
func (m *Migrator) SaveLog(ctx context.Context, logPath string) (err error) {
	if err := contextChecker(ctx); err != nil {
		return err
	}

	//nolint:gosec // G304: intentional log file creation at user-provided path
	file, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log file %s: %w", logPath, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close log file %s: %w", logPath, cerr)
		}
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(m.Log); err != nil {
		return fmt.Errorf("encode log to %s: %w", logPath, err)
	}

	if chownErr := m.Owner.Chown(logPath); chownErr != nil {
		m.logger.Warn("failed to chown log file", "path", logPath, "error", chownErr)
	}

	return nil
}

// Recording methods.
func (m *Migrator) recordSuccess(filename, postID, destPath string, info PostInfo, size int64, hash string) {
	m.Log.Operations = append(m.Log.Operations, Record{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		DestPath:   destPath,
		Subreddit:  info.Subreddit,
		Username:   info.Username,
		IsUserPost: info.IsUserPost,
		Status:     "moved",
		Timestamp:  time.Now(),
		FileSize:   size,
		Hash:       hash,
	})
	m.Log.MovedCount++
}

func (m *Migrator) recordSkipped(filename, postID, reason string) {
	m.Log.Operations = append(m.Log.Operations, Record{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		Status:     "skipped",
		Error:      reason,
		Timestamp:  time.Now(),
	})
	m.Log.SkippedCount++
}

func (m *Migrator) recordError(filename, postID, operation string, err error) {
	m.Log.Operations = append(m.Log.Operations, Record{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		Status:     "error",
		Error:      fmt.Sprintf("%s: %v", operation, err),
		Timestamp:  time.Now(),
	})
	m.Log.ErrorCount++
}

func (m *Migrator) recordDryRun(filename, postID, destPath string, info PostInfo, size int64, hash string) {
	m.Log.Operations = append(m.Log.Operations, Record{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		DestPath:   destPath,
		Subreddit:  info.Subreddit,
		Username:   info.Username,
		IsUserPost: info.IsUserPost,
		Status:     "dry_run",
		Timestamp:  time.Now(),
		FileSize:   size,
		Hash:       hash,
	})
}

func (m *Migrator) recordSuccessWithWarning(
	filename,
	postID,
	destPath string,
	info PostInfo,
	size int64,
	hash string,
	warnErr error,
) {
	m.Log.Operations = append(m.Log.Operations, Record{
		PostID:     postID,
		SourcePath: filepath.Join(m.SourceDir, filename),
		DestPath:   destPath,
		Subreddit:  info.Subreddit,
		Username:   info.Username,
		IsUserPost: info.IsUserPost,
		Status:     "moved_with_warning",
		Error:      fmt.Sprintf("warning: %v", warnErr),
		Timestamp:  time.Now(),
		FileSize:   size,
		Hash:       hash,
	})
	m.Log.MovedCount++
	m.Log.WarningCount++
}

// calculateHash computes BLAKE3 hash of a file.
func calculateHash(filePath string) (string, error) {
	//nolint:gosec // G304: filePath is validated by caller (processFile) before calling
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filePath, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close hash file: %w", cerr)
		}
	}()

	hash := blake3.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hashing %s: %w", filePath, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
