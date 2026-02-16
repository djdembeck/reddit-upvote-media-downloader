package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/reddit-media-downloader/internal/reddit"
	"github.com/user/reddit-media-downloader/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type hostRewriteTransport struct {
	base   http.RoundTripper
	target *url.URL
	hosts  map[string]struct{}
}

func (h *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}

	host := strings.ToLower(req.URL.Host)
	if _, ok := h.hosts[host]; !ok {
		return base.RoundTrip(req)
	}

	clone := req.Clone(req.Context())
	cloneURL := *req.URL
	cloneURL.Scheme = h.target.Scheme
	cloneURL.Host = h.target.Host
	clone.URL = &cloneURL
	clone.Host = req.URL.Host
	return base.RoundTrip(clone)
}

func newRewriteClient(server *httptest.Server, hosts ...string) *http.Client {
	target, _ := url.Parse(server.URL)
	hostMap := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		hostMap[strings.ToLower(host)] = struct{}{}
	}
	return &http.Client{
		Transport: &hostRewriteTransport{
			base:   http.DefaultTransport,
			target: target,
			hosts:  hostMap,
		},
	}
}

func waitForCondition(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition")
}

func TestExtractorRedditImage(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "abc123",
		Subreddit: "pics",
		URL:       "https://i.redd.it/abc123.jpg",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].Filename != "untitled_abc123.jpg" {
		t.Errorf("Filename = %s, want untitled_abc123.jpg", items[0].Filename)
	}
	if items[0].MediaType != "image" {
		t.Errorf("MediaType = %s, want image", items[0].MediaType)
	}
}

func TestExtractorGallery(t *testing.T) {
	post := reddit.RedditPost{
		ID:        "gal123",
		Subreddit: "pics",
		GalleryData: &reddit.GalleryData{
			Items: []reddit.GalleryItem{{MediaID: "media1"}, {MediaID: "media2"}},
		},
		MediaMeta: map[string]reddit.MediaMetadata{
			"media1": {Mime: "image/jpeg", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/a.jpg"}},
			"media2": {Mime: "image/png", Source: reddit.MediaMetadataImage{URL: "https://preview.redd.it/b.png"}},
		},
	}

	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Extract() items = %d, want 2", len(items))
	}
	if items[0].Filename != "untitled_gal123.jpg" {
		t.Errorf("Filename = %s, want untitled_gal123.jpg", items[0].Filename)
	}
	if items[1].Filename != "untitled_gal123.png" {
		t.Errorf("Filename = %s, want untitled_gal123.png", items[1].Filename)
	}
}

func TestExtractorRedditVideoDash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			switch r.URL.Path {
			case "/abc/DASH_1080.mp4":
				w.WriteHeader(http.StatusNotFound)
			case "/abc/DASH_720.mp4":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := newRewriteClient(server, "v.redd.it")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.RedditPost{
		ID:        "abc",
		Subreddit: "videos",
		IsVideo:   true,
		Media: &reddit.Media{
			RedditVideo: &reddit.RedditVideo{DashURL: "https://v.redd.it/abc/DASHPlaylist.mpd"},
		},
		URL: "https://v.redd.it/abc",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://v.redd.it/abc/DASH_720.mp4" {
		t.Errorf("URL = %s, want https://v.redd.it/abc/DASH_720.mp4", items[0].URL)
	}
}

func TestExtractorImgurPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, `<meta property="og:image" content="https://i.imgur.com/test.jpg">`)
	}))
	defer server.Close()

	client := newRewriteClient(server, "imgur.com")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.RedditPost{
		ID:        "img1",
		Subreddit: "pics",
		URL:       "https://imgur.com/abcd",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://i.imgur.com/test.jpg" {
		t.Errorf("URL = %s, want https://i.imgur.com/test.jpg", items[0].URL)
	}
}

func TestExtractorGfycatAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/gfycats/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payload := map[string]map[string]interface{}{
			"gfyItem": {
				"mp4Url": "https://giant.gfycat.com/sample.mp4",
			},
		}
		json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := newRewriteClient(server, "api.gfycat.com")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.RedditPost{
		ID:        "gfy1",
		Subreddit: "gifs",
		URL:       "https://gfycat.com/sample",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://giant.gfycat.com/sample.mp4" {
		t.Errorf("URL = %s, want https://giant.gfycat.com/sample.mp4", items[0].URL)
	}
}

func TestExtractorRedgifsAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/gifs/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payload := map[string]map[string]map[string]string{
			"gif": {
				"urls": {
					"hd": "https://thumbs.redgifs.com/sample.mp4",
				},
			},
		}
		json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := newRewriteClient(server, "api.redgifs.com")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.RedditPost{
		ID:        "rg1",
		Subreddit: "gifs",
		URL:       "https://redgifs.com/watch/sample",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].URL != "https://thumbs.redgifs.com/sample.mp4" {
		t.Errorf("URL = %s, want https://thumbs.redgifs.com/sample.mp4", items[0].URL)
	}
}

func TestExtractorDirectLink(t *testing.T) {
	extractor := NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
	post := reddit.RedditPost{
		ID:        "dir1",
		Subreddit: "videos",
		URL:       "https://example.com/clip.webm",
	}

	items, err := extractor.Extract(context.Background(), post)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Extract() items = %d, want 1", len(items))
	}
	if items[0].Filename != "untitled_dir1.webm" {
		t.Errorf("Filename = %s, want untitled_dir1.webm", items[0].Filename)
	}
}

func TestDownloaderSkipsExisting(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	bdfrStyleFilePath := filepath.Join(subredditDir, "test_abc.jpg")
	if err := os.WriteFile(bdfrStyleFilePath, []byte("existing"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected request")
	})}

	downloader := NewDownloader(Config{
		OutputDir:  outputDir,
		HTTPClient: client,
		Retries:    1,
		Timeout:    time.Second,
		UserAgent:  "test-agent",
	}, nil)

	items := []Downloadable{{
		PostID:    "abc",
		Subreddit: "pics",
		Filename:  "abc_1.jpg",
		URL:       "https://example.com/abc.jpg",
	}}

	if _, err := downloader.Download(context.Background(), items); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
}

func TestDownloaderRetries(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&calls, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	outputDir := t.TempDir()
	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     3,
		BackoffBase: time.Millisecond,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, nil)

	items := []Downloadable{{
		PostID:    "retry1",
		Subreddit: "pics",
		Filename:  "retry1_1.jpg",
		URL:       server.URL + "/file.jpg",
	}}

	if _, err := downloader.Download(context.Background(), items); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	filePath := filepath.Join(outputDir, "pics", "retry1_1.jpg")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

func TestDownloaderContinuesOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	outputDir := t.TempDir()
	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 2,
	}, nil)

	items := []Downloadable{
		{PostID: "fail", Subreddit: "pics", Filename: "fail_1.jpg", URL: server.URL + "/fail.jpg"},
		{PostID: "ok", Subreddit: "pics", Filename: "ok_1.jpg", URL: server.URL + "/ok.jpg"},
	}

	if _, err := downloader.Download(context.Background(), items); err == nil {
		t.Fatalf("expected error from Download()")
	}

	filePath := filepath.Join(outputDir, "pics", "ok_1.jpg")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected success file to exist: %v", err)
	}
}

func TestDownloaderConcurrencyLimit(t *testing.T) {
	var active int32
	var maxActive int32
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&active, 1)
		for {
			max := atomic.LoadInt32(&maxActive)
			if current <= max {
				break
			}
			if atomic.CompareAndSwapInt32(&maxActive, max, current) {
				break
			}
		}

		<-block
		atomic.AddInt32(&active, -1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	outputDir := t.TempDir()
	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     2 * time.Second,
		UserAgent:   "test-agent",
		Concurrency: 2,
	}, nil)

	items := []Downloadable{
		{PostID: "p1", Subreddit: "pics", Filename: "p1_1.jpg", URL: server.URL + "/1.jpg"},
		{PostID: "p2", Subreddit: "pics", Filename: "p2_1.jpg", URL: server.URL + "/2.jpg"},
		{PostID: "p3", Subreddit: "pics", Filename: "p3_1.jpg", URL: server.URL + "/3.jpg"},
		{PostID: "p4", Subreddit: "pics", Filename: "p4_1.jpg", URL: server.URL + "/4.jpg"},
	}

	done := make(chan error, 1)
	go func() {
		_, err := downloader.Download(context.Background(), items)
		done <- err
	}()

	waitForCondition(t, func() bool {
		return atomic.LoadInt32(&active) >= 2
	}, time.Second)
	close(block)

	if err := <-done; err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if max := atomic.LoadInt32(&maxActive); max > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", max)
	}
}

