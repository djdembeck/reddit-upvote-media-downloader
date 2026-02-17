package downloader

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateFileHash(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		expectError bool
		expectLen   int
		expectHex   bool
		expectEqual func(hash string) (string, bool)
		description string
	}{
		{
			name: "EmptyFile",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				tmpFile, err := os.Create(filepath.Join(dir, "empty.txt"))
				require.NoError(t, err)
				err = tmpFile.Close()
				require.NoError(t, err)
				return tmpFile.Name()
			},
			expectError: false,
			expectLen:   64,
			expectHex:   true,
			description: "calculates hash for empty file",
		},
		{
			name: "KnownContent",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				tmpFile, err := os.Create(filepath.Join(dir, "known.txt"))
				require.NoError(t, err)
				_, err = tmpFile.Write([]byte("hello world"))
				require.NoError(t, err)
				err = tmpFile.Close()
				require.NoError(t, err)
				return tmpFile.Name()
			},
			expectError: false,
			expectLen:   64,
			expectHex:   true,
			description: "calculates hash for known content",
		},
		{
			name: "DifferentContent",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				file1, err := os.Create(filepath.Join(dir, "contentA.txt"))
				require.NoError(t, err)
				_, err = file1.Write([]byte("content A"))
				require.NoError(t, err)
				err = file1.Close()
				require.NoError(t, err)
				return file1.Name()
			},
			expectError: false,
			expectLen:   64,
			expectHex:   true,
			description: "calculates hash for different content",
		},
		{
			name: "IdenticalContent",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				content := []byte("identical content for both files")
				file1, err := os.Create(filepath.Join(dir, "file1.txt"))
				require.NoError(t, err)
				_, err = file1.Write(content)
				require.NoError(t, err)
				err = file1.Close()
				require.NoError(t, err)
				return file1.Name()
			},
			expectError: false,
			expectLen:   64,
			expectHex:   true,
			description: "calculates identical hash for identical content",
		},
		{
			name: "NonExistentFile",
			setup: func(t *testing.T) string {
				return "/nonexistent/path/to/file.txt"
			},
			expectError: true,
			description: "returns error for non-existent file",
		},
		{
			name: "LargeFile",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				tmpFile, err := os.Create(filepath.Join(dir, "large.bin"))
				require.NoError(t, err)
				largeContent := make([]byte, 1024*1024)
				for i := range largeContent {
					largeContent[i] = byte(i % 256)
				}
				_, err = tmpFile.Write(largeContent)
				require.NoError(t, err)
				err = tmpFile.Close()
				require.NoError(t, err)
				return tmpFile.Name()
			},
			expectError: false,
			expectLen:   64,
			expectHex:   true,
			description: "calculates hash for large file (1MB)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setup(t)

			if tt.name == "IdenticalContent" {
				dir := t.TempDir()
				content := []byte("identical content for both files")
				file2, err := os.Create(filepath.Join(dir, "file2.txt"))
				require.NoError(t, err)
				_, err = file2.Write(content)
				require.NoError(t, err)
				err = file2.Close()
				require.NoError(t, err)

				hash1, err := CalculateFileHash(filePath)
				require.NoError(t, err)
				assert.Len(t, hash1, 64)

				hash2, err := CalculateFileHash(file2.Name())
				require.NoError(t, err)

				assert.Equal(t, hash1, hash2, "identical content should produce identical hashes")
				return
			}

			if tt.name == "DifferentContent" {
				dir := t.TempDir()
				file2, err := os.Create(filepath.Join(dir, "contentB.txt"))
				require.NoError(t, err)
				_, err = file2.Write([]byte("content B"))
				require.NoError(t, err)
				err = file2.Close()
				require.NoError(t, err)

				hash1, err := CalculateFileHash(filePath)
				require.NoError(t, err)

				hash2, err := CalculateFileHash(file2.Name())
				require.NoError(t, err)

				assert.NotEqual(t, hash1, hash2, "different content should produce different hashes")
				return
			}

			hash, err := CalculateFileHash(filePath)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.expectLen > 0 {
				assert.Len(t, hash, tt.expectLen, "hash length should be %d", tt.expectLen)
			}

			if tt.expectHex {
				for _, c := range hash {
					assert.True(t, isValidHex(byte(c)), "hash contains invalid hex character: %c", c)
				}
			}

			if tt.name == "KnownContent" {
				expectedHash, err := CalculateFileHash(filePath)
				require.NoError(t, err)
				assert.Equal(t, hash, expectedHash, "hash should be deterministic")
			}
		})
	}
}

