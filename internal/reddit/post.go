// Package reddit provides Reddit API client and post structures.
package reddit

import (
	"time"

	"github.com/user/reddit-media-downloader/internal/storage"
)

// RedditPost represents the JSON structure of a Reddit post from the API.
type RedditPost struct {
	ID          string                   `json:"id"`
	Title       string                   `json:"title"`
	Subreddit   string                   `json:"subreddit"`
	Author      string                   `json:"author"`
	URL         string                   `json:"url"`
	Permalink   string                   `json:"permalink"`
	CreatedUTC  float64                  `json:"created_utc"`
	IsVideo     bool                     `json:"is_video"`
	IsSelf      bool                     `json:"is_self"`
	SelfText    string                   `json:"selftext"`
	Thumbnail   string                   `json:"thumbnail"`
	NumComments int                      `json:"num_comments"`
	Score       int                      `json:"score"`
	Media       *Media                   `json:"media"`
	PostHint    string                   `json:"post_hint"`
	GalleryData *GalleryData             `json:"gallery_data"`
	MediaMeta   map[string]MediaMetadata `json:"media_metadata"`
	URLOverride string                   `json:"url_overridden_by_dest"`
}

type GalleryData struct {
	Items []GalleryItem `json:"items"`
}

type GalleryItem struct {
	MediaID string `json:"media_id"`
	ID      int    `json:"id"`
}

type MediaMetadata struct {
	Status   string               `json:"status"`
	Kind     string               `json:"e"`
	Mime     string               `json:"m"`
	Source   MediaMetadataImage   `json:"s"`
	Previews []MediaMetadataImage `json:"p"`
}

type MediaMetadataImage struct {
	URL string `json:"u"`
	X   int    `json:"x"`
	Y   int    `json:"y"`
}

// Media represents media metadata for a Reddit post.
type Media struct {
	RedditVideo *RedditVideo `json:"reddit_video"`
	OEmbed      *OEmbed      `json:"oembed"`
}

// RedditVideo represents Reddit-hosted video metadata.
type RedditVideo struct {
	BitrateKbps       int    `json:"bitrate_kbps"`
	FallbackURL       string `json:"fallback_url"`
	Height            int    `json:"height"`
	Width             int    `json:"width"`
	ScrubberMediaURL  string `json:"scrubber_media_url"`
	DashURL           string `json:"dash_url"`
	Duration          int    `json:"duration"`
	HLSURL            string `json:"hls_url"`
	IsGIF             bool   `json:"is_gif"`
	TranscodingStatus string `json:"transcoding_status"`
}

// OEmbed represents embedded media metadata (e.g., from external sites).
type OEmbed struct {
	AuthorName   string `json:"author_name"`
	AuthorURL    string `json:"author_url"`
	Description  string `json:"description"`
	HTML         string `json:"html"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Version      string `json:"version"`
}

// RedditListing represents the Reddit API listing response.
type RedditListing struct {
	Kind string `json:"kind"`
	Data struct {
		After    *string       `json:"after"`
		Before   *string       `json:"before"`
		Children []RedditChild `json:"children"`
	} `json:"data"`
}

// RedditChild represents a child item in a Reddit listing.
type RedditChild struct {
	Kind string     `json:"kind"`
	Data RedditPost `json:"data"`
}

// MediaType represents the type of media in a Reddit post.
type MediaType string

const (
	// MediaTypeImage indicates the post contains an image.
	MediaTypeImage MediaType = "image"
	// MediaTypeVideo indicates the post contains a video.
	MediaTypeVideo MediaType = "video"
	// MediaTypeGallery indicates the post contains a gallery.
	MediaTypeGallery MediaType = "gallery"
	// MediaTypeLink indicates the post is a link (external URL).
	MediaTypeLink MediaType = "link"
	// MediaTypeText indicates the post is a text/self post.
	MediaTypeText MediaType = "text"
	// MediaTypeUnknown indicates the media type could not be determined.
	MediaTypeUnknown MediaType = "unknown"
)

// ToStoragePost converts a RedditPost to the internal storage.Post struct.
// The source parameter indicates whether the post was upvoted or saved.
func (rp *RedditPost) ToStoragePost(source string) storage.Post {
	return storage.Post{
		ID:        rp.ID,
		Title:     rp.Title,
		Subreddit: rp.Subreddit,
		Author:    rp.Author,
		URL:       rp.URL,
		Permalink: rp.Permalink,
		CreatedAt: time.Unix(int64(rp.CreatedUTC), 0),
		MediaType: string(rp.DetectMediaType()),
		Source:    source,
	}
}

// DetectMediaType determines the media type of the Reddit post.
func (rp *RedditPost) DetectMediaType() MediaType {
	// Check for Reddit-hosted video
	if rp.IsVideo && rp.Media != nil && rp.Media.RedditVideo != nil {
		return MediaTypeVideo
	}

	// Check for self/text post
	if rp.IsSelf {
		return MediaTypeText
	}

	// Check post_hint for common media types
	switch rp.PostHint {
	case "image":
		return MediaTypeImage
	case "rich:video":
		return MediaTypeVideo
	case "link":
		return MediaTypeLink
	case "self":
		return MediaTypeText
	}

	// Try to infer from URL for image types
	if isImageURL(rp.URL) {
		return MediaTypeImage
	}

	// Try to infer from URL for video types
	if isVideoURL(rp.URL) {
		return MediaTypeVideo
	}

	// Default to link for external URLs
	if rp.URL != "" && rp.URL != rp.Permalink {
		return MediaTypeLink
	}

	return MediaTypeUnknown
}

// isImageURL checks if a URL points to an image.
func isImageURL(url string) bool {
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}
	lowerURL := url
	for _, ext := range imageExtensions {
		if len(lowerURL) > len(ext) && lowerURL[len(lowerURL)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// isVideoURL checks if a URL points to a video.
func isVideoURL(url string) bool {
	videoExtensions := []string{".mp4", ".webm", ".mov", ".mkv", ".avi", ".flv", ".wmv"}
	lowerURL := url
	for _, ext := range videoExtensions {
		if len(lowerURL) > len(ext) && lowerURL[len(lowerURL)-len(ext):] == ext {
			return true
		}
	}
	// Check for common video hosting platforms
	videoHosts := []string{"youtube.com", "youtu.be", "vimeo.com", "streamable.com", "gfycat.com", "redgifs.com"}
	for _, host := range videoHosts {
		// Simple string contains check
		if contains(lowerURL, host) {
			return true
		}
	}
	return false
}

// contains checks if substr is in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
