package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/ownutil"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/reddit"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
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
		resp, err := base.RoundTrip(req)
		if err != nil {
			return nil, fmt.Errorf("round trip failed: %w", err)
		}
		return resp, nil
	}

	clone := req.Clone(req.Context())
	cloneURL := *req.URL
	cloneURL.Scheme = h.target.Scheme
	cloneURL.Host = h.target.Host
	clone.URL = &cloneURL
	clone.Host = req.URL.Host
	resp, err := base.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("round trip failed: %w", err)
	}
	return resp, nil
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
	post := reddit.Post{
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
	post := reddit.Post{
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
	post := reddit.Post{
		ID:        "abc",
		Subreddit: "videos",
		IsVideo:   true,
		Media: &reddit.Media{
			Video: &reddit.Video{DashURL: "https://v.redd.it/abc/DASHPlaylist.mpd"},
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintln(w, `<meta property="og:image" content="https://i.imgur.com/test.jpg">`)
	}))
	defer server.Close()

	client := newRewriteClient(server, "imgur.com")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.Post{
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
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := newRewriteClient(server, "api.redgifs.com")
	extractor := NewExtractor(client, "test-agent")
	post := reddit.Post{
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
	post := reddit.Post{
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
	validContent := make([]byte, 0, 11)
	validContent = append(validContent, 0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00)
	// Pad to at least 1KB
	validContent = append(validContent, make([]byte, 1024-len(validContent))...)
	if err := os.WriteFile(bdfrStyleFilePath, validContent, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
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
		ItemIndex: -1,
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := atomic.AddInt32(&calls, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validData)
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
		ItemIndex: -1,
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
		_, _ = w.Write(validData)
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
		{PostID: "fail", Subreddit: "pics", Filename: "fail_1.jpg", URL: server.URL + "/fail.jpg", ItemIndex: -1},
		{PostID: "ok", Subreddit: "pics", Filename: "ok_1.jpg", URL: server.URL + "/ok.jpg", ItemIndex: -1},
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := atomic.AddInt32(&active, 1)
		for {
			m := atomic.LoadInt32(&maxActive)
			if current <= m {
				break
			}
			if atomic.CompareAndSwapInt32(&maxActive, m, current) {
				break
			}
		}

		<-block
		atomic.AddInt32(&active, -1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validData)
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
		{PostID: "p1", Subreddit: "pics", Filename: "p1_1.jpg", URL: server.URL + "/1.jpg", ItemIndex: -1},
		{PostID: "p2", Subreddit: "pics", Filename: "p2_1.jpg", URL: server.URL + "/2.jpg", ItemIndex: -1},
		{PostID: "p3", Subreddit: "pics", Filename: "p3_1.jpg", URL: server.URL + "/3.jpg", ItemIndex: -1},
		{PostID: "p4", Subreddit: "pics", Filename: "p4_1.jpg", URL: server.URL + "/4.jpg", ItemIndex: -1},
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
	if m := atomic.LoadInt32(&maxActive); m > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", m)
	}
}

type dedupTestSetup struct {
	db           *storage.DB
	server       *httptest.Server
	downloader   *Downloader
	outputDir    string
	subredditDir string
}

func setupDeduplicationTest(t *testing.T, serverContent []byte) *dedupTestSetup {
	t.Helper()

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755), "MkdirAll error")

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(context.Background(), dbPath, &ownutil.Owner{})
	require.NoError(t, err, "NewDB error")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(serverContent)
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
	_ = s.db.Close()
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
		serverContent       []byte
		existingFileContent []byte
		name                string
		existingFilename    string
		existingPostID      string
		newPostID           string
		newFilename         string
		existingFile        bool
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
				ItemIndex: -1,
			}}

			if tt.triggerDBError {
				_ = setup.db.Close()
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
					assert.NotEmpty(t, hash, "Hash should be returned for duplicates")
					assert.Len(t, hash, 64, "Expected raw hash length 64 for duplicates")
					assert.Equal(t, "true", hashes[tt.newPostID+"_duplicate"], "Expected duplicate marker to be set")
				}
			} else {
				assert.NotEmpty(t, hashes[tt.newPostID], "Hash should be returned for new file")
				assert.Empty(t, hashes[tt.newPostID+"_duplicate"], "Hash should not be marked as duplicate for new file")
			}

			if tt.checkHashLength {
				hash := hashes[tt.newPostID]
				assert.Len(t, hash, 64, "Expected hash length 64, got %d (hash: %s)", len(hash), hash)
			}

			newFilePath := filepath.Join(setup.subredditDir, tt.newFilename)
			_, err = os.Stat(newFilePath)
			fileExists := err == nil

			assert.Equal(t, tt.wantFileExists, fileExists, "New file existence mismatch")
			if tt.wantExistingFile && existingFilePath != "" {
				_, err := os.Stat(existingFilePath)
				require.NoError(t, err, "Existing file should remain")
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

	assert.Len(t, hash, 64, "Expected hash length 64")

	hash2, err := CalculateFileHash(testFilePath)
	require.NoError(t, err, "CalculateFileHash error")

	assert.Equal(t, hash, hash2, "Hash should be deterministic")

	hashFromBytes, err := CalculateHashFromReader(bytes.NewReader(testContent))
	require.NoError(t, err, "CalculateHashFromReader error")

	assert.Equal(t, hash, hashFromBytes, "File hash and reader hash should match for same content")
}

