package migration

import (
	"strings"
	"unicode"
)

var reservedWindowsNames = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
}

// SanitizePath sanitizes a string for use as a filesystem path component.
func SanitizePath(name string) string {
	if name == "" {
		return UnknownSubreddit
	}

	sanitized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)

	sanitized = strings.Trim(sanitized, "_")

	if sanitized == "" {
		return UnknownSubreddit
	}

	upperSanitized := strings.ToUpper(sanitized)
	for _, reserved := range reservedWindowsNames {
		if upperSanitized == reserved || strings.HasPrefix(upperSanitized, reserved+".") {
			sanitized = "_" + sanitized
			break
		}
	}

	return sanitized
}
