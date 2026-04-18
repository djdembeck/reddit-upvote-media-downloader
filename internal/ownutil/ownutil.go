package ownutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type Owner struct {
	UID int
	GID int
}

func NewOwner(uid, gid int) (*Owner, error) {
	if uid < 0 {
		return nil, fmt.Errorf("PUID must be non-negative, got %d", uid)
	}
	if gid < 0 {
		return nil, fmt.Errorf("PGID must be non-negative, got %d", gid)
	}
	return &Owner{UID: uid, GID: gid}, nil
}

func (o *Owner) IsNoOp() bool {
	return o == nil || (o.UID == 0 && o.GID == 0)
}

func (o *Owner) Chown(path string, logger *slog.Logger) {
	if o.IsNoOp() {
		return
	}
	if err := os.Lchown(path, o.UID, o.GID); err != nil {
		if logger != nil {
			logger.Warn("failed to chown file", "path", path, "uid", o.UID, "gid", o.GID, "error", err)
		}
	}
}

func (o *Owner) ChownDir(dir string, logger *slog.Logger) {
	o.ChownDirContext(context.Background(), dir, logger)
}

func (o *Owner) ChownDirContext(ctx context.Context, dir string, logger *slog.Logger) {
	if o.IsNoOp() {
		return
	}
	//nolint:errcheck
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if logger != nil {
				logger.Warn("walkdir error during chown", "path", path, "error", err)
			}
			return nil
		}
		o.Chown(path, logger)
		return ctx.Err()
	})
}

func (o *Owner) ChownMkdirAll(dir string, perm os.FileMode, logger *slog.Logger) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	o.ChownDir(dir, logger)
	return nil
}