// TestItemHashKey verifies that itemHashKey generates correct keys for single items and gallery items.
func TestItemHashKey(t *testing.T) {
	tests := []struct {
		item    Downloadable
		name    string
		wantKey string
	}{
		{
			name: "SingleItem",
			item: Downloadable{
				PostID:    "post123",
				Filename:  "title_post123.jpg",
				ItemIndex: -1,
			},
			wantKey: "post123",
		},
		{
			name: "GalleryItemFirst",
			item: Downloadable{
				PostID:    "gal456",
				Filename:  "title_1_gal456.jpg",
				ItemIndex: 1, // 1-based indexing
			},
			wantKey: "gal456_1",
		},
		{
			name: "GalleryItemSecond",
			item: Downloadable{
				PostID:    "gal456",
				Filename:  "title_2_gal456.jpg",
				ItemIndex: 2, // 1-based indexing
			},
			wantKey: "gal456_2",
		},
		{
			name: "GalleryItemTenth",
			item: Downloadable{
				PostID:    "gal789",
				Filename:  "title_10_gal789.jpg",
				ItemIndex: 10, // 1-based indexing
			},
			wantKey: "gal789_10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey := itemHashKey(tt.item)
			assert.Equal(t, tt.wantKey, gotKey)
		})
	}
}

func TestDownloadValidationAndRetryBehavior(t *testing.T) {
	smallData := []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p'}
	pngData := make([]byte, 1024)
	pngData[0] = 0x89
	pngData[1] = 0x50
	pngData[2] = 0x4E
	pngData[3] = 0x47

	tests := []struct {
		payload               []byte
		name                  string
		contentType           string
		expectedErrorContains string
		statusCode            int
		retries               int
		expectedCalls         int32
		expectFileExists      bool
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write(tc.payload)
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
				ItemIndex: -1,
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

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			count := atomic.AddInt32(&calls, 1)
			if count == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(validData)
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
			ItemIndex: -1,
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
		t.Run("HeaderBased", func(t *testing.T) {
			var calls int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, `<!DOCTYPE html><html><body>HTML content</body></html>`)
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
				ItemIndex: -1,
			}}

			_, err := downloader.Download(context.Background(), items)
			require.Error(t, err, "Download should fail")
			require.Equal(t, int32(1), atomic.LoadInt32(&calls), "Should only make 1 request (no retries for validation error)")
		})

		t.Run("BodyBased", func(t *testing.T) {
			var calls int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "video/mp4")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, `<!DOCTYPE html><html><body>HTML content disguised as video</body></html>`)
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
				PostID:    "bodycheck",
				Subreddit: "pics",
				Filename:  "bodycheck_1.mp4",
				URL:       server.URL + "/video.mp4",
				ItemIndex: -1,
			}}

			_, err := downloader.Download(context.Background(), items)
			require.Error(t, err, "Download should fail due to HTML content in body")
			require.Equal(t, int32(1), atomic.LoadInt32(&calls), "Should only make 1 request (no retries for validation error)")
		})
	})

	t.Run("ChunkedResponseRejectsSmallFile", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			// Force chunked encoding by flushing headers before writing body
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			// Write in chunks to ensure Content-Length isn't set
			_, _ = w.Write(smallData[:4])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = w.Write(smallData[4:])
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
			ItemIndex: -1,
		}}

		_, err := downloader.Download(context.Background(), items)
		require.Error(t, err, "Download should fail for small file without Content-Length")
		assert.Contains(t, err.Error(), "too small", "Error should mention file size")

		filePath := filepath.Join(outputDir, "pics", "smallchunked_1.mp4")
		_, statErr := os.Stat(filePath)
		require.True(t, os.IsNotExist(statErr), "Small file should not be created for chunked response")
	})

	t.Run("EmptyBodyValidation", func(t *testing.T) {
		var requestCount int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requestCount, 1)
			// Return HTTP 200 with Content-Type but zero-length body
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			// Explicitly write nothing to body
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
			PostID:    "emptybody",
			Subreddit: "pics",
			Filename:  "emptybody_1.mp4",
			URL:       server.URL + "/video.mp4",
			ItemIndex: -1,
		}}

		hashes, err := downloader.Download(context.Background(), items)
		require.Error(t, err, "Download should fail for empty body")
		assert.Contains(t, err.Error(), "empty response", "Error should mention empty response")

		// Server should only be hit once (no retries for permanent validation error)
		require.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "Should only make 1 request (no retries for validation error)")

		// No file should be created
		filePath := filepath.Join(outputDir, "pics", "emptybody_1.mp4")
		_, statErr := os.Stat(filePath)
		require.True(t, os.IsNotExist(statErr), "No file should be created for empty response")

		// No hash should be returned
		assert.Empty(t, hashes["emptybody"], "No hash should be returned for empty body")
	})
}

func TestDetectsCorruptExistingFile(t *testing.T) {
	validData := validMP4Data()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validData)
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
		ItemIndex: -1,
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
		_, _ = w.Write(validData)
	}))
	defer server.Close()

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755))

	// Create valid existing file with proper POSTID pattern (6+ chars)
	// The POSTID must be the last 6+ alphanumeric segment before extension
	existingFile := filepath.Join(subredditDir, "my_video_abc123.mp4")
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
		PostID:    "abc123",
		Subreddit: "pics",
		Filename:  "my_video_abc123.mp4",
		URL:       server.URL + "/video.mp4",
		ItemIndex: -1,
	}}

	hashes, err := downloader.Download(context.Background(), items)
	require.NoError(t, err, "Download should succeed")

	require.Equal(t, int32(0), requestCount, "Should not make any HTTP requests for existing valid file")

	hash := hashes["abc123"]
	require.NotEmpty(t, hash, "Hash should be returned for existing file")
	assert.Empty(t, hashes["abc123_duplicate"], "Local file reuse should NOT be marked as duplicate")
}

