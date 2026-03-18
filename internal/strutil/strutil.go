// Package strutil provides string utility functions.
package strutil

// IsNumeric checks if a string contains only digits.
func IsNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
