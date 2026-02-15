package migration

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type HTMLParser struct {
	PostMap map[string]PostInfo
}

func NewHTMLParser() *HTMLParser {
	return &HTMLParser{
		PostMap: make(map[string]PostInfo),
	}
}

func (p *HTMLParser) ParseIndexHTML(indexPath string) error {
	file, err := os.Open(indexPath)
	if err != nil {
		return fmt.Errorf("open index.html: %w", err)
	}
	defer file.Close()

	postLinkPattern := regexp.MustCompile(`<a\s+href="([a-zA-Z0-9]+)\.html"`)
	subredditPattern := regexp.MustCompile(`<span\s+class="subreddit"[^>]*>r/([^<]+)</span>`)
	userPattern := regexp.MustCompile(`<span\s+class="user"[^>]*>u/([^<]+)</span>`)

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	var currentPostID string
	var currentSubreddit string
	var currentUsername string

	for scanner.Scan() {
		line := scanner.Text()

		// Extract POSTID from href attribute
		if matches := postLinkPattern.FindStringSubmatch(line); matches != nil {
			// Save previous post before starting new one
			if currentPostID != "" && currentSubreddit != "" {
				p.addPost(currentPostID, currentSubreddit, currentUsername)
			}
			currentPostID = matches[1]
			currentSubreddit = ""
			currentUsername = ""
		}

		// Extract subreddit name
		if matches := subredditPattern.FindStringSubmatch(line); matches != nil {
			currentSubreddit = matches[1]
		}

		// Extract username
		if matches := userPattern.FindStringSubmatch(line); matches != nil {
			currentUsername = matches[1]
		}
	}

	// Save the last post in the file
	if currentPostID != "" && currentSubreddit != "" {
		p.addPost(currentPostID, currentSubreddit, currentUsername)
	}

	return scanner.Err()
}

func (p *HTMLParser) addPost(postID, subreddit, username string) {
	cleanSubreddit := strings.TrimPrefix(subreddit, "r/")
	isUserPost := strings.HasPrefix(cleanSubreddit, "u_")

	p.PostMap[postID] = PostInfo{
		PostID:     postID,
		Subreddit:  cleanSubreddit,
		Username:   username,
		IsUserPost: isUserPost,
	}
}