func TestKnownBadHashDetection(t *testing.T) {
	validTestData := make([]byte, 1024)
	validTestData[4] = 'f'
	validTestData[5] = 't'
	validTestData[6] = 'y'
	validTestData[7] = 'p'

	for i := 8; i < len(validTestData); i++ {
		validTestData[i] = byte(i % 256)
	}

	tempValidFile := filepath.Join(t.TempDir(), "valid.mp4")
	require.NoError(t, os.WriteFile(tempValidFile, validTestData, 0644))
	actualHash, err := CalculateFileHash(tempValidFile)
	require.NoError(t, err)

	knownBadHashesMu.Lock()
	defer knownBadHashesMu.Unlock()

	originalBadHashes := make(map[string]bool)
	for k, v := range knownBadHashes {
		originalBadHashes[k] = v
	}
	defer func() {
		knownBadHashes = originalBadHashes
	}()

	knownBadHashes[actualHash] = true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validTestData)
	}))
	defer server.Close()

	outputDir := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(context.Background(), dbPath, &ownutil.Owner{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Save the post to the database first (required for IncrementRetry to work)
	err = db.SavePost(context.Background(), &storage.Post{
		ID:        "badhash123",
		Title:     "Test Post",
		Subreddit: "pics",
	})
	require.NoError(t, err)

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  server.Client(),
		Retries:     1,
		BackoffBase: time.Millisecond,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "badhash123",
		Subreddit: "pics",
		Filename:  "badhash123_1.mp4",
		URL:       server.URL + "/video.mp4",
		ItemIndex: -1,
	}}

	hashes, downloadErr := downloader.Download(context.Background(), items)

	require.Error(t, downloadErr, "Download should fail for known bad hash")
	assert.Empty(t, hashes["badhash123"], "Hash should be empty for rejected file")

	filePath := filepath.Join(outputDir, "pics", "badhash123_1.mp4")
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "File with bad hash should be removed")

	post, err := db.GetPost(context.Background(), "badhash123")
	require.NoError(t, err)
	require.NotNil(t, post)
	assert.Equal(t, 1, post.RetryCount, "Retry count should be 1 after bad hash detection")
	assert.Equal(t, errReasonKnownBadHash, post.LastError)

	var valErr ValidationError
	require.ErrorAs(t, downloadErr, &valErr, "Download error should be a ValidationError")
	assert.True(t, valErr.Permanent, "ValidationError should be permanent")
	assert.Equal(t, errReasonKnownBadHash, valErr.Reason)
}

