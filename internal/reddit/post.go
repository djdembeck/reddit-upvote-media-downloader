// Package reddit provides Reddit API client and post structures.
package reddit

import (
	"strings"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

// RedditPost represents the JSON structure of a Reddit post from the API.
//
//nolint:revive
type RedditPost struct {
	ID          string                   `json:"id"`
	Title       string                   `json:"title"`
	Subreddit   string                   `json:"subreddit"`
	Author      string                   `json:"author"`
	URL         string                   `json:"url"`
	Permalink   string                   `json:"permalink"`
	CreatedUTC  float64                  `json:"createdUtc"`
	IsVideo     bool                     `json:"isVideo"`
	IsSelf      bool                     `json:"isSelf"`
	SelfText    string                   `json:"selftext"`
	Thumbnail   string                   `json:"thumbnail"`
	NumComments int                      `json:"numComments"`
	Score       int                      `json:"score"`
	Media       *Media                   `json:"media"`
	PostHint    string                   `json:"postHint"`
	GalleryData *GalleryData             `json:"galleryData"`
	MediaMeta   map[string]MediaMetadata `json:"mediaMetadata"`
	URLOverride string                   `json:"urlOverriddenByDest"`
}

// GalleryData represents the gallery data structure from Reddit API.
type GalleryData struct {
	Items []GalleryItem `json:"items"`
}

// GalleryItem represents a single item in a Reddit gallery.
type GalleryItem struct {
	MediaID string `json:"mediaId"`
	ID      int    `json:"id"`
}

// MediaMetadata represents metadata for a media item in a gallery.
//
//nolint:govet // Field order kept for readability (Status, Kind, Mime first)
type MediaMetadata struct {
	Status   string               `json:"status"`
	Kind     string               `json:"e"`
	Mime     string               `json:"m"`
	Source   MediaMetadataImage   `json:"s"`
	Previews []MediaMetadataImage `json:"p"`
}

// MediaMetadataImage represents image source information.
type MediaMetadataImage struct {
	URL string `json:"u"`
	X   int    `json:"x"`
	Y   int    `json:"y"`
}

// Media represents media metadata for a Reddit post.
type Media struct {
	RedditVideo *RedditVideo `json:"redditVideo"`
	OEmbed      *OEmbed      `json:"oembed"`
}

// RedditVideo represents Reddit-hosted video metadata.
//
//nolint:revive
type RedditVideo struct {
	BitrateKbps       int    `json:"bitrateKbps"`
	FallbackURL       string `json:"fallbackUrl"`
	Height            int    `json:"height"`
	Width             int    `json:"width"`
	ScrubberMediaURL  string `json:"scrubberMediaUrl"`
	DashURL           string `json:"dashUrl"`
	Duration          int    `json:"duration"`
	HLSURL            string `json:"hlsUrl"`
	IsGIF             bool   `json:"isGif"`
	TranscodingStatus string `json:"transcodingStatus"`
}

// OEmbed represents embedded media metadata (e.g., from external sites).
type OEmbed struct {
	AuthorName   string `json:"authorName"`
	AuthorURL    string `json:"authorUrl"`
	Description  string `json:"description"`
	HTML         string `json:"html"`
	ProviderName string `json:"providerName"`
	ProviderURL  string `json:"providerUrl"`
	ThumbnailURL string `json:"thumbnailUrl"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Version      string `json:"version"`
}

// RedditListing represents the Reddit API listing response.
//
//nolint:revive
type RedditListing struct {
	Kind string `json:"kind"`
	Data struct {
		After    *string       `json:"after"`
		Before   *string       `json:"before"`
		Children []RedditChild `json:"children"`
	} `json:"data"`
}

// RedditChild represents a child item in a Reddit listing.
//
//nolint:revive
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
	if mediaType := rp.detectGalleryType(); mediaType != "" {
		return mediaType
	}
	if mediaType := rp.detectVideoType(); mediaType != "" {
		return mediaType
	}
	if mediaType := rp.detectSelfType(); mediaType != "" {
		return mediaType
	}
	if mediaType := rp.detectFromPostHint(); mediaType != "" {
		return mediaType
	}
	if mediaType := rp.detectFromURL(); mediaType != "" {
		return mediaType
	}
	return MediaTypeUnknown
}

func (rp *RedditPost) detectGalleryType() MediaType {
	if rp.GalleryData != nil && len(rp.GalleryData.Items) > 0 {
		return MediaTypeGallery
	}
	if len(rp.MediaMeta) > 1 {
		for _, meta := range rp.MediaMeta {
			if strings.HasPrefix(strings.ToLower(meta.Mime), "image/") {
				return MediaTypeGallery
			}
		}
	}
	if len(rp.MediaMeta) == 1 {
		for _, meta := range rp.MediaMeta {
			if strings.HasPrefix(strings.ToLower(meta.Mime), "image/") {
				return MediaTypeImage
			}
		}
	}
	return ""
}

func (rp *RedditPost) detectVideoType() MediaType {
	if rp.IsVideo && rp.Media != nil && rp.Media.RedditVideo != nil {
		return MediaTypeVideo
	}
	return ""
}

func (rp *RedditPost) detectSelfType() MediaType {
	if rp.IsSelf {
		return MediaTypeText
	}
	return ""
}

func (rp *RedditPost) detectFromPostHint() MediaType {
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
	return ""
}

func (rp *RedditPost) detectFromURL() MediaType {
	if isImageURL(rp.URL) {
		return MediaTypeImage
	}
	if isVideoURL(rp.URL) {
		return MediaTypeVideo
	}
	if rp.URL != "" && rp.URL != rp.Permalink {
		return MediaTypeLink
	}
	return ""
}

// isImageURL checks if a URL points to an image.
func isImageURL(url string) bool {
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}
	lowerURL := strings.ToLower(url)
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
	lowerURL := strings.ToLower(url)
	for _, ext := range videoExtensions {
		if len(lowerURL) > len(ext) && lowerURL[len(lowerURL)-len(ext):] == ext {
			return true
		}
	}
	// Check for common video hosting platforms
	videoHosts := []string{"youtube.com", "youtu.be", "vimeo.com", "streamable.com", "gfycat.com", "redgifs.com"}
	for _, host := range videoHosts {
		// Simple string contains check
		if strings.Contains(lowerURL, host) {
			return true
		}
	}
	return false
}
