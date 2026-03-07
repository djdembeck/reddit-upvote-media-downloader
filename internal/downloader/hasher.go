package downloader

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// CalculateHashFromReader calculates a BLAKE3 hash from an io.Reader using streaming.
// Returns a 64-character hex-encoded string (256-bit hash).
func CalculateHashFromReader(reader io.Reader) (string, error) {
	hash := blake3.New()

	buf := make([]byte, 32*1024) // 32KB chunks for efficient streaming
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if _, writeErr := hash.Write(buf[:n]); writeErr != nil {
				return "", fmt.Errorf("writing to hash: %w", writeErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading source for hashing: %w", err)
		}
	}

	sum := hash.Sum(nil)
	return hex.EncodeToString(sum), nil
}

// CalculateFileHash calculates a BLAKE3 hash for a file.
// Returns a 64-character hex-encoded string (256-bit hash).
func CalculateFileHash(filePath string) (_ string, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file %s: %w", filePath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close file %s: %w", filePath, closeErr)
		}
	}()

	hash, err := CalculateHashFromReader(file)
	if err != nil {
		return "", fmt.Errorf("calculate hash for %s: %w", filePath, err)
	}

	return hash, nil
}