func TestKnownBadHashDetection_ExistingFile(t *testing.T) {
	existingFileContent := make([]byte, 1024)
	existingFileContent[4] = 'f'
	existingFileContent[5] = 't'
	existingFileContent[6] = 'y'
	existingFileContent[7] = 'p'

	for i := 8; i < len(existingFileContent); i++ {
		existingFileContent[i] = byte(i % 256)
	}

	outputDir := t.TempDir()
	subredditDir := filepath.Join(outputDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755))

	existingFilePath := filepath.Join(subredditDir, "existingbad_1.mp4")
	require.NoError(t, os.WriteFile(existingFilePath, existingFileContent, 0644))

	badHash, err := CalculateFileHash(existingFilePath)
	require.NoError(t, err)

	knownBadHashesMu.Lock()
	defer knownBadHashesMu.Unlock()

	originalBadHashes := make(map[string]bool)
	for k, v := range knownBadHashes {
		originalBadHashes[k] = v
	}
	defer func() {
		knownBadHashes = originalBadHashes
	}()

	knownBadHashes[badHash] = true

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(context.Background(), dbPath, &ownutil.Owner{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	err = db.SavePost(context.Background(), &storage.Post{
		ID:        "existingbad",
		Title:     "Test Post",
		Subreddit: "pics",
	})
	require.NoError(t, err)

	downloader := NewDownloader(Config{
		OutputDir:   outputDir,
		HTTPClient:  &http.Client{},
		Retries:     1,
		BackoffBase: time.Millisecond,
		Timeout:     time.Second,
		UserAgent:   "test-agent",
		Concurrency: 1,
	}, db)

	items := []Downloadable{{
		PostID:    "existingbad",
		Subreddit: "pics",
		Filename:  "existingbad_1.mp4",
		URL:       "http://example.com/video.mp4",
		ItemIndex: -1,
	}}

	hashes, downloadErr := downloader.Download(context.Background(), items)

	require.Error(t, downloadErr, "Download should fail for known bad hash on existing file")
	assert.Empty(t, hashes["existingbad"], "Hash should be empty for rejected file")

	_, statErr := os.Stat(existingFilePath)
	assert.True(t, os.IsNotExist(statErr), "Existing file with bad hash should be removed")

	post, err := db.GetPost(context.Background(), "existingbad")
	require.NoError(t, err)
	require.NotNil(t, post)
	assert.Equal(t, 1, post.RetryCount, "Retry count should be 1 after bad hash detection")
	assert.Equal(t, errReasonKnownBadHash, post.LastError)

	var valErr ValidationError
	require.ErrorAs(t, downloadErr, &valErr, "Download error should be a ValidationError")
	assert.True(t, valErr.Permanent, "ValidationError should be permanent")
	assert.Equal(t, errReasonKnownBadHash, valErr.Reason)
}

func TestErrRetryImmediately(t *testing.T) {
	validData := validJPEGData()
	corruptData := []byte(`<!DOCTYPE html><html><body>Not an image</body></html>`)
	corruptData = append(corruptData, make([]byte, 1024-len(corruptData))...)

	tests := []struct {
		name             string
		postID           string
		filename         string
		wantHashKey      string
		retries          int
		wantRequestCount int32
		setupFile        bool
		blockAndCorrupt  bool
	}{
		{
			name:             "BlockingFileCorruptRemoved",
			postID:           "retrytest",
			filename:         "retrytest_1.jpg",
			setupFile:        true,
			blockAndCorrupt:  true,
			retries:          3,
			wantRequestCount: 1,
			wantHashKey:      "retrytest",
		},
		{
			name:             "DownloadOncePath",
			postID:           "retryimmed",
			filename:         "retryimmed_1.jpg",
			setupFile:        false,
			blockAndCorrupt:  false,
			retries:          2,
			wantRequestCount: 1,
			wantHashKey:      "retryimmed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestCount int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)
				w.Header().Set("Content-Type", "image/jpeg")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(validData)
			}))
			defer server.Close()

			outputDir := t.TempDir()
			subredditDir := filepath.Join(outputDir, "pics")
			require.NoError(t, os.MkdirAll(subredditDir, 0755))

			targetFile := filepath.Join(subredditDir, tt.filename)

			if tt.blockAndCorrupt {
				require.NoError(t, os.WriteFile(targetFile, corruptData, 0644))
			}

			d := NewDownloader(Config{
				OutputDir:   outputDir,
				HTTPClient:  server.Client(),
				Retries:     tt.retries,
				BackoffBase: time.Millisecond,
				Timeout:     time.Second,
				UserAgent:   "test-agent",
				Concurrency: 1,
			}, nil)

			items := []Downloadable{{
				PostID:    tt.postID,
				Subreddit: "pics",
				Filename:  tt.filename,
				URL:       server.URL + "/image.jpg",
				ItemIndex: -1,
			}}

			hashes, err := d.Download(context.Background(), items)
			require.NoError(t, err, "Download should succeed")

			assert.Equal(t, tt.wantRequestCount, atomic.LoadInt32(&requestCount), "HTTP request count mismatch")

			content, readErr := os.ReadFile(targetFile)
			require.NoError(t, readErr, "Should be able to read file")
			assert.Equal(t, validData, content, "File should contain valid data")
			assert.NotEmpty(t, hashes[tt.wantHashKey], "Hash should be returned")
		})
	}
}

// TestCheckAndHandleExistingFile tests the checkAndHandleExistingFile function
func TestCheckAndHandleExistingFile(t *testing.T) {
	validData := validJPEGData()
	corruptData := []byte(`<!DOCTYPE html><html><body>Not an image</body></html>`)
	corruptData = append(corruptData, make([]byte, 1024-len(corruptData))...)

	tests := []struct {
		fileContent    []byte
		name           string
		fileExists     bool
		wantHash       bool
		wantLocalReuse bool
		wantRemoved    bool
		wantErr        bool
	}{
		{
			name:           "NoExistingFile",
			fileExists:     false,
			wantHash:       false,
			wantLocalReuse: false,
			wantRemoved:    false,
			wantErr:        false,
		},
		{
			name:           "ValidExistingFile",
			fileExists:     true,
			fileContent:    validData,
			wantHash:       true,
			wantLocalReuse: true,
			wantRemoved:    false,
			wantErr:        false,
		},
		{
			name:           "CorruptFileRemoved",
			fileExists:     true,
			fileContent:    corruptData,
			wantHash:       false,
			wantLocalReuse: false,
			wantRemoved:    true,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			subredditDir := filepath.Join(tempDir, "testsub")
			require.NoError(t, os.MkdirAll(subredditDir, 0755))

			// Use POSTID pattern: filename starting with 6+ char POSTID
			filePath := filepath.Join(subredditDir, "abcdef.jpg")

			d := NewDownloader(Config{
				OutputDir: tempDir,
				Retries:   1,
				Timeout:   time.Second,
				UserAgent: "test-agent",
			}, nil)

			if tt.fileExists {
				require.NoError(t, os.WriteFile(filePath, tt.fileContent, 0644))
			}

			hash, isLocalReuse, wasRemoved, err := d.checkAndHandleExistingFile(context.Background(), subredditDir, "abcdef", "abcdef.jpg")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.wantHash {
				assert.NotEmpty(t, hash, "hash should be returned")
			} else {
				assert.Empty(t, hash, "hash should be empty")
			}

			assert.Equal(t, tt.wantLocalReuse, isLocalReuse, "isLocalReuse mismatch")
			assert.Equal(t, tt.wantRemoved, wasRemoved, "wasRemoved mismatch")

			if tt.wantRemoved {
				_, statErr := os.Stat(filePath)
				assert.True(t, os.IsNotExist(statErr), "file should be removed")
			}
		})
	}
}

