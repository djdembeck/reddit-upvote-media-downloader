package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
)

func TestExtractorPermalink(t *testing.T) {
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
			name: "URLOverride image on old.reddit.com",
			post: reddit.RedditPost{
				ID:          "old123",
				Title:       "Old Reddit Post",
				Subreddit:   "pics",
				URL:         "https://old.reddit.com/r/pics/comments/old123/test/",
				URLOverride: "https://i.redd.it/old123.jpg",
			},
			wantCount:     1,
			wantURL:       "https://i.redd.it/old123.jpg",
			wantFilename:  "Old Reddit Post_old123.jpg",
			wantMediaType: "image",
		},
		{
			name: "URLOverride image on reddit.com",
			post: reddit.RedditPost{
				ID:          "reddit123",
				Title:       "Reddit Post",
				Subreddit:   "pics",
				URL:         "https://reddit.com/r/pics/comments/reddit123/test/",
				URLOverride: "https://i.redd.it/reddit123.jpg",
			},
			wantCount:     1,
			wantURL:       "https://i.redd.it/reddit123.jpg",
			wantFilename:  "Reddit Post_reddit123.jpg",
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
			name: "MediaMeta multiple keys unsorted",
			post: reddit.RedditPost{
				ID:        "meta456",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/meta456/test/",
				MediaMeta: map[string]reddit.MediaMetadata{
					"b_media": {
						Mime:   "image/png",
						Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/img2.png"},
					},
					"a_media": {
						Mime:   "image/jpeg",
						Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/img1.jpg"},
					},
				},
			},
			wantCount:      2,
			wantURL:        "https://preview.redd.it/img1.jpg",
			wantFilename:   "Test Post_1_meta456.jpg",
			wantMediaType:  "image",
			wantURL2:       "https://preview.redd.it/img2.png",
			wantFilename2:  "Test Post_2_meta456.png",
			wantMediaType2: "image",
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
			name: "Video wins over others",
			post: reddit.RedditPost{
				ID:          "vidmix",
				Title:       "Test Video Post",
				Subreddit:   "videos",
				URL:         "https://www.reddit.com/r/videos/comments/vidmix/test/",
				URLOverride: "https://i.redd.it/override.jpg",
				IsVideo:     true,
				GalleryData: &reddit.GalleryData{
					Items: []reddit.GalleryItem{{MediaID: "media1"}},
				},
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {
						Mime:   "image/jpeg",
						Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/gallery.jpg"},
					},
				},
				Media: &reddit.Media{
					RedditVideo: &reddit.RedditVideo{
						FallbackURL: "https://v.redd.it/vidmix/DASH_720.mp4",
					},
				},
			},
			wantCount:     1,
			wantURL:       "https://v.redd.it/vidmix/DASH_720.mp4",
			wantFilename:  "Test Video Post_vidmix.mp4",
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
		{
			name: "MediaMeta with empty Source.URL (no fallback)",
			post: reddit.RedditPost{
				ID:        "emptyurl",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/emptyurl/test/",
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {
						Mime:   "image/jpeg",
						Source: reddit.MediaMetadataImage{URL: ""},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "MediaMeta with empty Source.URL (fallback to preview)",
			post: reddit.RedditPost{
				ID:        "fallback",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/fallback/test/",
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {
						Mime:   "image/jpeg",
						Source: reddit.MediaMetadataImage{URL: ""},
						Previews: []reddit.MediaMetadataImage{
							{URL: "https://preview.redd.it/preview1.jpg"},
						},
					},
				},
			},
			wantCount:     1,
			wantURL:       "https://preview.redd.it/preview1.jpg",
			wantFilename:  "Test Post_fallback.jpg",
			wantMediaType: "image",
		},
		{
			name: "MediaMeta with empty/unsupported Mime",
			post: reddit.RedditPost{
				ID:        "bademime",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/bademime/test/",
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {
						Mime:   "",
						Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/img.jpg"},
					},
				},
			},
			wantCount:     1,
			wantURL:       "https://preview.redd.it/img.jpg",
			wantFilename:  "Test Post_bademime.jpg",
			wantMediaType: "image",
		},
		{
			name: "Gallery with MediaID missing from MediaMeta",
			post: reddit.RedditPost{
				ID:        "galmiss",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/galmiss/test/",
				GalleryData: &reddit.GalleryData{
					Items: []reddit.GalleryItem{
						{MediaID: "media1"},
						{MediaID: "missing_media"},
						{MediaID: "media2"},
					},
				},
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {Mime: "image/jpeg", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/a.jpg"}},
					"media2": {Mime: "image/png", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/b.png"}},
				},
			},
			wantCount:      2,
			wantURL:        "https://preview.redd.it/a.jpg",
			wantFilename:   "Test Post_1_galmiss.jpg",
			wantMediaType:  "image",
			wantURL2:       "https://preview.redd.it/b.png",
			wantFilename2:  "Test Post_3_galmiss.png",
			wantMediaType2: "image",
		},
		{
			name: "Gallery with all MediaIDs missing from MediaMeta",
			post: reddit.RedditPost{
				ID:        "galallmiss",
				Title:     "Test Post",
				Subreddit: "pics",
				URL:       "https://www.reddit.com/r/pics/comments/galallmiss/test/",
				GalleryData: &reddit.GalleryData{
					Items: []reddit.GalleryItem{
						{MediaID: "missing1"},
						{MediaID: "missing2"},
					},
				},
				MediaMeta: map[string]reddit.MediaMetadata{
					"media1": {Mime: "image/jpeg", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/a.jpg"}},
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewExtractor(nil, "test-agent")
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
			if tt.wantURL2 != "" || tt.wantFilename2 != "" || tt.wantMediaType2 != "" {
				if len(items) <= 1 {
					t.Fatalf("Expected second item for wantURL2=%q, wantFilename2=%q, wantMediaType2=%q, but got len(items)=%d",
						tt.wantURL2, tt.wantFilename2, tt.wantMediaType2, len(items))
				}
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

func TestFetchTextReturnsErrGoneOn410(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")

	_, err := extractor.fetchText(context.Background(), server.URL)
	if err == nil {
		t.Fatal("fetchText() expected error, got nil")
	}
	if !errors.Is(err, errGone) {
		t.Errorf("fetchText() error = %v, want errGone", err)
	}
}

func TestFetchJSONReturnsErrGoneOn410(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")

	_, err := extractor.fetchJSON(context.Background(), server.URL)
	if err == nil {
		t.Fatal("fetchJSON() expected error, got nil")
	}
	if !errors.Is(err, errGone) {
		t.Errorf("fetchJSON() error = %v, want errGone", err)
	}
}

func TestExtractGfycatRedgifsSkipsOnGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")
	post := reddit.RedditPost{ID: "test123", Title: "Test", Subreddit: "test"}

	items, err := extractor.extractGfycatRedgifs(context.Background(), post, server.URL+"/testid")
	if err != nil {
		t.Errorf("extractGfycatRedgifs() error = %v, want nil", err)
	}
	if items != nil {
		t.Errorf("extractGfycatRedgifs() items = %v, want nil", items)
	}
}

func TestExtractGfycatRedgifsReturnsErrorOnNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")
	post := reddit.RedditPost{ID: "test123", Title: "Test", Subreddit: "test"}

	items, err := extractor.extractGfycatRedgifs(context.Background(), post, server.URL)
	if err == nil {
		t.Fatal("extractGfycatRedgifs() expected error on 503, got nil")
	}
	if items != nil {
		t.Errorf("extractGfycatRedgifs() items = %v, want nil", items)
	}
}

func TestExtractGfycatRedgifsWebMFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(`<html><body><video><source src="https://giant.gfycat.com/test.webm" type="video/webm"></video></body></html>`)); err != nil {
			t.Fatalf("TestExtractGfycatRedgifsWebMFallback: HTTP handler failed to write response: %v", err)
		}
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")
	post := reddit.RedditPost{ID: "test123", Title: "Test", Subreddit: "test"}

	items, err := extractor.extractGfycatRedgifs(context.Background(), post, server.URL+"/testid")
	if err != nil {
		t.Fatalf("extractGfycatRedgifs() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("extractGfycatRedgifs() returned %d items, want 1", len(items))
	}
	if items[0].URL != "https://giant.gfycat.com/test.webm" {
		t.Errorf("extractGfycatRedgifs() URL = %s, want https://giant.gfycat.com/test.webm", items[0].URL)
	}
}

func TestExtractGfycatRedgifsRedgifsAPISuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"gif": map[string]interface{}{
				"urls": map[string]string{
					"hd": "https://redgifs.com/get/HD.mp4",
					"sd": "https://redgifs.com/get/SD.mp4",
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	extractor := NewExtractor(server.Client(), "test-agent")
	post := reddit.RedditPost{ID: "test123", Title: "Test", Subreddit: "test"}

	items, err := extractor.extractGfycatRedgifs(context.Background(), post, server.URL+"/watch/testid")
	if err != nil {
		t.Fatalf("extractGfycatRedgifs() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("extractGfycatRedgifs() returned %d items, want 1", len(items))
	}
	if items[0].URL != "https://redgifs.com/get/HD.mp4" {
		t.Errorf("extractGfycatRedgifs() URL = %s, want HD URL", items[0].URL)
	}
}
