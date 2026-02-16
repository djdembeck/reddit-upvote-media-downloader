package downloader

import (
	"encoding/hex"
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
				return "", writeErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	sum := hash.Sum(nil)
	return hex.EncodeToString(sum), nil
}

// CalculateFileHash calculates a BLAKE3 hash for a file.
// Returns a 64-character hex-encoded string (256-bit hash).
func CalculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash, err := CalculateHashFromReader(file)
	if err != nil {
		return "", err
	}

	return hash, nil
}
