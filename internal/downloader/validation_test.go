package downloader

import (
	"strings"
	"testing"
)

func TestValidateMagicBytes(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		ext     string
		wantErr bool
		errMsg  string
	}{
		// Valid MP4 tests
		{
			name:    "Valid MP4 with ftyp signature",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'},
			ext:     ".mp4",
			wantErr: false,
		},
		{
			name:    "Valid MP4 with case insensitive extension",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'},
			ext:     ".MP4",
			wantErr: false,
		},
		{
			name:    "Valid MP4 with mixed case extension",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'},
			ext:     ".Mp4",
			wantErr: false,
		},
		{
			name:    "Invalid MP4 - wrong signature",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'x', 'y', 'z', 'w', 'i', 's', 'o', 'm'},
			ext:     ".mp4",
			wantErr: true,
			errMsg:  "invalid MP4 magic bytes",
		},
		{
			name:    "MP4 too small - less than 8 bytes",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p'},
			ext:     ".mp4",
			wantErr: false, // 8 bytes is minimum, this is exactly 8
		},
		{
			name:    "MP4 too small - only 7 bytes",
			data:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y'},
			ext:     ".mp4",
			wantErr: true,
			errMsg:  "too small",
		},
		// Valid WebM tests
		{
			name:    "Valid WebM with EBML header",
			data:    []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81},
			ext:     ".webm",
			wantErr: false,
		},
		{
			name:    "Valid WebM case insensitive",
			data:    []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81},
			ext:     ".WEBM",
			wantErr: false,
		},
		{
			name:    "Invalid WebM - wrong EBML header",
			data:    []byte{0x00, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81},
			ext:     ".webm",
			wantErr: true,
			errMsg:  "invalid WebM magic bytes",
		},
		// Valid JPEG tests
		{
			name:    "Valid JPEG with SOI marker",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
			ext:     ".jpg",
			wantErr: false,
		},
		{
			name:    "Valid JPEG with jpeg extension",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
			ext:     ".jpeg",
			wantErr: false,
		},
		{
			name:    "Valid JPEG case insensitive",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
			ext:     ".JPEG",
			wantErr: false,
		},
		{
			name:    "Invalid JPEG - wrong SOI marker",
			data:    []byte{0xFF, 0xD9, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
			ext:     ".jpg",
			wantErr: true,
			errMsg:  "invalid JPEG magic bytes",
		},
		{
			name:    "JPEG too small - only 2 bytes",
			data:    []byte{0xFF, 0xD8},
			ext:     ".jpg",
			wantErr: true,
			errMsg:  "too small",
		},
		// Valid PNG tests
		{
			name:    "Valid PNG with signature",
			data:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'I', 'H', 'D', 'R'},
			ext:     ".png",
			wantErr: false,
		},
		{
			name:    "Valid PNG case insensitive",
			data:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			ext:     ".PNG",
			wantErr: false,
		},
		{
			name:    "Invalid PNG - wrong signature",
			data:    []byte{0x00, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			ext:     ".png",
			wantErr: true,
			errMsg:  "invalid PNG magic bytes",
		},
		// Valid GIF tests
		{
			name:    "Valid GIF89a",
			data:    []byte{'G', 'I', 'F', '8', '9', 'a', 0x00, 0x00, 0x00, 0x00},
			ext:     ".gif",
			wantErr: false,
		},
		{
			name:    "Valid GIF87a",
			data:    []byte{'G', 'I', 'F', '8', '7', 'a', 0x00, 0x00, 0x00, 0x00},
			ext:     ".gif",
			wantErr: false,
		},
		{
			name:    "Valid GIF case insensitive",
			data:    []byte{'G', 'I', 'F', '8', '9', 'a', 0x00, 0x00, 0x00, 0x00},
			ext:     ".GIF",
			wantErr: false,
		},
		{
			name:    "Invalid GIF - wrong version",
			data:    []byte{'G', 'I', 'F', '8', '8', 'a', 0x00, 0x00, 0x00, 0x00},
			ext:     ".gif",
			wantErr: true,
			errMsg:  "invalid GIF magic bytes",
		},
		{
			name:    "Invalid GIF - not GIF at all",
			data:    []byte{'X', 'Y', 'Z', '8', '9', 'a', 0x00, 0x00, 0x00, 0x00},
			ext:     ".gif",
			wantErr: true,
			errMsg:  "invalid GIF magic bytes",
		},
		{
			name:    "GIF too small - only 5 bytes",
			data:    []byte{'G', 'I', 'F', '8', '9'},
			ext:     ".gif",
			wantErr: true,
			errMsg:  "too small",
		},
		// Edge cases
		{
			name:    "Empty file",
			data:    []byte{},
			ext:     ".mp4",
			wantErr: true,
			errMsg:  "too small",
		},
		{
			name:    "File too small for magic bytes - 3 bytes",
			data:    []byte{0x00, 0x01, 0x02},
			ext:     ".png",
			wantErr: true,
			errMsg:  "too small",
		},
		{
			name:    "Unknown extension",
			data:    []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07},
			ext:     ".xyz",
			wantErr: true,
			errMsg:  "unsupported file extension",
		},
		{
			name:    "Extension without dot",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0},
			ext:     "jpg",
			wantErr: true,
			errMsg:  "unsupported file extension",
		},
		{
			name:    "Buffer with exactly 512 bytes",
			data:    append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 508)...),
			ext:     ".jpg",
			wantErr: false,
		},
		{
			name:    "Buffer with less than 512 bytes - valid",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10},
			ext:     ".jpg",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMagicBytes(tt.data, tt.ext)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMagicBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateMagicBytes() error message = %v, want to contain %v", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestIsHTMLContent(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		// HTML DOCTYPE tests
		{
			name: "DOCTYPE html lowercase",
			data: []byte("<!DOCTYPE html><html><body>Hello</body></html>"),
			want: true,
		},
		{
			name: "DOCTYPE HTML uppercase",
			data: []byte("<!DOCTYPE HTML><HTML><BODY>Hello</BODY></HTML>"),
			want: true,
		},
		{
			name: "doctype mixed case",
			data: []byte("<!doctype Html>"),
			want: true,
		},
		{
			name: "html tag lowercase",
			data: []byte("<html><head><title>Test</title></head></html>"),
			want: true,
		},
		{
			name: "HTML tag uppercase - detected by case-insensitive check",
			data: []byte("<HTML>"),
			want: true,
		},
		{
			name: "DOCTYPE without html",
			data: []byte("<!DOCTYPE>"),
			want: true,
		},
		// HTML tags within first 512 bytes
		{
			name: "head tag",
			data: []byte("<head><title>Test</title></head>"),
			want: true,
		},
		{
			name: "body tag",
			data: []byte("<body>Content</body>"),
			want: true,
		},
		{
			name: "div tag",
			data: []byte("<div>Content</div>"),
			want: true,
		},
		{
			name: "span tag",
			data: []byte("<span>Text</span>"),
			want: true,
		},
		{
			name: "p tag",
			data: []byte("<p>Paragraph</p>"),
			want: true,
		},
		{
			name: "a tag",
			data: []byte("<a href='link'>Click</a>"),
			want: true,
		},
		{
			name: "img tag",
			data: []byte("<img src='image.jpg'>"),
			want: true,
		},
		{
			name: "script tag",
			data: []byte("<script>alert('test')</script>"),
			want: true,
		},
		{
			name: "style tag",
			data: []byte("<style>body{color:red}</style>"),
			want: true,
		},
		{
			name: "table tags",
			data: []byte("<table><tr><td>Cell</td></tr></table>"),
			want: true,
		},
		{
			name: "form and input tags",
			data: []byte("<form><input type='text'></form>"),
			want: true,
		},
		{
			name: "meta tag",
			data: []byte("<meta charset='utf-8'>"),
			want: true,
		},
		{
			name: "title tag",
			data: []byte("<title>Page Title</title>"),
			want: true,
		},
		{
			name: "link tag",
			data: []byte("<link rel='stylesheet' href='style.css'>"),
			want: true,
		},
		{
			name: "HTML comment",
			data: []byte("<!-- This is a comment -->"),
			want: true,
		},
		{
			name: "Comment with dash",
			data: []byte("<!- partial comment"),
			want: true,
		},
		// Leading whitespace before HTML
		{
			name: "Leading whitespace before DOCTYPE",
			data: []byte("   \n\t<!DOCTYPE html>"),
			want: true,
		},
		{
			name: "Leading whitespace before html tag",
			data: []byte("   \n\t<html>"),
			want: true,
		},
		// Binary files - should NOT be HTML
		{
			name: "Binary MP4 file",
			data: []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p'},
			want: false,
		},
		{
			name: "Binary JPEG file",
			data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10},
			want: false,
		},
		{
			name: "Binary PNG file",
			data: []byte{0x89, 0x50, 0x4E, 0x47},
			want: false,
		},
		{
			name: "Binary WebM file",
			data: []byte{0x1A, 0x45, 0xDF, 0xA3},
			want: false,
		},
		{
			name: "Binary GIF file",
			data: []byte{'G', 'I', 'F', '8', '9', 'a'},
			want: false,
		},
		// Non-HTML text files
		{
			name: "Plain text file",
			data: []byte("This is just plain text without any HTML tags."),
			want: false,
		},
		{
			name: "JSON content",
			data: []byte(`{"key": "value", "number": 123}`),
			want: false,
		},
		{
			name: "XML content",
			data: []byte("<?xml version='1.0'?><root><item>Text</item></root>"),
			want: false,
		},
		{
			name: "CSV content",
			data: []byte("name,age,city\nJohn,30,NYC"),
			want: false,
		},
		// Empty file
		{
			name: "Empty file",
			data: []byte{},
			want: false,
		},
		{
			name: "Only whitespace",
			data: []byte("   \n\t\r\n   "),
			want: false,
		},
		// HTML tag beyond 512 bytes (should NOT be detected)
		{
			name: "HTML tag beyond 512 bytes",
			data: append(make([]byte, 512), []byte("<html>")...),
			want: false,
		},
		{
			name: "DOCTYPE beyond 512 bytes",
			data: append(make([]byte, 512), []byte("<!DOCTYPE html>")...),
			want: false,
		},
		// Edge cases
		{
			name: "Less than sign without tag",
			data: []byte("5 < 10 and 10 > 5"),
			want: false,
		},
		{
			name: "Angle brackets with spaces",
			data: []byte("< not a tag >"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHTMLContent(tt.data)
			if got != tt.want {
				t.Errorf("isHTMLContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateMinimumSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{
			name:    "File exactly at 1KB boundary - should pass",
			size:    1024,
			wantErr: false,
		},
		{
			name:    "File at 1023 bytes - should fail",
			size:    1023,
			wantErr: true,
		},
		{
			name:    "File at 1024 bytes - should pass",
			size:    1024,
			wantErr: false,
		},
		{
			name:    "File at 1025 bytes - should pass",
			size:    1025,
			wantErr: false,
		},
		{
			name:    "Large file - should pass",
			size:    1024 * 1024 * 100, // 100MB
			wantErr: false,
		},
		{
			name:    "Zero size file - should fail",
			size:    0,
			wantErr: true,
		},
		{
			name:    "One byte file - should fail",
			size:    1,
			wantErr: true,
		},
		{
			name:    "Half KB file - should fail",
			size:    512,
			wantErr: true,
		},
		{
			name:    "Exactly 1KB minus 1 - should fail",
			size:    1023,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMinimumSize(tt.size)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMinimumSize(%d) error = %v, wantErr %v", tt.size, err, tt.wantErr)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	tests := []struct {
		name      string
		permanent bool
		reason    string
		wantErr   string
	}{
		{
			name:      "Permanent error",
			permanent: true,
			reason:    "invalid magic bytes",
			wantErr:   "permanent validation error: invalid magic bytes",
		},
		{
			name:      "Temporary error",
			permanent: false,
			reason:    "network timeout",
			wantErr:   "temporary validation error: network timeout",
		},
		{
			name:      "Permanent error with empty reason",
			permanent: true,
			reason:    "",
			wantErr:   "permanent validation error: ",
		},
		{
			name:      "Temporary error with empty reason",
			permanent: false,
			reason:    "",
			wantErr:   "temporary validation error: ",
		},
		{
			name:      "Permanent error with long reason",
			permanent: true,
			reason:    "this is a very long error message describing exactly what went wrong during validation",
			wantErr:   "permanent validation error: this is a very long error message describing exactly what went wrong during validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidationError{
				Permanent: tt.permanent,
				Reason:    tt.reason,
			}
			if got := err.Error(); got != tt.wantErr {
				t.Errorf("ValidationError.Error() = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestValidationErrorImplementsErrorInterface(t *testing.T) {
	// Test that ValidationError implements the error interface
	var _ error = ValidationError{Permanent: true, Reason: "test"}

	// Test with permanent error
	errPermanent := ValidationError{Permanent: true, Reason: "permanent test"}
	if errPermanent.Error() != "permanent validation error: permanent test" {
		t.Errorf("Permanent ValidationError.Error() = %q, want %q", errPermanent.Error(), "permanent validation error: permanent test")
	}

	// Test with temporary error
	errTemporary := ValidationError{Permanent: false, Reason: "temporary test"}
	if errTemporary.Error() != "temporary validation error: temporary test" {
		t.Errorf("Temporary ValidationError.Error() = %q, want %q", errTemporary.Error(), "temporary validation error: temporary test")
	}
}

func TestValidationErrorPermanentFlag(t *testing.T) {
	// Test that the Permanent flag is correctly stored and accessible
	err1 := ValidationError{Permanent: true, Reason: "test"}
	if !err1.Permanent {
		t.Error("Expected Permanent to be true")
	}

	err2 := ValidationError{Permanent: false, Reason: "test"}
	if err2.Permanent {
		t.Error("Expected Permanent to be false")
	}
}

// TestEdgeCases combines various edge cases for thorough coverage
func TestEdgeCases(t *testing.T) {
	t.Run("case insensitive extension handling", func(t *testing.T) {
		data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
		extensions := []string{".jpg", ".JPG", ".Jpg", ".jPg", ".JPg"}

		for _, ext := range extensions {
			if err := validateMagicBytes(data, ext); err != nil {
				t.Errorf("validateMagicBytes() with extension %q should not error, got: %v", ext, err)
			}
		}
	})

	t.Run("unknown extension returns error", func(t *testing.T) {
		data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
		unknownExts := []string{".xyz", ".abc", ".unknown", ".bin", ".data"}

		for _, ext := range unknownExts {
			err := validateMagicBytes(data, ext)
			if err == nil {
				t.Errorf("validateMagicBytes() with extension %q should error", ext)
				continue
			}
			if !strings.Contains(err.Error(), "unsupported file extension") {
				t.Errorf("validateMagicBytes() error should mention 'unsupported file extension', got: %v", err)
			}
		}
	})

	t.Run("buffer with exactly 512 bytes", func(t *testing.T) {
		// Create a 512-byte buffer with valid JPEG magic bytes
		data := make([]byte, 512)
		data[0] = 0xFF
		data[1] = 0xD8
		data[2] = 0xFF
		data[3] = 0xE0

		if err := validateMagicBytes(data, ".jpg"); err != nil {
			t.Errorf("validateMagicBytes() with 512-byte buffer should not error, got: %v", err)
		}
	})

	t.Run("buffer with less than 512 bytes", func(t *testing.T) {
		// Create a 100-byte buffer with valid JPEG magic bytes
		data := make([]byte, 100)
		data[0] = 0xFF
		data[1] = 0xD8
		data[2] = 0xFF
		data[3] = 0xE0

		if err := validateMagicBytes(data, ".jpg"); err != nil {
			t.Errorf("validateMagicBytes() with 100-byte buffer should not error, got: %v", err)
		}
	})

	t.Run("HTML detection at boundary - tag partially within 512 bytes", func(t *testing.T) {
		// Create a 514-byte buffer with HTML tag starting at position 508
		// <html> is 6 chars: positions 508-513
		// First 512 bytes (0-511) contains "<htm" - not enough to match "<html"
		data := make([]byte, 514)
		copy(data[508:], "<html>")

		// Won't be detected because only "<htm" is within first 512 bytes
		if isHTMLContent(data) {
			t.Error("isHTMLContent() should return false when only partial tag is within 512 bytes")
		}
	})

	t.Run("HTML detection with tag fully within 512 bytes", func(t *testing.T) {
		// Create a 512-byte buffer with HTML tag starting at position 506
		// <html> is 6 chars: positions 506-511
		data := make([]byte, 512)
		copy(data[506:], "<html>")

		// Should be detected as HTML (complete tag within 512 bytes)
		if !isHTMLContent(data) {
			t.Error("isHTMLContent() should return true for HTML tag fully within 512 bytes")
		}
	})

	t.Run("HTML detection with tag beyond 512 bytes", func(t *testing.T) {
		// Create a 600-byte buffer with HTML tag beyond 512 bytes
		data := make([]byte, 600)
		copy(data[513:], "<html>")

		// Should NOT be detected as HTML
		if isHTMLContent(data) {
			t.Error("isHTMLContent() should return false for HTML tag beyond 512 bytes")
		}
	})
}
