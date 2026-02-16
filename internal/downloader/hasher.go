package downloader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func CalculateHashFromReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	buf := make([]byte, 4096)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			written, writeErr := hash.Write(buf[:n])
			if writeErr != nil {
				return "", fmt.Errorf("hash write failed for %d bytes: %w", written, writeErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("read failed from reader: %w", err)
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func CalculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("calculating hash for %s: %w", filePath, err)
	}
	defer file.Close()

	hash, err := CalculateHashFromReader(file)
	if err != nil {
		return "", fmt.Errorf("calculating hash for %s: %w", filePath, err)
	}

	return hash, nil
}

func CalculateFileHashFromBytes(data []byte) string {
	reader := bytes.NewReader(data)
	hash, err := CalculateHashFromReader(reader)
	if err != nil {
		return ""
	}
	return hash
}