// TestCheckAndHandleExistingFile_KnownBadHash tests that checkAndHandleExistingFile
// properly detects and removes files with known bad hashes
func TestCheckAndHandleExistingFile_KnownBadHash(t *testing.T) {
	testData := make([]byte, 1024)
	testData[0] = 0xFF
	testData[1] = 0xD8
	testData[2] = 0xFF

	for i := 3; i < len(testData); i++ {
		testData[i] = byte(i % 256)
	}

	tempDir := t.TempDir()
	subredditDir := filepath.Join(tempDir, "pics")
	require.NoError(t, os.MkdirAll(subredditDir, 0755))

	filePath := filepath.Join(subredditDir, "badhash.jpg")
	require.NoError(t, os.WriteFile(filePath, testData, 0644))

	badHash, err := CalculateFileHash(filePath)
	require.NoError(t, err)

	// Lock and modify knownBadHashes - hold lock for entire test
	knownBadHashesMu.Lock()
	defer knownBadHashesMu.Unlock()
	originalBadHashes := make(map[string]bool)
	for k, v := range knownBadHashes {
		originalBadHashes[k] = v
	}
	knownBadHashes[badHash] = true

	defer func() {
		// Lock is already held by outer defer, just restore
		knownBadHashes = originalBadHashes
	}()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	db, err := storage.NewDB(context.Background(), dbPath, &ownutil.Owner{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Save post to database
	err = db.SavePost(context.Background(), &storage.Post{
		ID:        "badhash",
		Title:     "Test",
		Subreddit: "pics",
	})
	require.NoError(t, err)

	d := NewDownloader(Config{
		OutputDir: tempDir,
		Retries:   1,
		Timeout:   time.Second,
		UserAgent: "test-agent",
	}, db)

	hash, isLocalReuse, wasRemoved, err := d.checkAndHandleExistingFile(context.Background(), subredditDir, "badhash", "badhash.jpg")

	// Should return an error for known bad hash
	require.Error(t, err, "should error for known bad hash")
	assert.Empty(t, hash, "hash should be empty")
	assert.False(t, isLocalReuse, "should not be local reuse")
	assert.True(t, wasRemoved, "file should be marked as removed")

	// Verify file was removed
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "file with bad hash should be removed")

	// Check that error is a ValidationError with Permanent=true
	var valErr ValidationError
	require.ErrorAs(t, err, &valErr, "error should be a ValidationError")
	assert.True(t, valErr.Permanent, "ValidationError should be permanent")
	assert.Equal(t, errReasonKnownBadHash, valErr.Reason)
}

// TestDownloadTimerBasicFunctionality tests the basic functionality of downloadTimer
func TestDownloadTimerBasicFunctionality(t *testing.T) {
	tests := []struct {
		name       string
		delay      time.Duration
		waitBefore time.Duration
		wantNilErr bool
	}{
		{
			name:       "zero delay returns immediately",
			delay:      0,
			wantNilErr: true,
		},
		{
			name:       "negative delay returns immediately",
			delay:      -1 * time.Millisecond,
			wantNilErr: true,
		},
		{
			name:       "small delay with wait before returns immediately",
			delay:      1 * time.Millisecond,
			waitBefore: 10 * time.Millisecond,
			wantNilErr: true,
		},
		{
			name:       "small delay without wait before needs to wait",
			delay:      50 * time.Millisecond,
			waitBefore: 0,
			wantNilErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer := newDownloadTimer(tt.delay)
			ctx := context.Background()

			// If we need to wait before the first call, simulate it
			if tt.waitBefore > 0 {
				timer.lastStart = time.Now().Add(-tt.waitBefore)
			}

			err := timer.Wait(ctx)
			if tt.wantNilErr {
				require.NoError(t, err, "Wait should not return error")
			} else {
				require.Error(t, err, "Wait should return error")
			}
		})
	}
}

// TestDownloadTimerZeroDelay tests that Wait() with zero delay returns immediately
func TestDownloadTimerZeroDelay(t *testing.T) {
	timer := newDownloadTimer(0)
	ctx := context.Background()

	start := time.Now()
	err := timer.Wait(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err, "Wait should not return error for zero delay")
	assert.Less(t, elapsed, 10*time.Millisecond, "Wait should return immediately for zero delay")
}

// TestDownloadTimerCanceledContext tests that Wait() returns error when context is canceled
func TestDownloadTimerCanceledContext(t *testing.T) {
	timer := newDownloadTimer(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	// First call to set lastStart to now - this should return immediately
	err := timer.Wait(ctx)
	require.NoError(t, err, "First Wait should not return error")

	// Start waiting in a goroutine - this will have to wait
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- timer.Wait(ctx)
	}()

	// Wait for the goroutine to acquire the lock and start waiting
	time.Sleep(20 * time.Millisecond)

	// Cancel context while the goroutine is waiting
	cancel()

	// Wait for the Wait to return
	select {
	case err := <-waitErr:
		require.Error(t, err, "Wait should return error when context is canceled")
		require.Contains(t, err.Error(), "context canceled", "error should mention context canceled")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for Wait to return")
	}
}

