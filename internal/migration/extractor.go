package migration

import (
	"fmt"
	"regexp"
)

var postIDPattern = regexp.MustCompile(`_([a-zA-Z0-9]{6,})(?:_\d+)?\.[^.]+$`)

func ExtractPostID(filename string) (string, error) {
	matches := postIDPattern.FindStringSubmatch(filename)
	if len(matches) < 2 {
		return "", fmt.Errorf("no POSTID found in filename: %s", filename)
	}
	return matches[1], nil
}
