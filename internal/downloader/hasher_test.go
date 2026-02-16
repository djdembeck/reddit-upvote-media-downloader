package downloader

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCalculateFileHash_EmptyFile(t *testing.T) {
	// Create a temporary empty file
	tmpFile, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Calculate hash
	hash, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Verify hash is 64 characters (256-bit hex-encoded)
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}

	// Verify hash is valid hex
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			t.Errorf("Hash contains invalid hex character: %c", c)
		}
	}
}

func TestCalculateFileHash_KnownContent(t *testing.T) {
	// Create a temporary file with known content
	tmpFile, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := []byte("hello world")
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Calculate hash
	hash, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}

	// Calculate expected hash by reading the file
	expectedHash, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to calculate expected hash: %v", err)
	}

	// Hash should be deterministic
	if hash != expectedHash {
		t.Errorf("Hash not deterministic: got %s, want %s", hash, expectedHash)
	}
}

func TestCalculateFileHash_DifferentContent(t *testing.T) {
	// Create two files with different content
	tmpFile1, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile1.Name())

	tmpFile2, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile2.Name())

	// Write different content to each file
	if _, err := tmpFile1.Write([]byte("content A")); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile1.Close()

	if _, err := tmpFile2.Write([]byte("content B")); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile2.Close()

	hash1, err := CalculateFileHash(tmpFile1.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	hash2, err := CalculateFileHash(tmpFile2.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Different content should produce different hashes
	if hash1 == hash2 {
		t.Error("Different content should produce different hashes")
	}
}

func TestCalculateFileHash_IdenticalContent(t *testing.T) {
	// Create two files with identical content
	tmpFile1, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile1.Name())

	tmpFile2, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile2.Name())

	content := []byte("identical content for both files")
	if _, err := tmpFile1.Write(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile1.Close()

	if _, err := tmpFile2.Write(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile2.Close()

	hash1, err := CalculateFileHash(tmpFile1.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	hash2, err := CalculateFileHash(tmpFile2.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Identical content should produce identical hashes
	if hash1 != hash2 {
		t.Error("Identical content should produce identical hashes")
	}
}

func TestCalculateFileHash_NonExistentFile(t *testing.T) {
	_, err := CalculateFileHash("/nonexistent/path/to/file.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestCalculateFileHash_LargeFile(t *testing.T) {
	// Create a temporary file with large content (1MB)
	tmpFile, err := os.CreateTemp("", "hash-test-*.bin")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Create 1MB of data
	largeContent := make([]byte, 1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	if _, err := tmpFile.Write(largeContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Calculate hash
	hash, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}
}

func TestCalculateHashFromReader_EmptyReader(t *testing.T) {
	reader := bytes.NewReader([]byte{})

	hash, err := CalculateHashFromReader(reader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}
}

func TestCalculateHashFromReader_KnownContent(t *testing.T) {
	content := []byte("hello world")
	reader := bytes.NewReader(content)

	hash, err := CalculateHashFromReader(reader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}

	// Verify hash is valid hex
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			t.Errorf("Hash contains invalid hex character: %c", c)
		}
	}
}

func TestCalculateHashFromReader_Deterministic(t *testing.T) {
	content := []byte("test content for determinism")
	reader1 := bytes.NewReader(content)
	reader2 := bytes.NewReader(content)

	hash1, err := CalculateHashFromReader(reader1)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	hash2, err := CalculateHashFromReader(reader2)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Same content should produce same hash
	if hash1 != hash2 {
		t.Error("Same content should produce same hash")
	}
}

func TestCalculateHashFromReader_Streaming(t *testing.T) {
	// Test that streaming works correctly by reading in chunks
	content := []byte("streaming test data with multiple chunks")
	reader := bytes.NewReader(content)

	hash, err := CalculateHashFromReader(reader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}
}

func TestCalculateHashFromReader_LargeStream(t *testing.T) {
	// Create a large reader simulating streaming
	size := 5 * 1024 * 1024 // 5MB
	largeContent := make([]byte, size)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}
	reader := bytes.NewReader(largeContent)

	hash, err := CalculateHashFromReader(reader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}
}

func TestCalculateHashFromReader_ErrorReader(t *testing.T) {
	// Create a reader that returns an error
	errorReader := &errorReader{err: os.ErrPermission}

	_, err := CalculateHashFromReader(errorReader)
	if err == nil {
		t.Error("Expected error from error reader")
	}
}

func TestCalculateHashFromReader_EOFReader(t *testing.T) {
	// Create a reader that returns EOF immediately
	eofReader := &eofReader{}

	hash, err := CalculateHashFromReader(eofReader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Should return hash for empty content
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}
}

func TestCalculateHashFromReader_PartialRead(t *testing.T) {
	// Create a reader that returns partial reads
	content := []byte("partial read test")
	partialReader := &partialReader{data: content}

	hash, err := CalculateHashFromReader(partialReader)
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Verify hash is 64 characters
	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}

	// Verify hash matches expected for the content
	expectedHash, err := CalculateFileHashFromBytes(content)
	if err != nil {
		t.Fatalf("Failed to calculate expected hash: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("Hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

// Helper function to calculate hash from bytes (for testing)
func CalculateFileHashFromBytes(data []byte) (string, error) {
	return CalculateHashFromReader(bytes.NewReader(data))
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
	data      []byte
	offset    int
	chunkSize int
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

func TestHashConsistency_FileAndReader(t *testing.T) {
	// Create a file and verify that CalculateFileHash and CalculateHashFromReader
	// produce the same hash for the same content
	tmpFile, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := []byte("consistency test content")
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Calculate hash using file
	hashFromFile, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Calculate hash using reader
	hashFromReader, err := CalculateHashFromReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("CalculateHashFromReader() error = %v", err)
	}

	// Both should produce the same hash
	if hashFromFile != hashFromReader {
		t.Error("File hash and reader hash should be identical for same content")
	}
}

func TestHashHexFormat(t *testing.T) {
	// Verify that hash is lowercase hex
	tmpFile, err := os.CreateTemp("", "hash-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte("test")); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	hash, err := CalculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("CalculateFileHash() error = %v", err)
	}

	// Verify hash is lowercase hex
	if strings.ToLower(hash) != hash {
		t.Error("Hash should be lowercase hex")
	}

	// Verify no uppercase letters
	for _, c := range hash {
		if c >= 'A' && c <= 'F' {
			t.Errorf("Hash should not contain uppercase hex characters: %c", c)
		}
	}
}
