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

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validJPEGData() []byte {
	data := make([]byte, 1024)
	data[0] = 0xFF
	data[1] = 0xD8
	data[2] = 0xFF
	for i := 3; i < len(data); i++ {
		data[i] = byte(i % 256)
	}
	return data
}

func validMP4Data() []byte {
	data := make([]byte, 1024)
	// MP4 magic bytes: at offset 4, bytes should be "ftyp"
	data[4] = 'f'
	data[5] = 't'
	data[6] = 'y'
	data[7] = 'p'
	// Fill rest with some content
	for i := 8; i < len(data); i++ {
		data[i] = byte(i % 256)
	}
	return data
}

func validWebMData() []byte {
	data := make([]byte, 1024)
	// WebM magic bytes: EBML header at offset 0
	data[0] = 0x1A
	data[1] = 0x45
	data[2] = 0xDF
	data[3] = 0xA3
	// Fill rest with some content
	for i := 4; i < len(data); i++ {
		data[i] = byte(i % 256)
	}
	return data
}

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
	if items[0].Filename != "untitled_1_gal123.jpg" {
		t.Errorf("Filename = %s, want untitled_1_gal123.jpg", items[0].Filename)
	}
	if items[1].Filename != "untitled_2_gal123.png" {
		t.Errorf("Filename = %s, want untitled_2_gal123.png", items[1].Filename)
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
	// Use proper bdfr-html filename pattern: {POSTID}.ext (POSTID must be 6+ chars)
	// Create a valid JPEG file (at least 1KB) to test validation
	bdfrStyleFilePath := filepath.Join(subredditDir, "abc123.jpg")
	// Valid JPEG magic bytes: 0xFF 0xD8 0xFF
	validContent := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}
	// Pad to at least 1KB
	validContent = append(validContent, make([]byte, 1024-len(validContent))...)
	if err := os.WriteFile(bdfrStyleFilePath, validContent, 0644); err != nil {
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
		PostID:    "abc123",
		Subreddit: "pics",
		Filename:  "abc123_1.jpg",
		URL:       "https://example.com/abc123.jpg",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	// Verify the file was skipped (not re-downloaded)
	if hashes["abc123"] == "" {
		t.Error("Expected file to be skipped, but hash is empty")
	}
}

func TestDownloaderRetries(t *testing.T) {
	validData := validJPEGData()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&calls, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(validData)
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
	validData := validJPEGData()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(validData)
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
	validData := validJPEGData()
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
		w.Write(validData)
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

type dedupTestSetup struct {
	outputDir    string
	subredditDir string
	db           *storage.DB
	server       *httptest.Server
	downloader   *Downloader
	existingFile string
	existingHash string
}

func setupDeduplicationTest(t *testing.T, serverContent []byte) *dedupTestSetup {
	t.Helper()

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755), "MkdirAll error")

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(dbPath)
	require.NoError(t, err, "NewDB error")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(serverContent)
	}))

	d := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	return &dedupTestSetup{
		outputDir:    outputDir,
		subredditDir: subredditDir,
		db:           db,
		server:       server,
		downloader:   d,
	}
}

func (s *dedupTestSetup) cleanup() {
	s.server.Close()
	s.db.Close()
}

func (s *dedupTestSetup) createExistingFile(t *testing.T, filename string, content []byte, postID string) string {
	t.Helper()

	existingFilePath := filepath.Join(s.subredditDir, filename)
	require.NoError(t, os.WriteFile(existingFilePath, content, 0644), "WriteFile error")

	hash, err := CalculateFileHash(existingFilePath)
	require.NoError(t, err, "CalculateFileHash error")

	if postID != "" {
		existingPost := &storage.Post{
			ID:           postID,
			DownloadedAt: time.Now(),
			Hash:         hash,
		}
		require.NoError(t, s.db.SavePost(context.Background(), existingPost), "SavePost error")
	}

	return hash
}

