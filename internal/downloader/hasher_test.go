package downloader

import (
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"testing"
)

// hexRegex matches a 64-character SHA-256 hex string
var hexRegex = regexp.MustCompile(`^[a-f0-9]{64}$`)

// errorReader returns an error on Read
type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, r.err
}

// eofReader returns io.EOF immediately on Read
type eofReader struct{}

func (r *eofReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

// partialReader returns partial data then an error
type partialReader struct {
	data   []byte
	offset int
	err    error
}

func (r *partialReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func TestCalculateFileHash(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		setup   func() (string, func())
		check   func(t *testing.T, hash string)
	}{
		{
			name: "EmptyFile",
			setup: func() (string, func()) {
				tmpFile, err := os.CreateTemp(t.TempDir(), "empty-*")
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				tmpFile.Close()
				return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }
			},
			check: func(t *testing.T, hash string) {
				if len(hash) != 64 {
					t.Errorf("hash length = %d, want 64", len(hash))
				}
				if !hexRegex.MatchString(hash) {
					t.Errorf("hash format invalid: %s", hash)
				}
			},
		},
		{
			name: "KnownContent",
			setup: func() (string, func()) {
				tmpFile, err := os.CreateTemp(t.TempDir(), "known-*")
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				if _, err := tmpFile.Write([]byte("hello world")); err != nil {
					t.Fatalf("failed to write to temp file: %v", err)
				}
				tmpFile.Close()
				return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }
			},
			check: func(t *testing.T, hash string) {
				// SHA-256 hash of "hello world" is deterministic
				expectedHash := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
				if hash != expectedHash {
					t.Errorf("hash = %s, want %s", hash, expectedHash)
				}
			},
		},
		{
			name: "DifferentContent",
			setup: func() (string, func()) {
				// This test will create two files and compare their hashes
				return "", func() {}
			},
			check: func(t *testing.T, hash string) {}, // Placeholder, logic handled in test body
		},
		{
			name: "IdenticalContent",
			setup: func() (string, func()) {
				// This test will create two files with same content and compare their hashes
				return "", func() {}
			},
			check: func(t *testing.T, hash string) {}, // Placeholder, logic handled in test body
		},
		{
			name:    "NonExistentFile",
			wantErr: true,
			setup: func() (string, func()) {
				return "/nonexistent/path/to/file.txt", func() {}
			},
			check: func(t *testing.T, hash string) {},
		},
		{
			name: "LargeFile",
			setup: func() (string, func()) {
				tmpFile, err := os.CreateTemp(t.TempDir(), "large-*")
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				// Write 10MB of data
				data := bytes.Repeat([]byte("A"), 10*1024*1024)
				if _, err := tmpFile.Write(data); err != nil {
					t.Fatalf("failed to write to temp file: %v", err)
				}
				tmpFile.Close()
				return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }
			},
			check: func(t *testing.T, hash string) {
				if len(hash) != 64 {
					t.Errorf("hash length = %d, want 64", len(hash))
				}
				if !hexRegex.MatchString(hash) {
					t.Errorf("hash format invalid: %s", hash)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "DifferentContent" {
				// Create two files with different content
				tmpDir := t.TempDir()
				file1 := tmpDir + "/file1.txt"
				file2 := tmpDir + "/file2.txt"

				if err := os.WriteFile(file1, []byte("content one"), 0644); err != nil {
					t.Fatalf("failed to write file1: %v", err)
				}
				if err := os.WriteFile(file2, []byte("content two"), 0644); err != nil {
					t.Fatalf("failed to write file2: %v", err)
				}

				hash1, err1 := CalculateFileHash(file1)
				hash2, err2 := CalculateFileHash(file2)

				if err1 != nil {
					t.Fatalf("CalculateFileHash(file1) error = %v", err1)
				}
				if err2 != nil {
					t.Fatalf("CalculateFileHash(file2) error = %v", err2)
				}

				if hash1 == hash2 {
					t.Errorf("different contents produced same hash: %s", hash1)
				}
				return
			}

			if tc.name == "IdenticalContent" {
				// Create two files with identical content
				tmpDir := t.TempDir()
				file1 := tmpDir + "/file1.txt"
				file2 := tmpDir + "/file2.txt"

				content := []byte("identical content for both files")
				if err := os.WriteFile(file1, content, 0644); err != nil {
					t.Fatalf("failed to write file1: %v", err)
				}
				if err := os.WriteFile(file2, content, 0644); err != nil {
					t.Fatalf("failed to write file2: %v", err)
				}

				hash1, err1 := CalculateFileHash(file1)
				hash2, err2 := CalculateFileHash(file2)

				if err1 != nil {
					t.Fatalf("CalculateFileHash(file1) error = %v", err1)
				}
				if err2 != nil {
					t.Fatalf("CalculateFileHash(file2) error = %v", err2)
				}

				if hash1 != hash2 {
					t.Errorf("identical contents produced different hashes: %s vs %s", hash1, hash2)
				}
				return
			}

			filepath, cleanup := tc.setup()
			defer cleanup()

			hash, err := CalculateFileHash(filepath)

			if tc.wantErr {
				if err == nil {
					t.Errorf("CalculateFileHash() expected error but got nil, hash = %s", hash)
				}
				return
			}

			if err != nil {
				t.Errorf("CalculateFileHash() unexpected error = %v", err)
				return
			}

			tc.check(t, hash)
		})
	}
}

