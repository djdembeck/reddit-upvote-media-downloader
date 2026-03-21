package migration

import (
	"fmt"
	"regexp"
)

var (
	// postIDPattern matches POSTID from filenames with title prefix.
	postIDPattern = regexp.MustCompile(`_([a-zA-Z0-9]{6,})(?:_\d+)?\.[^.]+$`)
	// postIDPatternPlain matches POSTID from plain filenames.
	postIDPatternPlain = regexp.MustCompile(`^([a-zA-Z0-9]{6,})\.[^.]+$`)
)

// ExtractPostID extracts the Reddit post ID from a filename.
func ExtractPostID(filename string) (string, error) {
	matches := postIDPatternPlain.FindStringSubmatch(filename)
	if len(matches) >= 2 {
		return matches[1], nil
	}

	matches = postIDPattern.FindStringSubmatch(filename)
	if len(matches) >= 2 {
		return matches[1], nil
	}

	return "", fmt.Errorf("no POSTID found in filename: %s", filename)
}
