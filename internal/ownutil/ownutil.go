//go:build !windows

package ownutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Owner holds UID and GID.
type Owner struct {
	uid int
	gid int
}

// GetUID returns the configured UID. Returns 0 for nil Owner.
func (o *Owner) GetUID() int {
	if o == nil {
		return 0
	}
	return o.uid
}

// GetGID returns the configured GID. Returns 0 for nil Owner.
func (o *Owner) GetGID() int {
	if o == nil {
		return 0
	}
	return o.gid
}

// chownUID returns the UID to pass to os.Lchown, or -1 to leave it unchanged.
func (o *Owner) chownUID() int {
	if o == nil || o.uid == 0 {
		return -1
	}
	return o.uid
}

// chownGID returns the GID to pass to os.Lchown, or -1 to leave it unchanged.
func (o *Owner) chownGID() int {
	if o == nil || o.gid == 0 {
		return -1
	}
	return o.gid
}

// NewOwner creates Owner from uid and gid.
//
// The non-negative UID/GID checks here are intentionally duplicated as
// defense-in-depth because [config.validateStorageSettings] already validates
// these values earlier in the configuration loading process. This redundancy
// ensures that even if the config validation is bypassed, the Owner creation
// will catch invalid values.
func NewOwner(uid, gid int) (*Owner, error) {
	if uid < 0 {
		return nil, fmt.Errorf("PUID must be non-negative, got %d", uid)
	}
	if gid < 0 {
		return nil, fmt.Errorf("PGID must be non-negative, got %d", gid)
	}
	return &Owner{uid: uid, gid: gid}, nil
}

// IsNoOp returns true if o is nil or has zero uid/gid.
func (o *Owner) IsNoOp() bool {
	return o == nil || (o.uid == 0 && o.gid == 0)
}

// Chown changes ownership of path.
func (o *Owner) Chown(path string) error {
	if o.IsNoOp() {
		return nil
	}
	if err := os.Lchown(path, o.chownUID(), o.chownGID()); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// ChownDir recursively changes ownership under dir, respecting ctx.
func (o *Owner) ChownDir(dir string, logger *slog.Logger) error {
	return o.ChownDirContext(context.Background(), dir, logger)
}

// ChownDirContext recursively changes ownership under dir, with ctx.
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

// processChownWalk processes each filepath.WalkDir entry.
func (o *Owner) processChownWalk(ctx context.Context, path string, _ os.DirEntry, walkErr error, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return fmt.Errorf("context error: %w", ctx.Err())
	}
	if walkErr != nil {
		warnLog(logger, "walkdir error during chown", "path", path, "error", walkErr)
		return walkErr
	}
	if err := o.Chown(path); err != nil {
		return err
	}
	return nil
}

// ChownMkdirAll runs MkdirAll then changes ownership.
func (o *Owner) ChownMkdirAll(dir string, perm os.FileMode, logger *slog.Logger) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	if err := o.ChownDir(dir, logger); err != nil {
		return fmt.Errorf("chown directory %s: %w", dir, err)
	}
	return nil
}

// ChownMkdirAllContext runs MkdirAll then changes ownership with context.
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

func warnLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(msg, args...)
}