func TestDeduplication(t *testing.T) {
	validData := validJPEGData()
	uniqueData := make([]byte, 1024)
	copy(uniqueData, validData)
	uniqueData[100] = 0xAB

	tests := []struct {
		name                string
		serverContent       []byte
		existingFile        bool
		existingFileContent []byte
		existingFilename    string
		existingPostID      string
		newPostID           string
		newFilename         string
		wantEmptyHash       bool
		wantFileExists      bool
		wantExistingFile    bool
		checkHashLength     bool
		triggerDBError      bool
		wantError           bool
	}{
		{
			name:                "SkipsExistingHash",
			serverContent:       validData,
			existingFile:        true,
			existingFileContent: validData,
			existingFilename:    "existing_abc.jpg",
			existingPostID:      "existing",
			newPostID:           "abc",
			newFilename:         "abc_1.jpg",
			wantEmptyHash:       true,
			wantFileExists:      false,
			wantExistingFile:    true,
		},
		{
			name:           "KeepsFileOnDBError",
			serverContent:  validData,
			existingFile:   false,
			newPostID:      "newpost",
			newFilename:    "newpost_1.jpg",
			wantEmptyHash:  true,
			wantFileExists: true,
			triggerDBError: true,
			wantError:      true,
		},
		{
			name:            "NewHashSaved",
			serverContent:   uniqueData,
			existingFile:    false,
			newPostID:       "uniquepost",
			newFilename:     "uniquepost_1.jpg",
			wantEmptyHash:   false,
			wantFileExists:  true,
			checkHashLength: true,
		},
		{
			name:                "IdenticalContent",
			serverContent:       validData,
			existingFile:        true,
			existingFileContent: validData,
			existingFilename:    "original_abc.jpg",
			existingPostID:      "existing",
			newPostID:           "duplicate",
			newFilename:         "duplicate_1.jpg",
			wantEmptyHash:       true,
			wantFileExists:      false,
			wantExistingFile:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup := setupDeduplicationTest(t, tt.serverContent)
			defer setup.cleanup()

			var existingFilePath string
			if tt.existingFile {
				setup.createExistingFile(t, tt.existingFilename, tt.existingFileContent, tt.existingPostID)
				existingFilePath = filepath.Join(setup.subredditDir, tt.existingFilename)
			}

			items := []Downloadable{{
				PostID:    tt.newPostID,
				Subreddit: "pics",
				Filename:  tt.newFilename,
				URL:       setup.server.URL + "/download.jpg",
			}}

			if tt.triggerDBError {
				setup.db.Close()
			}

			hashes, err := setup.downloader.Download(context.Background(), items)
			require.Equal(t, tt.wantError, err != nil, "Download() error mismatch")
			if !tt.wantError {
				require.NoError(t, err, "Download() error")
			}

			if tt.wantEmptyHash {
				if tt.triggerDBError {
					assert.Empty(t, hashes[tt.newPostID], "Expected empty hash (error)")
				} else {
					hash := hashes[tt.newPostID]
					assert.NotEmpty(t, hash, "Hash should be marked with DUPLICATE prefix for duplicates")
					assert.True(t, strings.HasPrefix(hash, "DUPLICATE:"), "Expected hash to start with DUPLICATE: prefix, got %s", hash)
				}
			} else {
				assert.NotEmpty(t, hashes[tt.newPostID], "Hash should be returned for new file")
				assert.False(t, strings.HasPrefix(hashes[tt.newPostID], "DUPLICATE:"), "Hash should not be marked as duplicate for new file")
			}

			if tt.checkHashLength {
				hash := hashes[tt.newPostID]
				expectedLen := 64
				if strings.HasPrefix(hash, "DUPLICATE:") {
					expectedLen = 75
				}
				assert.Equal(t, expectedLen, len(hash), "Expected hash length %d, got %d (hash: %s)", expectedLen, len(hash), hash)
			}

			newFilePath := filepath.Join(setup.subredditDir, tt.newFilename)
			_, err = os.Stat(newFilePath)
			fileExists := err == nil

			assert.Equal(t, tt.wantFileExists, fileExists, "New file existence mismatch")
			if tt.wantExistingFile && existingFilePath != "" {
				_, err := os.Stat(existingFilePath)
				assert.NoError(t, err, "Existing file should remain")
			}
		})
	}
}

func TestHashCalculation_Integration(t *testing.T) {
	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "testsub")
	require.NoError(t, os.MkdirAll(subredditDir, 0755), "MkdirAll error")

	testContent := []byte("content for hash test")
	testFilePath := filepath.Join(subredditDir, "testfile.jpg")
	require.NoError(t, os.WriteFile(testFilePath, testContent, 0644), "WriteFile error")

	hash, err := CalculateFileHash(testFilePath)
	require.NoError(t, err, "CalculateFileHash error")

	assert.Equal(t, 64, len(hash), "Expected hash length 64")

	hash2, err := CalculateFileHash(testFilePath)
	require.NoError(t, err, "CalculateFileHash error")

	assert.Equal(t, hash, hash2, "Hash should be deterministic")

	hashFromBytes, err := CalculateHashFromReader(bytes.NewReader(testContent))
	require.NoError(t, err, "CalculateHashFromReader error")

	assert.Equal(t, hash, hashFromBytes, "File hash and reader hash should match for same content")
}

