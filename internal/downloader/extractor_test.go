package downloader

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
)

func TestExtractorPermalink(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")

	tests := []struct {
		name           string
		post           reddit.RedditPost
		wantCount      int
		wantURL        string
		wantFilename   string
		wantMediaType  string
		wantURL2       string
		wantFilename2  string
		wantMediaType2 string
		wantNil        bool
	}{
		{
			name: "URLOverride image",
			post: reddit.RedditPost{
				ID:          "url123",
				Title:       "Test Post",
				Subreddit:   "pics",
				URL:         "https://www.reddit.com/r/pics/comments/url123/test/",
				URLOverride: "https://i.redd.it/abc123.jpg",
			},
			wantCount:     1,
			wantURL:       "https://i.redd.it/abc123.jpg",
			wantFilename:  "Test Post_url123.jpg",
			wantMediaType: "image",
		},
		{
			name: "MediaMeta",
			post: reddit.RedditPost{
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
			},
			wantCount:     1,
			wantURL:       "https://preview.redd.it/img1.jpg",
			wantFilename:  "Test Post_meta123.jpg",
			wantMediaType: "image",
		},
		{
			name: "Gallery",
			post: reddit.RedditPost{
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
			},
			wantCount:      2,
			wantURL:        "https://preview.redd.it/a.jpg",
			wantFilename:   "Test Post_1_gal123.jpg",
			wantMediaType:  "image",
			wantURL2:       "https://preview.redd.it/b.png",
			wantFilename2:  "Test Post_2_gal123.png",
			wantMediaType2: "image",
		},
		{
			name: "Video priority",
			post: reddit.RedditPost{
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
			},
			wantCount:     1,
			wantURL:       "https://v.redd.it/vid123/DASH_720.mp4",
			wantFilename:  "Test Video Post_vid123.mp4",
			wantMediaType: "video",
		},
		{
			name: "No media",
			post: reddit.RedditPost{
				ID:        "nomedia",
				Title:     "Text Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/nomedia/test/",
				IsSelf:    true,
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := extractor.Extract(context.Background(), tt.post)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}

			if tt.wantNil {
				if len(items) != 0 {
					t.Fatalf("Extract() items = %v, want empty", items)
				}
				return
			}

			if len(items) != tt.wantCount {
				t.Fatalf("Extract() items = %d, want %d", len(items), tt.wantCount)
			}

			if tt.wantURL != "" && items[0].URL != tt.wantURL {
				t.Errorf("URL = %s, want %s", items[0].URL, tt.wantURL)
			}
			if tt.wantFilename != "" && items[0].Filename != tt.wantFilename {
				t.Errorf("Filename = %s, want %s", items[0].Filename, tt.wantFilename)
			}
			if tt.wantMediaType != "" && items[0].MediaType != tt.wantMediaType {
				t.Errorf("MediaType = %s, want %s", items[0].MediaType, tt.wantMediaType)
			}
			if tt.wantURL2 != "" && items[1].URL != tt.wantURL2 {
				t.Errorf("items[1].URL = %s, want %s", items[1].URL, tt.wantURL2)
			}
			if tt.wantFilename2 != "" && items[1].Filename != tt.wantFilename2 {
				t.Errorf("items[1].Filename = %s, want %s", items[1].Filename, tt.wantFilename2)
			}
			if tt.wantMediaType2 != "" && items[1].MediaType != tt.wantMediaType2 {
				t.Errorf("items[1].MediaType = %s, want %s", items[1].MediaType, tt.wantMediaType2)
			}
		})
	}
}