func TestCalculateHashFromReader(t *testing.T) {
	t.Run("EmptyReader", func(t *testing.T) {
		hash, err := CalculateHashFromReader(bytes.NewReader([]byte{}))
		require.NoError(t, err)
		assert.Len(t, hash, 64)
	})

	t.Run("KnownContent", func(t *testing.T) {
		hash, err := CalculateHashFromReader(bytes.NewReader([]byte("hello world")))
		require.NoError(t, err)
		assert.Len(t, hash, 64)
		for _, c := range hash {
			assert.True(t, isValidHex(byte(c)))
		}
	})

	t.Run("Deterministic", func(t *testing.T) {
		content := []byte("test content for determinism")
		hash1, err := CalculateHashFromReader(bytes.NewReader(content))
		require.NoError(t, err)
		hash2, err := CalculateHashFromReader(bytes.NewReader(content))
		require.NoError(t, err)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("Streaming", func(t *testing.T) {
		hash, err := CalculateHashFromReader(bytes.NewReader([]byte("streaming test data with multiple chunks")))
		require.NoError(t, err)
		assert.Len(t, hash, 64)
	})

	t.Run("LargeStream", func(t *testing.T) {
		size := 5 * 1024 * 1024
		largeContent := make([]byte, size)
		for i := range largeContent {
			largeContent[i] = byte(i % 256)
		}
		hash, err := CalculateHashFromReader(bytes.NewReader(largeContent))
		require.NoError(t, err)
		assert.Len(t, hash, 64)
	})

	t.Run("ErrorReader", func(t *testing.T) {
		_, err := CalculateHashFromReader(&errorReader{err: os.ErrPermission})
		assert.Error(t, err)
	})

	t.Run("EOFReader", func(t *testing.T) {
		hash, err := CalculateHashFromReader(&eofReader{})
		require.NoError(t, err)
		assert.Len(t, hash, 64)
	})

	t.Run("PartialRead", func(t *testing.T) {
		content := []byte("partial read test")
		hash, err := CalculateHashFromReader(&partialReader{data: content})
		require.NoError(t, err)
		assert.Len(t, hash, 64)
		expectedHash, err := CalculateFileHashFromBytes(content)
		require.NoError(t, err)
		assert.Equal(t, hash, expectedHash)
	})
}

func TestHashConsistency_FileAndReader(t *testing.T) {
	dir := t.TempDir()
	content := []byte("consistency test content")
	tmpFile, err := os.Create(filepath.Join(dir, "consistency.txt"))
	require.NoError(t, err)
	_, err = tmpFile.Write(content)
	require.NoError(t, err)
	err = tmpFile.Close()
	require.NoError(t, err)

	hashFromFile, err := CalculateFileHash(tmpFile.Name())
	require.NoError(t, err)

	hashFromReader, err := CalculateHashFromReader(bytes.NewReader(content))
	require.NoError(t, err)

	assert.Equal(t, hashFromFile, hashFromReader, "file hash and reader hash should be identical for same content")
}

func TestHashHexFormat(t *testing.T) {
	dir := t.TempDir()
	tmpFile, err := os.Create(filepath.Join(dir, "hexformat.txt"))
	require.NoError(t, err)
	_, err = tmpFile.Write([]byte("test"))
	require.NoError(t, err)
	err = tmpFile.Close()
	require.NoError(t, err)

	hash, err := CalculateFileHash(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, strings.ToLower(hash), hash, "hash should be lowercase hex")

	for _, c := range hash {
		assert.False(t, c >= 'A' && c <= 'F', "hash should not contain uppercase hex characters")
	}
}

func TestCalculateFileHash_KnownReference(t *testing.T) {
	// BLAKE3-256 hash for "hello world" (precomputed reference value)
	expectedHash := "d74981efa70a0c880b8d8c1985d075dbcbf679b99a5f9914e5aaf96b831a9e24"

	dir := t.TempDir()
	tmpFile, err := os.Create(filepath.Join(dir, "knownref.txt"))
	require.NoError(t, err)
	content := []byte("hello world")
	_, err = tmpFile.Write(content)
	require.NoError(t, err)
	err = tmpFile.Close()
	require.NoError(t, err)

	hash, err := CalculateFileHash(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, expectedHash, hash, "hash should match known BLAKE3-256 reference value")
}

// Helper function to calculate hash from bytes (for testing)
func CalculateFileHashFromBytes(data []byte) (string, error) {
	return CalculateHashFromReader(bytes.NewReader(data))
}

// Helper function to check if character is valid hex
func isValidHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// errorReader is a reader that always returns an error
type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

// eofReader is a reader that returns EOF immediately
type eofReader struct{}

func (r *eofReader) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

// partialReader is a reader that returns data in small chunks
type partialReader struct {
	data   []byte
	offset int
}

func (r *partialReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}

	// Return at most 5 bytes at a time
	remaining := len(r.data) - r.offset
	toRead := len(p)
	if toRead > remaining {
		toRead = remaining
	}
	if toRead > 5 {
		toRead = 5
	}

	copy(p, r.data[r.offset:r.offset+toRead])
	r.offset += toRead

	if r.offset >= len(r.data) {
		return toRead, io.EOF
	}

	return toRead, nil
}