func TestDownloadValidationAndRetryBehavior(t *testing.T) {
	smallData := []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p'}
	pngData := make([]byte, 1024)
	pngData[0] = 0x89
	pngData[1] = 0x50
	pngData[2] = 0x4E
	pngData[3] = 0x47

	tests := []struct {
		name                  string
		payload               []byte
		contentType           string
		statusCode            int
		retries               int
		expectedErrorContains string
		expectFileExists      bool
		expectedCalls         int32
		expectHash            bool
	}{
		{
			name: "RejectsHTML",
			payload: func() []byte {
				html := `<!DOCTYPE html><html><head><title>Test</title></head><body>Not a video</body></html>`
				padding := make([]byte, 1024-len(html))
				return append([]byte(html), padding...)
			}(),
			contentType:           "text/html; charset=utf-8",
			statusCode:            http.StatusOK,
			retries:               3,
			expectedErrorContains: "HTML",
			expectFileExists:      false,
			expectedCalls:         1,
			expectHash:            false,
		},
		{
			name:                  "RejectsSmallFile",
			payload:               smallData,
			statusCode:            http.StatusOK,
			retries:               1,
			expectedErrorContains: "too small",
			expectFileExists:      false,
			expectedCalls:         1,
			expectHash:            false,
		},
		{
			name:                  "RejectsWrongMagicBytes",
			payload:               pngData,
			statusCode:            http.StatusOK,
			retries:               1,
			expectedErrorContains: "magic bytes",
			expectFileExists:      false,
			expectedCalls:         1,
			expectHash:            false,
		},
		{
			name:             "AcceptsValidMP4",
			payload:          validMP4Data(),
			contentType:      "video/mp4",
			statusCode:       http.StatusOK,
			retries:          1,
			expectFileExists: true,
			expectedCalls:    1,
			expectHash:       true,
		},
		{
			name:             "AcceptsValidWebM",
			payload:          validWebMData(),
			contentType:      "video/webm",
			statusCode:       http.StatusOK,
			retries:          1,
			expectFileExists: true,
			expectedCalls:    1,
			expectHash:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.statusCode)
				w.Write(tc.payload)
			}))
			defer server.Close()

			outputDir := t.TempDir()
			downloader := NewDownloader(Config{
				OutputDir:   outputDir,
				HTTPClient:  server.Client(),
				Retries:     tc.retries,
				BackoffBase: time.Millisecond,
				Timeout:     time.Second,
				UserAgent:   "test-agent",
				Concurrency: 1,
			}, nil)

			postID := strings.ToLower(tc.name)
			ext := ".mp4"
			if tc.contentType == "video/webm" {
				ext = ".webm"
			}
			filename := postID + "_1" + ext
			items := []Downloadable{{
				PostID:    postID,
				Subreddit: "pics",
				Filename:  filename,
				URL:       server.URL + "/file" + ext,
			}}

			hashes, err := downloader.Download(context.Background(), items)

			if tc.expectedErrorContains != "" {
				require.Error(t, err, "Download should fail")
				assert.Contains(t, err.Error(), tc.expectedErrorContains, "Error should contain expected message")
			} else {
				require.NoError(t, err, "Download should succeed")
			}

			filePath := filepath.Join(outputDir, "pics", filename)
			_, statErr := os.Stat(filePath)
			fileExists := statErr == nil
			assert.Equal(t, tc.expectFileExists, fileExists, "File existence mismatch")

			if tc.expectedCalls > 0 {
				assert.Equal(t, tc.expectedCalls, atomic.LoadInt32(&calls), "HTTP call count mismatch")
			}

			if tc.expectHash {
				assert.NotEmpty(t, hashes[postID], "Hash should be returned")
			}
		})
	}

	t.Run("RetriesOnTransientError", func(t *testing.T) {
		validData := validMP4Data()
		var calls int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&calls, 1)
			if count == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			w.Write(validData)
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
			PostID:    "retrytransient",
			Subreddit: "pics",
			Filename:  "retrytransient_1.mp4",
			URL:       server.URL + "/video.mp4",
		}}

		hashes, err := downloader.Download(context.Background(), items)
		require.NoError(t, err, "Download should succeed after retry")
		require.Equal(t, int32(2), atomic.LoadInt32(&calls), "Should have made 2 requests")

		filePath := filepath.Join(outputDir, "pics", "retrytransient_1.mp4")
		_, statErr := os.Stat(filePath)
		require.NoError(t, statErr, "File should exist after successful retry")
		assert.NotEmpty(t, hashes["retrytransient"], "Hash should be returned")
	})

	t.Run("PermanentSkipOnValidationError", func(t *testing.T) {
		var calls int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `<!DOCTYPE html><html><body>HTML content</body></html>`)
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
			PostID:    "permanent",
			Subreddit: "pics",
			Filename:  "permanent_1.mp4",
			URL:       server.URL + "/video.mp4",
		}}

		_, err := downloader.Download(context.Background(), items)
		require.Error(t, err, "Download should fail")
		require.Equal(t, int32(1), atomic.LoadInt32(&calls), "Should only make 1 request (no retries for validation error)")
	})

	t.Run("ChunkedResponseRejectsSmallFile", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(smallData)
		}))
		defer server.Close()

		outputDir := t.TempDir()
		downloader := NewDownloader(Config{
			OutputDir:   outputDir,
			HTTPClient:  server.Client(),
			Retries:     1,
			BackoffBase: time.Millisecond,
			Timeout:     time.Second,
			UserAgent:   "test-agent",
			Concurrency: 1,
		}, nil)

		items := []Downloadable{{
			PostID:    "smallchunked",
			Subreddit: "pics",
			Filename:  "smallchunked_1.mp4",
			URL:       server.URL + "/small.mp4",
		}}

		_, err := downloader.Download(context.Background(), items)
		require.Error(t, err, "Download should fail for small file without Content-Length")
		assert.Contains(t, err.Error(), "too small", "Error should mention file size")

		filePath := filepath.Join(outputDir, "pics", "smallchunked_1.mp4")
		_, statErr := os.Stat(filePath)
		require.True(t, os.IsNotExist(statErr), "Small file should not be created for chunked response")
	})
}

