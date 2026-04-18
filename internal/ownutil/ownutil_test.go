package ownutil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNewOwner(t *testing.T) {
	tests := []struct {
		name     string
		uid      int
		gid      int
		wantErr  bool
		wantNoOp bool
		wantUID  int
		wantGID  int
	}{
		{"Zero values", 0, 0, false, true, 0, 0},
		{"Non-zero values", 1000, 1000, false, false, 1000, 1000},
		{"Partial PUID", 1000, 0, false, false, 1000, 0},
		{"Partial PGID", 0, 1000, false, false, 0, 1000},
		{"Negative PUID", -1, 0, true, false, 0, 0},
		{"Negative PGID", 0, -1, true, false, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := NewOwner(tt.uid, tt.gid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewOwner(%d,%d) error = %v, wantErr %v", tt.uid, tt.gid, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if o == nil {
				t.Fatal("NewOwner returned nil, expected valid *Owner")
			}
			if o.IsNoOp() != tt.wantNoOp {
				t.Fatalf("IsNoOp() = %v, want %v", o.IsNoOp(), tt.wantNoOp)
			}
			if tt.wantNoOp {
				return
			}
			if o.UID != tt.wantUID {
				t.Fatalf("UID = %d, want %d", o.UID, tt.wantUID)
			}
			if o.GID != tt.wantGID {
				t.Fatalf("GID = %d, want %d", o.GID, tt.wantGID)
			}
		})
	}
}

func TestIsNoOp_NilReceiver(t *testing.T) {
	var o *Owner
	if !o.IsNoOp() {
		t.Fatal("nil Owner.IsNoOp() should be true")
	}
}

func TestChown_NilReceiver(t *testing.T) {
	var o *Owner
	if err := o.Chown("/some/path"); err != nil {
		t.Fatalf("Chown with nil receiver failed: %v", err)
	}
}

func TestChownDir_NilReceiver(t *testing.T) {
	var o *Owner
	if err := o.ChownDir("/some/path", slog.Default()); err != nil {
		t.Fatalf("ChownDir with nil receiver returned error: %v", err)
	}
}

func TestChownMkdirAll_NilReceiver(t *testing.T) {
	var o *Owner
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "a", "b")
	if err := o.ChownMkdirAll(target, 0750, nil); err != nil {
		t.Fatalf("ChownMkdirAll with nil owner failed: %v", err)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatal("directory should exist after ChownMkdirAll")
	}
}

func TestChownMkdirAllContext_NilReceiver(t *testing.T) {
	var o *Owner
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "ctx", "a", "b")
	if err := o.ChownMkdirAllContext(context.Background(), target, 0750, nil); err != nil {
		t.Fatalf("ChownMkdirAllContext with nil owner failed: %v", err)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatal("directory should exist after ChownMkdirAllContext")
	}
}

func TestChown_NoOp(t *testing.T) {
	o, _ := NewOwner(0, 0)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "testfile")
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := o.Chown(path); err != nil {
		t.Fatalf("o.Chown(%q) returned error: %v", path, err)
	}
}

func TestChown_NonExistentPath(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	if err := o.Chown("/nonexistent/path/file.txt"); err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestChownDir_NoOp(t *testing.T) {
	o, _ := NewOwner(0, 0)
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "subdir", "file.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := o.ChownDir(tmpDir, nil); err != nil {
		t.Fatalf("o.ChownDir(%q) returned error: %v", tmpDir, err)
	}
}

func TestChownMkdirAll_NoOp(t *testing.T) {
	o, _ := NewOwner(0, 0)
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "a", "b", "c")
	if err := o.ChownMkdirAll(target, 0750, nil); err != nil {
		t.Fatalf("ChownMkdirAll failed: %v", err)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatal("directory should exist after ChownMkdirAll")
	}
}

func TestChownDirContext_Cancellation(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping chown test: requires root")
	}

	o, _ := NewOwner(1000, 1000)
	tmpDir := t.TempDir()

	// Create a directory structure
	for i := 0; i < 10; i++ {
		sub := filepath.Join(tmpDir, fmt.Sprintf("dir%d", i))
		if err := os.MkdirAll(sub, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	o.ChownDirContext(ctx, tmpDir, slog.Default())
	// Should not panic; some files may or may not be chowned depending on timing
}

func TestChownDirContext_NilReceiver(t *testing.T) {
	var o *Owner
	o.ChownDirContext(context.Background(), "/some/path", slog.Default())
}

func TestChownDirContext_NonExistentDir(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	o.ChownDirContext(context.Background(), "/nonexistent/dir/path", slog.Default())
	// Should not panic; WalkDir error is logged as warning
}

func TestChownDir_SkipsSymlinks(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping chown test: requires root")
	}

	o, _ := NewOwner(1000, 1000)
	tmpDir := t.TempDir()

	// Create a regular file and a symlink
	if err := os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing outside the tree
	if err := os.Symlink("/tmp", filepath.Join(tmpDir, "outsidelink")); err != nil {
		t.Fatal(err)
	}

	o.ChownDir(tmpDir, slog.Default())

	// The symlink itself should still exist (not followed, but chowned)
	linkPath := filepath.Join(tmpDir, "outsidelink")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("symlink should still exist: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink to remain a symlink")
	}
}

func TestChown_WithRootPrivilege(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping chown test: requires root")
	}

	o, _ := NewOwner(1000, 1000)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "testfile")
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	o.Chown(path)

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Sys() == nil {
		t.Fatal("expected stat sys info")
	}

	// Assert actual ownership
	sysStat, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("expected syscall.Stat_t from Sys()")
	}
	if sysStat.Uid != 1000 {
		t.Fatalf("expected UID 1000, got %d", sysStat.Uid)
	}
	if sysStat.Gid != 1000 {
		t.Fatalf("expected GID 1000, got %d", sysStat.Gid)
	}
}

func TestChownDir_WithRootPrivilege(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping chown test: requires root")
	}

	o, _ := NewOwner(1000, 1000)
	tmpDir := t.TempDir()
	sub := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(sub, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	o.ChownDir(tmpDir, slog.Default())

	// Assert ownership of the directory
	dirStat, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirStat.Sys() == nil {
		t.Fatal("expected stat sys info for dir")
	}
	sysStat, ok := dirStat.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("expected syscall.Stat_t from Sys() for dir")
	}
	if sysStat.Uid != 1000 {
		t.Fatalf("expected dir UID 1000, got %d", sysStat.Uid)
	}
	if sysStat.Gid != 1000 {
		t.Fatalf("expected dir GID 1000, got %d", sysStat.Gid)
	}

	// Assert ownership of the file
	fileStat, err := os.Stat(filepath.Join(sub, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sysFileStat, ok := fileStat.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("expected syscall.Stat_t from Sys() for file")
	}
	if sysFileStat.Uid != 1000 {
		t.Fatalf("expected file UID 1000, got %d", sysFileStat.Uid)
	}
	if sysFileStat.Gid != 1000 {
		t.Fatalf("expected file GID 1000, got %d", sysFileStat.Gid)
	}
}

func TestChown_NilLoggerFallsBackToDefault(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	// This should not panic even with nil logger
	o.Chown("/nonexistent/path/file.txt")
}

func TestChownDir_NilLoggerFallsBackToDefault(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	// This should not panic even with nil logger
	o.ChownDir("/nonexistent/dir", nil)
}