// TestDownloadTimerConcurrencySafety tests that concurrent calls to Wait() are properly serialized
func TestDownloadTimerConcurrencySafety(t *testing.T) {
	// Use a reasonable delay that we can verify
	delay := 20 * time.Millisecond
	timer := newDownloadTimer(delay)
	ctx := context.Background()

	const numGoroutines = 5
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Track when each call completes
	finishTimes := make([]time.Time, numGoroutines)
	var mu sync.Mutex

	// Track errors from goroutines using a channel
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			if err := timer.Wait(ctx); err != nil {
				errChan <- fmt.Errorf("goroutine %d: %w", idx, err)
			}
			mu.Lock()
			finishTimes[idx] = time.Now()
			mu.Unlock()
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for any errors from goroutines
	for err := range errChan {
		require.NoError(t, err, "Wait should not return error")
	}

	// Verify that calls were properly spaced out
	// Sort finish times to check intervals
	sort.Slice(finishTimes, func(i, j int) bool {
		return finishTimes[i].Before(finishTimes[j])
	})

	// Check that the minimum interval between consecutive calls is close to delay
	for i := 1; i < len(finishTimes); i++ {
		interval := finishTimes[i].Sub(finishTimes[i-1])
		// Allow some tolerance for test environment variations
		assert.GreaterOrEqual(t, interval, delay/2, "calls should be spaced out by at least half the delay")
	}
}

// TestDownloadTimerMultipleGoroutinesSpacedExecution tests that multiple goroutines waiting
// result in spaced-out execution
func TestDownloadTimerMultipleGoroutinesSpacedExecution(t *testing.T) {
	// Use a larger delay to make spacing more obvious
	delay := 50 * time.Millisecond
	timer := newDownloadTimer(delay)
	ctx := context.Background()

	// Warm up the timer first so lastStart is set
	err := timer.Wait(ctx)
	require.NoError(t, err, "warmup Wait should not fail")

	const numGoroutines = 3
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Track when each call completes
	completionTimes := make([]time.Time, numGoroutines)
	var mu sync.Mutex

	// Track errors from goroutines using a channel
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			if err := timer.Wait(ctx); err != nil {
				errChan <- fmt.Errorf("goroutine %d: %w", idx, err)
			}
			mu.Lock()
			completionTimes[idx] = time.Now()
			mu.Unlock()
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for any errors from goroutines
	for err := range errChan {
		require.NoError(t, err, "Wait should not return error")
	}

	// Sort completion times to check intervals
	sort.Slice(completionTimes, func(i, j int) bool {
		return completionTimes[i].Before(completionTimes[j])
	})

	// Verify that the time span from first to last is at least (numGoroutines - 1) * delay
	if !completionTimes[numGoroutines-1].IsZero() && !completionTimes[0].IsZero() {
		totalSpan := completionTimes[numGoroutines-1].Sub(completionTimes[0])
		minExpected := time.Duration(numGoroutines-1) * delay
		// Allow 10% tolerance for timing variations
		tolerance := minExpected / 10
		assert.GreaterOrEqual(t, totalSpan, minExpected-tolerance,
			"total execution time should be at least (n-1) * delay for serialized execution")
	}
}

// TestDownloadTimerVerySmallDelay tests with very small delays (1ms)
func TestDownloadTimerVerySmallDelay(t *testing.T) {
	timer := newDownloadTimer(1 * time.Millisecond)
	ctx := context.Background()

	// First call should return quickly
	start := time.Now()
	err := timer.Wait(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err, "First Wait should not return error")
	assert.Less(t, elapsed, 50*time.Millisecond, "First call should complete quickly")

	// Second call immediately after should also be quick (delay already passed)
	start = time.Now()
	err = timer.Wait(ctx)
	elapsed = time.Since(start)
	require.NoError(t, err, "Second Wait should not return error")
	assert.Less(t, elapsed, 50*time.Millisecond, "Second call should also complete quickly when delay passed")
}

// TestDownloadTimerLargeDelay tests with large delays (1s)
func TestDownloadTimerLargeDelay(t *testing.T) {
	timer := newDownloadTimer(1 * time.Second)
	ctx := context.Background()

	// First call should return quickly
	start := time.Now()
	err := timer.Wait(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err, "First Wait should not return error")
	assert.Less(t, elapsed, 50*time.Millisecond, "First call should complete quickly")

	// Second call immediately should have to wait close to the full delay
	start = time.Now()
	err = timer.Wait(ctx)
	elapsed = time.Since(start)
	require.NoError(t, err, "Second Wait should not return error")
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond, "Second call should wait at least ~delay")
	assert.Less(t, elapsed, 1100*time.Millisecond, "Second call should not wait too long")
}

// TestDownloadDelayConfigIntegration tests that Config.DownloadDelay is properly passed to downloadTimer
func TestDownloadDelayConfigIntegration(t *testing.T) {
	tests := []struct {
		name           string
		downloadDelay  time.Duration
		wantTimerDelay time.Duration
	}{
		{
			name:           "zero delay creates no rate limiting",
			downloadDelay:  0,
			wantTimerDelay: 0,
		},
		{
			name:           "positive delay is passed through",
			downloadDelay:  200 * time.Millisecond,
			wantTimerDelay: 200 * time.Millisecond,
		},
		{
			name:           "small delay is passed through",
			downloadDelay:  1 * time.Millisecond,
			wantTimerDelay: 1 * time.Millisecond,
		},
		{
			name:           "large delay is passed through",
			downloadDelay:  5 * time.Second,
			wantTimerDelay: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config with specific DownloadDelay
			config := Config{
				HTTPClient:    &http.Client{},
				Logger:        slog.New(slog.NewTextHandler(os.Stdout, nil)),
				DownloadDelay: tt.downloadDelay,
			}

			// Apply defaults
			config = applyDefaults(config)

			// Create downloadTimer with the config's DownloadDelay
			timer := newDownloadTimer(config.DownloadDelay)

			// Verify the timer has the expected delay
			assert.Equal(t, tt.wantTimerDelay, timer.minDelay, "timer should have configured delay")
		})
	}
}

