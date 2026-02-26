package downloader

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
)

func TestExtractorPermalinkWithURLOverrideImage(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:          "url123",
		Title:       "Test Post",
		Subreddit:   "pics",
		URL:         "https://www.reddit.com/r/pics/comments/url123/test/",
		URLOverride: "https://i.redd.it/abc123.jpg",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://i.redd.it/abc123.jpg" {
		t.Errorf("URL = %s, want https://i.redd.it/abc123.jpg", items[0].URL)
	}
	if items[0].Filename != "Test Post_url123.jpg" {
		t.Errorf("Filename = %s, want Test Post_url123.jpg", items[0].Filename)
	}
	if items[0].MediaType != "image" {
		t.Errorf("MediaType = %s, want image", items[0].MediaType)
	}
}

func TestExtractorPermalinkWithMediaMeta(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "meta123",
		Title:     "Test Post",
		Subreddit: "pics",
		URL:       "https://www.reddit.com/r/pics/comments/meta123/test/",
		MediaMeta: map[string]reddit.MediaMetadata{
			"media1": {
				Mime:   "image/jpeg",
				Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/img1.jpg"},
			},
		},
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://preview.redd.it/img1.jpg" {
		t.Errorf("URL = %s, want https://preview.redd.it/img1.jpg", items[0].URL)
	}
	if items[0].Filename != "Test Post_meta123.jpg" {
		t.Errorf("Filename = %s, want Test Post_meta123.jpg", items[0].Filename)
	}
}

func TestExtractorPermalinkWithGallery(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "gal123",
		Title:     "Test Post",
		Subreddit: "pics",
		URL:       "https://www.reddit.com/r/pics/comments/gal123/test/",
		GalleryData: &reddit.GalleryData{
			Items: []reddit.GalleryItem{{MediaID: "media1"}, {MediaID: "media2"}},
		},
		MediaMeta: map[string]reddit.MediaMetadata{
			"media1": {Mime: "image/jpeg", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/a.jpg"}},
			"media2": {Mime: "image/png", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/b.png"}},
		},
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Extract() items = %d, want 2", len(items))
	}
	if items[0].URL != "https://preview.redd.it/a.jpg" {
		t.Errorf("URL = %s, want https://preview.redd.it/a.jpg", items[0].URL)
	}
	if items[0].Filename != "Test Post_gal123.jpg" {
		t.Errorf("Filename = %s, want Test Post_gal123.jpg", items[0].Filename)
	}
	if items[1].URL != "https://preview.redd.it/b.png" {
		t.Errorf("URL = %s, want https://preview.redd.it/b.png", items[1].URL)
	}
	if items[1].Filename != "Test Post_gal123.png" {
		t.Errorf("Filename = %s, want Test Post_gal123.png", items[1].Filename)
	}
}

func TestExtractorPermalinkVideoPriority(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "vid123",
		Title:     "Test Video Post",
		Subreddit: "videos",
		URL:       "https://www.reddit.com/r/videos/comments/vid123/test/",
		IsVideo:   true,
		Media: &reddit.Media{
			RedditVideo: &reddit.RedditVideo{
				FallbackURL: "https://v.redd.it/vid123/DASH_720.mp4",
			},
		},
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://v.redd.it/vid123/DASH_720.mp4" {
		t.Errorf("URL = %s, want https://v.redd.it/vid123/DASH_720.mp4", items[0].URL)
	}
	if items[0].MediaType != "video" {
		t.Errorf("MediaType = %s, want video", items[0].MediaType)
	}
}

func TestExtractorPermalinkNoMedia(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "nomedia",
		Title:     "Text Post",
		Subreddit: "pics",
		URL:       "https://www.reddit.com/r/pics/comments/nomedia/test/",
		IsSelf:    true,
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if items != nil {
		t.Fatalf("Extract() items = %v, want nil", items)
	}
}
