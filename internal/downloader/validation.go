package downloader

import (
	"bytes"
	"errors"
	"strings"
)

// validateMagicBytes checks if the data has the correct magic bytes for the given extension.
// Supports: .mp4, .webm, .jpg, .jpeg, .png, .gif
func validateMagicBytes(data []byte, ext string) error {
	if len(data) < 4 {
		return errors.New("data too small to validate magic bytes")
	}

	ext = strings.ToLower(ext)

	switch ext {
	case ".mp4":
		// MP4: Check for "ftyp" at offset 4 (bytes 4-7 should be "ftyp")
		if len(data) < 8 {
			return errors.New("MP4 data too small to validate ftyp signature")
		}
		if !bytes.HasPrefix(data[4:8], []byte("ftyp")) {
			return errors.New("invalid MP4 magic bytes: expected 'ftyp' at offset 4")
		}

	case ".webm":
		// WebM: Check for EBML header (0x1A 0x45 0xDF 0xA3) at offset 0
		// Caller guarantees len(data) >= 4
		if !bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
			return errors.New("invalid WebM magic bytes: expected EBML header at offset 0")
		}

	case ".jpg", ".jpeg":
		// JPEG: Check for 0xFF 0xD8 0xFF at offset 0
		if !bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
			return errors.New("invalid JPEG magic bytes: expected 0xFF 0xD8 0xFF at offset 0")
		}

	case ".png":
		// PNG: Check for 0x89 0x50 0x4E 0x47 at offset 0
		if !bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
			return errors.New("invalid PNG magic bytes: expected 0x89 0x50 0x4E 0x47 at offset 0")
		}

	case ".gif":
		// GIF: Check for "GIF89a" or "GIF87a" at offset 0
		if len(data) < 6 {
			return errors.New("GIF data too small to validate signature")
		}
		if !strings.HasPrefix(string(data[:6]), "GIF89a") && !strings.HasPrefix(string(data[:6]), "GIF87a") {
			return errors.New("invalid GIF magic bytes: expected 'GIF89a' or 'GIF87a' at offset 0")
		}

	default:
		return errors.New("unsupported file extension for magic byte validation: " + ext)
	}

	return nil
}

// isHTMLContent checks if the data appears to be HTML content.
// Checks for common HTML markers in the first 512 bytes.
func isHTMLContent(data []byte) bool {
	if len(data) > 512 {
		data = data[:512]
	}

	// Convert to lowercase once for case-insensitive checks
	trimmed := strings.TrimSpace(strings.ToLower(string(data)))

	if strings.HasPrefix(trimmed, "<!doctype") ||
		strings.HasPrefix(trimmed, "<html") {
		return true
	}

	htmlTags := []string{
		"<html", "<head", "<body", "<div", "<span", "<p", "<a", "<img",
		"<script", "<style", "<table", "<tr", "<td", "<form", "<input",
		"<meta", "<title", "<link", "<!--", "<!-",
	}

	for _, tag := range htmlTags {
		if strings.Contains(trimmed, tag) {
			return true
		}
	}

	return false
}

// validateMinimumSize ensures the file is at least 1KB (1024 bytes).
func validateMinimumSize(size int64) error {
	if size < 1024 {
		return errors.New("file too small: must be at least 1KB")
	}
	return nil
}

// ValidationError represents a validation error with retry behavior.
type ValidationError struct {
	Permanent bool
	Reason    string
}

// Error returns the error message.
func (e ValidationError) Error() string {
	if e.Permanent {
		return "permanent validation error: " + e.Reason
	}
	return "temporary validation error: " + e.Reason
}
