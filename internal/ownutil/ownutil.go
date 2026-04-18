// Package ownutil provides file ownership utilities for PUID/PGID support.
// When PUID and/or PGID are set to non-zero values, files and directories
// created by the application will be chowned to the specified UID:GID.
package ownutil

import (
	"log/slog"
	"os"
	"path/filepath"
)

// Owner handles file ownership changes based on PUID/PGID configuration.
// A nil Owner means no ownership changes are needed (PUID=0, PGID=0 or unset).
type Owner struct {
	UID int
	GID int
}

// NewOwner creates an Owner from PUID and PGID values.
// Returns nil if both uid and gid are 0 (no ownership change needed).
func NewOwner(uid, gid int) *Owner {
	if uid == 0 && gid == 0 {
		return nil
	}
	return &Owner{UID: uid, GID: gid}
}

// Chown changes the ownership of the file at path to the configured UID:GID.
// If the receiver is nil, this is a no-op. Errors are logged as warnings.
func (o *Owner) Chown(path string, logger *slog.Logger) {
	if o == nil {
		return
	}
	if err := os.Chown(path, o.UID, o.GID); err != nil {
		if logger != nil {
			logger.Warn("failed to chown file", "path", path, "uid", o.UID, "gid", o.GID, "error", err)
		}
	}
}

// ChownDir changes ownership of a directory and all its contents recursively.
// If the receiver is nil, this is a no-op. Errors are logged as warnings.
func (o *Owner) ChownDir(dir string, logger *slog.Logger) {
	if o == nil {
		return
	}
	o.Chown(dir, logger)
	//nolint:errcheck // WalkDir errors are non-fatal; individual chown failures are logged
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // continue walking
		}
		o.Chown(path, logger)
		return nil
	})
}