func TestDetectsCorruptExistingFile(t *testing.T) {
	validData := validMP4Data()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write(validData)
	}))
	defer server.Close()

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755))

	// Create corrupt file (HTML content) that should be detected and replaced
	corruptContent := []byte(`<!DOCTYPE html><html><body>This is HTML, not a video</body></html>`)
	corruptContent = append(corruptContent, make([]byte, 1024-len(corruptContent))...)
	existingFile := filepath.Join(subredditDir, "corrupttest_1.mp4")
	require.NoError(t, os.WriteFile(existingFile, corruptContent, 0644))

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		BackoffBase: time.Millisecond,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, nil)

	items := []Downloadable{{
		PostID:    "corrupttest",
		Subreddit: "pics",
		Filename:  "corrupttest_1.mp4",
		URL:       server.URL + "/video.mp4",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	require.NoError(t, err, "Download should succeed after replacing corrupt file")

	// Verify the new file exists and is valid
	newFilePath := filepath.Join(subredditDir, "corrupttest_1.mp4")
	_, statErr := os.Stat(newFilePath)
	require.NoError(t, statErr, "Valid file should exist after re-download")

	// Verify content is valid MP4
	content, err := os.ReadFile(newFilePath)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(content[4:8], []byte("ftyp")), "File should have valid MP4 signature")

	assert.NotEmpty(t, hashes["corrupttest"], "Hash should be returned")
}

func TestValidExistingFileSkipped(t *testing.T) {
	validData := validMP4Data()
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write(validData)
	}))
	defer server.Close()

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755))

	// Create valid existing file with proper POSTID pattern (6+ chars)
	existingFile := filepath.Join(subredditDir, "existingvalid_123456.mp4")
	require.NoError(t, os.WriteFile(existingFile, validData, 0644))

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		BackoffBase: time.Millisecond,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, nil)

	items := []Downloadable{{
		PostID:    "existingvalid",
		Subreddit: "pics",
		Filename:  "existingvalid_1.mp4",
		URL:       server.URL + "/video.mp4",
	}}

	hashes, err := downloader.Download(context.Background(), items)
	require.NoError(t, err, "Download should succeed")

	require.Equal(t, int32(0), requestCount, "Should not make any HTTP requests for existing valid file")

	hash := hashes["existingvalid"]
	require.NotEmpty(t, hash, "Hash should be returned for existing file")
	assert.False(t, strings.HasPrefix(hash, "DUPLICATE:"), "Local file reuse should NOT be marked as duplicate")
}
