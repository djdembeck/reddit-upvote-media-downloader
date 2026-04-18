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

func TestNewOwner_ZeroValues(t *testing.T) {
	o, err := NewOwner(0, 0)
	if err != nil {
		t.Fatalf("NewOwner(0,0) returned error: %v", err)
	}
	if o == nil {
		t.Fatal("NewOwner(0,0) returned nil, expected valid *Owner")
	}
	if !o.IsNoOp() {
		t.Fatal("NewOwner(0,0).IsNoOp() should be true")
	}
}

func TestNewOwner_NonZeroValues(t *testing.T) {
	o, err := NewOwner(1000, 1000)
	if err != nil {
		t.Fatalf("NewOwner(1000,1000) returned error: %v", err)
	}
	if o.IsNoOp() {
		t.Fatal("NewOwner(1000,1000).IsNoOp() should be false")
	}
	if o.UID != 1000 || o.GID != 1000 {
		t.Fatalf("expected UID=1000 GID=1000, got UID=%d GID=%d", o.UID, o.GID)
	}
}

func TestNewOwner_PartialPUID(t *testing.T) {
	o, err := NewOwner(1000, 0)
	if err != nil {
		t.Fatalf("NewOwner(1000,0) returned error: %v", err)
	}
	if o.IsNoOp() {
		t.Fatal("NewOwner(1000,0).IsNoOp() should be false")
	}
}

func TestNewOwner_PartialPGID(t *testing.T) {
	o, err := NewOwner(0, 1000)
	if err != nil {
		t.Fatalf("NewOwner(0,1000) returned error: %v", err)
	}
	if o.IsNoOp() {
		t.Fatal("NewOwner(0,1000).IsNoOp() should be false")
	}
}

func TestNewOwner_NegativePUID(t *testing.T) {
	_, err := NewOwner(-1, 0)
	if err == nil {
		t.Fatal("NewOwner(-1,0) should return error")
	}
}

func TestNewOwner_NegativePGID(t *testing.T) {
	_, err := NewOwner(0, -1)
	if err == nil {
		t.Fatal("NewOwner(0,-1) should return error")
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
	o.Chown("/some/path", slog.Default())
}

func TestChownDir_NilReceiver(t *testing.T) {
	var o *Owner
	o.ChownDir("/some/path", slog.Default())
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
	o.Chown(path, nil)
}

func TestChown_NonExistentPath(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	o.Chown("/nonexistent/path/file.txt", slog.Default())
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
	o.ChownDir(tmpDir, nil)
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
	o.Chown(path, slog.Default())

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
	o.Chown("/nonexistent/path/file.txt", nil)
}

func TestChownDir_NilLoggerFallsBackToDefault(t *testing.T) {
	o, _ := NewOwner(1000, 1000)
	// This should not panic even with nil logger
	o.ChownDir("/nonexistent/dir", nil)
}
