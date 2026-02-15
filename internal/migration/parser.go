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

	// Patterns for extracting data from bdfr-html index.html format
	// POSTID from <a href="POSTID.html"> in title section
	postLinkPattern := regexp.MustCompile(`<a href="([a-zA-Z0-9]+)\.html">`)
	// Subreddit from <span class="subreddit">r/SUBREDDIT</span>
	subredditPattern := regexp.MustCompile(`<span class="subreddit">r/([^<]+)</span>`)
	// Username from <span class="user">u/USERNAME</span>
	userPattern := regexp.MustCompile(`<span class="user">u/([^<]+)</span>`)

	scanner := bufio.NewScanner(file)
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
	// Clean subreddit by stripping "r/" prefix if present
	cleanSubreddit := strings.TrimPrefix(subreddit, "r/")

	// User profile posts have subreddit starting with "u_"
	isUserPost := strings.HasPrefix(cleanSubreddit, "u_")

	p.PostMap[postID] = PostInfo{
		PostID:     postID,
		Subreddit:  cleanSubreddit,
		Username:   username,
		IsUserPost: isUserPost,
	}
}
