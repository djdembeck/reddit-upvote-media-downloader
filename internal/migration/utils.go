package migration

import (
	"strings"
	"unicode"
)

func SanitizePath(name string) string {
	if name == "" {
		return "unknown"
	}

	sanitized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)

	sanitized = strings.Trim(sanitized, "_")

	if sanitized == "" {
		return "unknown"
	}

	return sanitized
}