// TestDownloaderRespectsConfiguredDelay tests that the downloader respects the configured delay
func TestDownloaderRespectsConfiguredDelay(t *testing.T) {
	// Create a temporary directory for output
	tempDir := t.TempDir()

	// Create test server that responds quickly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(validMP4Data()); err != nil {
			t.Errorf("Failed to write response: %v", err)
			return
		}
	}))
	defer server.Close()

	// Create config with a significant delay
	delay := 100 * time.Millisecond
	config := Config{
		HTTPClient:    newRewriteClient(server),
		Logger:        slog.New(slog.NewTextHandler(os.Stdout, nil)),
		DownloadDelay: delay,
		OutputDir:     tempDir,
		Timeout:       30 * time.Second,
		Retries:       0, // No retries for simpler test
	}

	// Create a temporary database file path
	dbFile := filepath.Join(tempDir, "test.db")

	// Create database
	db, err := storage.NewDB(context.Background(), dbFile, &ownutil.Owner{})
	require.NoError(t, err, "NewDB should not fail")
	defer func() { _ = db.Close() }()

	// Create downloader
	d := NewDownloader(config, db)

	// Create a test item
	item := Downloadable{
		URL:       server.URL + "/test.mp4",
		PostID:    "test123",
		Subreddit: "test",
	}

	// Download multiple items and verify timing
	items := []Downloadable{item, item, item}
	for i := range items {
		items[i].PostID = fmt.Sprintf("test%d", i)
	}

	start := time.Now()
	hashes, err := d.Download(context.Background(), items)
	elapsed := time.Since(start)

	require.NoError(t, err, "Download should not fail")
	assert.NotEmpty(t, hashes, "hashes should not be empty")

	// With 3 items and delay of 100ms between starts, we expect at least 200ms total
	// (first starts immediately, second waits ~100ms, third waits ~100ms more)
	minExpected := time.Duration(len(items)-1) * delay
	assert.GreaterOrEqual(t, elapsed, minExpected,
		"download should take at least (n-1) * delay between item starts")
}

// TestDownloadDelayDefaultValue tests that DownloadDelay defaults to defaultDownloadDelay when negative
func TestDownloadDelayDefaultValue(t *testing.T) {
	config := Config{
		HTTPClient:    &http.Client{},
		Logger:        slog.New(slog.NewTextHandler(os.Stdout, nil)),
		DownloadDelay: -1, // Negative value should trigger default
	}

	// Apply defaults
	config = applyDefaults(config)

	// Verify default is applied
	assert.Equal(t, defaultDownloadDelay, config.DownloadDelay,
		"negative DownloadDelay should default to defaultDownloadDelay")
}