func TestCalculateHashFromReader(t *testing.T) {
	tests := []struct {
		name    string
		reader  io.Reader
		wantErr bool
		check   func(t *testing.T, hash string)
	}{
		{
			name:    "BytesReader",
			reader:  bytes.NewReader([]byte("hello world")),
			wantErr: false,
			check: func(t *testing.T, hash string) {
				expectedHash := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
				if hash != expectedHash {
					t.Errorf("hash = %s, want %s", hash, expectedHash)
				}
				if len(hash) != 64 {
					t.Errorf("hash length = %d, want 64", len(hash))
				}
				if !hexRegex.MatchString(hash) {
					t.Errorf("hash format invalid: %s", hash)
				}
			},
		},
		{
			name:    "ErrorReader",
			reader:  &errorReader{err: errors.New("read error")},
			wantErr: true,
			check:   func(t *testing.T, hash string) {},
		},
		{
			name:    "EOFReader",
			reader:  &eofReader{},
			wantErr: false,
			check: func(t *testing.T, hash string) {
				// Empty content hash (SHA-256 of empty data)
				expectedHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
				if hash != expectedHash {
					t.Errorf("hash = %s, want %s", hash, expectedHash)
				}
				if len(hash) != 64 {
					t.Errorf("hash length = %d, want 64", len(hash))
				}
			},
		},
		{
			name: "PartialReader",
			reader: &partialReader{
				data: []byte("partial data"),
				err:  errors.New("read error after partial data"),
			},
			wantErr: true,
			check:   func(t *testing.T, hash string) {},
		},
		{
			name:    "LargeReader",
			reader:  bytes.NewReader(bytes.Repeat([]byte("X"), 10*1024*1024)),
			wantErr: false,
			check: func(t *testing.T, hash string) {
				if len(hash) != 64 {
					t.Errorf("hash length = %d, want 64", len(hash))
				}
				if !hexRegex.MatchString(hash) {
					t.Errorf("hash format invalid: %s", hash)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := CalculateHashFromReader(tc.reader)

			if tc.wantErr {
				if err == nil {
					t.Errorf("CalculateHashFromReader() expected error but got nil, hash = %s", hash)
				}
				return
			}

			if err != nil {
				t.Errorf("CalculateHashFromReader() unexpected error = %v", err)
				return
			}

			tc.check(t, hash)
		})
	}
}