func TestDeduplication_SkipsExistingHash(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	existingFilePath := filepath.Join(subredditDir, "existing_abc.jpg")
	existingContent := []byte("existing file content")
	if err := os.WriteFile(existingFilePath, existingContent, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	existingHash, err := CalculateFileHash(existingFilePath)
	if err != nil {
		t.Fatalf("CalculateFileHash error = %v", err)
	}

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	existingPost := &storage.Post{
		ID:           "existing",
		DownloadedAt: time.Now(),
		Hash:         existingHash,
	}
	if err := db.SavePost(ctx, existingPost); err != nil {
		t.Fatalf("SavePost error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("new content"))
	}))
	defer server.Close()

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "abc",
		Subreddit: "pics",
		Filename:  "abc_1.jpg",
		URL:       server.URL + "/new.jpg",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if hashes["abc"] != "" {
		t.Errorf("Expected empty hash (skipped), got %s", hashes["abc"])
	}

	newFilePath := filepath.Join(subredditDir, "abc_1.jpg")
	if _, err := os.Stat(newFilePath); err == nil {
		t.Error("New file should not exist when hash exists")
	}

	if _, err := os.Stat(existingFilePath); err != nil {
		t.Errorf("Existing file should remain: %v", err)
	}
}

func TestDeduplication_KeepsFileOnDBError(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("downloaded content"))
	}))
	defer server.Close()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB error = %v", err)
	}
	defer db.Close()

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "newpost",
		Subreddit: "pics",
		Filename:  "newpost_1.jpg",
		URL:       server.URL + "/new.jpg",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	newFilePath := filepath.Join(subredditDir, "newpost_1.jpg")
	if _, err := os.Stat(newFilePath); err != nil {
		t.Errorf("New file should exist: %v", err)
	}

	if hashes["newpost"] == "" {
		t.Error("Hash should be returned for new file")
	}
}

func TestDeduplication_NewHashSaved(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("unique content for new hash"))
	}))
	defer server.Close()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB error = %v", err)
	}
	defer db.Close()

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "uniquepost",
		Subreddit: "pics",
		Filename:  "uniquepost_1.jpg",
		URL:       server.URL + "/unique.jpg",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if hashes["uniquepost"] == "" {
		t.Error("Hash should be returned for new file")
	}

	// Verify hash format is correct (64 character hex string)
	if len(hashes["uniquepost"]) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hashes["uniquepost"]))
	}

	// Verify file exists
	destFilePath := filepath.Join(subredditDir, "uniquepost_1.jpg")
	if _, err := os.Stat(destFilePath); err != nil {
		t.Errorf("Downloaded file should exist: %v", err)
	}
}

func TestDeduplication_IdenticalContent(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	existingFilePath := filepath.Join(subredditDir, "original_abc.jpg")
	sharedContent := []byte("shared identical content")
	if err := os.WriteFile(existingFilePath, sharedContent, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	existingHash, err := CalculateFileHash(existingFilePath)
	if err != nil {
		t.Fatalf("CalculateFileHash error = %v", err)
	}

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	existingPost := &storage.Post{
		ID:           "existing",
		DownloadedAt: time.Now(),
		Hash:         existingHash,
	}
	if err := db.SavePost(ctx, existingPost); err != nil {
		t.Fatalf("SavePost error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(sharedContent)
	}))
	defer server.Close()

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "duplicate",
		Subreddit: "pics",
		Filename:  "duplicate_1.jpg",
		URL:       server.URL + "/duplicate.jpg",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if hashes["duplicate"] != "" {
		t.Errorf("Expected empty hash (skipped), got %s", hashes["duplicate"])
	}

	newFilePath := filepath.Join(subredditDir, "duplicate_1.jpg")
	if _, err := os.Stat(newFilePath); err == nil {
		t.Error("New file should not exist for identical content")
	}
}

func TestHashCalculation_Integration(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "testsub")
	if err := os.MkdirAll(subredditDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	testContent := []byte("content for hash test")
	testFilePath := filepath.Join(subredditDir, "testfile.jpg")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	hash, err := CalculateFileHash(testFilePath)
	if err != nil {
		t.Fatalf("CalculateFileHash error = %v", err)
	}

	if len(hash) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash))
	}

	hash2, err := CalculateFileHash(testFilePath)
	if err != nil {
		t.Fatalf("CalculateFileHash error = %v", err)
	}

	if hash != hash2 {
		t.Error("Hash should be deterministic")
	}

	hashFromBytes, err := CalculateHashFromReader(bytes.NewReader(testContent))
	if err != nil {
		t.Fatalf("CalculateHashFromReader error = %v", err)
	}

	if hash != hashFromBytes {
		t.Error("File hash and reader hash should match for same content")
	}
}
