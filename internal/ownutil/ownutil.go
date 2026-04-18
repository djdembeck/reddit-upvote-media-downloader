package ownutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Owner manages file ownership changes via PUID/PGID environment variables.
// A nil or zero-valued Owner is a no-op, preserving default container user ownership.
type Owner struct {
	UID int
	GID int
}

// NewOwner creates an Owner with the given UID and GID.
// Returns an error if either value is negative.
func NewOwner(uid, gid int) (*Owner, error) {
	if uid < 0 {
		return nil, fmt.Errorf("PUID must be non-negative, got %d", uid)
	}
	if gid < 0 {
		return nil, fmt.Errorf("PGID must be non-negative, got %d", gid)
	}
	return &Owner{UID: uid, GID: gid}, nil
}

// IsNoOp reports whether the Owner would not change file ownership.
// Returns true for nil receivers and the zero value (0, 0).
func (o *Owner) IsNoOp() bool {
	return o == nil || (o.UID == 0 && o.GID == 0)
}

// Chown changes ownership of the file at path to the Owner's UID/GID using os.Lchown.
// Returns an error if the operation fails.
func (o *Owner) Chown(path string) error {
	if o.IsNoOp() {
		return nil
	}
	if err := os.Lchown(path, o.UID, o.GID); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// ChownDir recursively changes ownership of all files and directories under dir.
// It uses context.Background() internally; use ChownDirContext for cancellation support.
func (o *Owner) ChownDir(dir string, logger *slog.Logger) error {
	return o.ChownDirContext(context.Background(), dir, logger)
}

// ChownDirContext recursively changes ownership under dir, respecting context cancellation.
// Symlinks within the directory tree are skipped to prevent following links outside
// the target tree. Returns the first error encountered during the walk.
func (o *Owner) ChownDirContext(ctx context.Context, dir string, logger *slog.Logger) error {
	if o.IsNoOp() {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("context error: %w", ctx.Err())
	}
	var firstErr error
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		firstErr = o.processChownWalk(ctx, path, d, walkErr, logger)
		return firstErr
	})
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// processChownWalk handles the walk callback logic separately to reduce cyclomatic complexity.
func (o *Owner) processChownWalk(ctx context.Context, path string, d os.DirEntry, walkErr error, logger *slog.Logger) error {
	// Check for context cancellation before any mutation
	if ctx.Err() != nil {
		return fmt.Errorf("context error: %w", ctx.Err())
	}

	if walkErr != nil {
		warnLog(logger, "walkdir error during chown", "path", path, "error", walkErr)
		return walkErr
	}
	if isSymlink(d) {
		return nil
	}
	if err := o.Chown(path); err != nil {
		return err
	}
	return nil
}

// isSymlink returns true if the DirEntry is a symlink.
func isSymlink(d os.DirEntry) bool {
	return d != nil && d.Type()&os.ModeSymlink != 0
}

// ChownMkdirAll creates a directory tree (like os.MkdirAll) and then recursively
// changes ownership. It uses context.Background() for the chown walk; use
// ChownMkdirAllContext for cancellation support.
func (o *Owner) ChownMkdirAll(dir string, perm os.FileMode, logger *slog.Logger) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	if err := o.ChownDir(dir, logger); err != nil {
		return fmt.Errorf("chown directory %s: %w", dir, err)
	}
	return nil
}

// ChownMkdirAllContext creates a directory tree and recursively changes ownership,
// respecting context cancellation.
func (o *Owner) ChownMkdirAllContext(ctx context.Context, dir string, perm os.FileMode, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return fmt.Errorf("context error: %w", ctx.Err())
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("context error: %w", ctx.Err())
	}
	if err := o.ChownDirContext(ctx, dir, logger); err != nil {
		return fmt.Errorf("chown directory %s: %w", dir, err)
	}
	return nil
}

// warnLog logs a warning via the structured logger, falling back to slog.Default()
// if the provided logger is nil.
func warnLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(msg, args...)
}