// TestExtractorRedditVideoCases consolidates all Reddit video extraction test scenarios
// into a single table-driven test to reduce duplication.
func TestExtractorRedditVideoCases(t *testing.T) {
	tests := []struct {
		name          string
		serverHandler http.HandlerFunc
		hosts         []string
		post          reddit.Post
		wantError     bool
		errorContains string
		expectedURL   string
		urlChecks     map[string]bool // map[substr]shouldContain
		wantNilItems  bool
		wantItemCount int
		wantMediaType string
	}{
		{
			name:          "FallbackURL_Priority",
			serverHandler: nil, // No server needed
			hosts:         []string{},
			post: reddit.Post{
				ID:        "video1",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/abc123",
				Media: &reddit.Media{
					Video: &reddit.Video{
						FallbackURL: "https://v.redd.it/abc123/DASH_720.mp4",
						DashURL:     "https://v.redd.it/abc123/DASHPlaylist.mpd",
					},
				},
			},
			wantError:     false,
			expectedURL:   "https://v.redd.it/abc123/DASH_720.mp4",
			wantItemCount: 1,
			wantMediaType: "video",
		},
		{
			name: "DASH_QualitySelection",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
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
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "abc",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/abc/DASHPlaylist.mpd",
				Media: &reddit.Media{
					Video: &reddit.Video{
						DashURL: "https://v.redd.it/abc/DASHPlaylist.mpd",
					},
				},
			},
			wantError:     false,
			expectedURL:   "https://v.redd.it/abc/DASH_720.mp4",
			wantItemCount: 1,
		},
		{
			name: "DASH_FailureWithHLSFallthrough",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hlsvid/DASH_1080.mp4", "/hlsvid/DASH_720.mp4", "/hlsvid/DASH_480.mp4":
					w.WriteHeader(http.StatusNotFound)
				default:
					w.WriteHeader(http.StatusOK)
				}
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "hlsvid",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/hlsvid/download",
				Media: &reddit.Media{
					Video: &reddit.Video{
						DashURL: "", // Will be set in test
						HLSURL:  "", // Will be set in test
					},
				},
			},
			wantError:     false,
			wantItemCount: 1,
			urlChecks: map[string]bool{
				".m3u8":       false,
				"DASH_":       true,
				"hlsvid":      true,
				"invalidpath": false,
				"dash.m3u8":   false,
			},
		},
		{
			name: "HLS_OnlyMetadata_FallsThrough",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "hlsonly",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/hlsonly",
				Media: &reddit.Media{
					Video: &reddit.Video{
						HLSURL: "", // Will be set in test
					},
				},
			},
			wantError:     false,
			wantItemCount: 1,
			urlChecks: map[string]bool{
				".m3u8":   false,
				"DASH_":   true,
				"hlsonly": true,
			},
		},
		{
			name: "DASH_BasePriority",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/dashbase/DASH_1080.mp4":
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "prioritytest",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/video/invalidpath",
				Media: &reddit.Media{
					Video: &reddit.Video{
						DashURL: "", // Will be set in test
						HLSURL:  "https://v.redd.it/hls/DASH_720.mp4",
					},
				},
			},
			wantError:     false,
			wantItemCount: 1,
			urlChecks: map[string]bool{
				"dashbase":    true,
				"invalidpath": false,
				".m3u8":       false,
			},
		},
		{
			name: "HLS_FallsThroughToDerivedDASH",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodHead:
					switch r.URL.Path {
					case "/hlsvid/DASH_720.mp4":
						w.WriteHeader(http.StatusOK)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				default:
					w.WriteHeader(http.StatusOK)
				}
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "hlsvid",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/hlsvid/HLSPlaylist.m3u8",
				Media: &reddit.Media{
					Video: &reddit.Video{
						DashURL: "https://v.redd.it/hlsvid/DASHPlaylist.mpd",
						HLSURL:  "https://v.redd.it/hlsvid/HLSPlaylist.m3u8",
					},
				},
			},
			wantError:     false,
			expectedURL:   "https://v.redd.it/hlsvid/DASH_720.mp4",
			wantItemCount: 1,
			urlChecks: map[string]bool{
				".m3u8": false,
			},
		},
		{
			name: "DerivedBase_SuccessWhenMediaNil",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodHead:
					switch r.URL.Path {
					case "/derived/DASH_480.mp4":
						w.WriteHeader(http.StatusOK)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				default:
					w.WriteHeader(http.StatusOK)
				}
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "derived",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/derived/DASH_480.mp4",
				// Media is nil - forces derived-base path
			},
			wantError:     false,
			expectedURL:   "https://v.redd.it/derived/DASH_480.mp4",
			wantItemCount: 1,
		},
		{
			name: "DerivedBase_FailureNoQualities",
			serverHandler: func(w http.ResponseWriter, _ *http.Request) {
				// All quality checks return 404
				w.WriteHeader(http.StatusNotFound)
			},
			hosts: []string{"v.redd.it"},
			post: reddit.Post{
				ID:        "nofind",
				Subreddit: "videos",
				IsVideo:   true,
				URL:       "https://v.redd.it/nofind/DASH_720.mp4",
				Media: &reddit.Media{
					Video: &reddit.Video{
						DashURL: "https://v.redd.it/nofind/DASHPlaylist.mpd",
					},
				},
			},
			wantError:     true,
			errorContains: "no Reddit video quality found",
			wantNilItems:  true,
		},
		{
			name:          "Gfycat_ShortCircuit",
			serverHandler: nil,
			hosts:         []string{},
			post: reddit.Post{
				ID:        "gfycat1",
				Subreddit: "videos",
				URL:       "https://gfycat.com/somevideo",
			},
			wantError:    false,
			wantNilItems: true,
		},
		{
			name: "Redgifs_410GoneHandling",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/v2/gifs/") {
					w.WriteHeader(http.StatusGone)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			hosts: []string{"api.redgifs.com", "redgifs.com"},
			post: reddit.Post{
				ID:        "redgifs1",
				Subreddit: "videos",
				URL:       "https://redgifs.com/watch/somevideo",
			},
			wantError:    false,
			wantNilItems: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var extractor *Extractor
			var items []Downloadable
			var err error

			if tt.serverHandler != nil {
				server := httptest.NewServer(tt.serverHandler)
				defer server.Close()

				client := newRewriteClient(server, tt.hosts...)
				extractor = NewExtractor(client, "test-agent")

				// Set dynamic URLs for tests that need them
				localPost := tt.post
				switch tt.name {
				case "DASH_FailureWithHLSFallthrough":
					localPost.Media.Video.DashURL = server.URL + "/hlsvid/DASH_1080.mp4"
					localPost.Media.Video.HLSURL = server.URL + "/hlsvid/dash.m3u8"
				case "HLS_OnlyMetadata_FallsThrough":
					localPost.Media.Video.HLSURL = server.URL + "/hlsonly/hls/master.m3u8"
				case "DASH_BasePriority":
					localPost.Media.Video.DashURL = server.URL + "/dashbase/DASH_1080.mp4"
				}

				items, err = extractor.Extract(context.Background(), localPost)
			} else {
				extractor = NewExtractor(&http.Client{Timeout: time.Second}, "test-agent")
				items, err = extractor.Extract(context.Background(), tt.post)
			}

			if tt.wantError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.wantNilItems {
				assert.Nil(t, items)
			} else if tt.wantItemCount > 0 {
				require.Len(t, items, tt.wantItemCount)
			}

			if tt.expectedURL != "" && len(items) > 0 {
				assert.Equal(t, tt.expectedURL, items[0].URL)
			}

			if tt.wantMediaType != "" && len(items) > 0 {
				assert.Equal(t, tt.wantMediaType, items[0].MediaType)
			}

			for substr, shouldContain := range tt.urlChecks {
				if len(items) > 0 {
					if shouldContain {
						assert.Contains(t, items[0].URL, substr)
					} else {
						assert.NotContains(t, items[0].URL, substr)
					}
				}
			}
		})
	}
}
