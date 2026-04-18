package ownutil

import (
	"log/slog"
	"os"
	"path/filepath"
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
	var o *Owner = nil
	if !o.IsNoOp() {
		t.Fatal("nil Owner.IsNoOp() should be true")
	}
}

func TestChown_NilReceiver(t *testing.T) {
	var o *Owner = nil
	o.Chown("/some/path", slog.Default())
}

func TestChownDir_NilReceiver(t *testing.T) {
	var o *Owner = nil
	o.ChownDir("/some/path", slog.Default())
}

func TestChownMkdirAll_NilReceiver(t *testing.T) {
	var o *Owner = nil
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "a", "b")
	if err := o.ChownMkdirAll(target, 0750, nil); err != nil {
		t.Fatalf("ChownMkdirAll with nil owner failed: %v", err)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatal("directory should exist after ChownMkdirAll")
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
}
