package migration

import (
	"fmt"
	"regexp"
)

var (
	postIDPattern      = regexp.MustCompile(`_([a-zA-Z0-9]{6,})(?:_\d+)?\.[^.]+$`)
	postIDPatternPlain = regexp.MustCompile(`^([a-zA-Z0-9]{6,})\.[^.]+$`)
)

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
