package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/user/reddit-media-downloader/internal/reddit"
)

const (
	defaultUserAgent = "reddit-media-downloader/1.0"
)

var (
	imgurDirectRegex    = regexp.MustCompile(`https?://i\.imgur\.com/[^"'\s]+`)
	imgurOGImageRegex   = regexp.MustCompile(`property=["']og:image["']\s+content=["']([^"']+)["']`)
	imgurMetaImageRegex = regexp.MustCompile(`name=["']twitter:image["']\s+content=["']([^"']+)["']`)
	mp4URLRegex         = regexp.MustCompile(`https?://[^"'\s]+\.mp4`)
)

var supportedExtensions = map[string]string{
	".jpg":  "image",
	".jpeg": "image",
	".png":  "image",
	".gif":  "image",
	".mp4":  "video",
	".webm": "video",
}

type Downloadable struct {
	PostID    string
	URL       string
	Filename  string
	MediaType string
	Subreddit string
	Hash      string
}

type Extractor struct {
	client    *http.Client
	userAgent string
	logger    *slog.Logger
}

func NewExtractor(client *http.Client, userAgent string) *Extractor {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	return &Extractor{client: client, userAgent: userAgent, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func NewExtractorWithLogger(client *http.Client, userAgent string, logger *slog.Logger) *Extractor {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Extractor{client: client, userAgent: userAgent, logger: logger}
}

func (e *Extractor) Extract(ctx context.Context, post reddit.RedditPost) ([]Downloadable, error) {
	if post.GalleryData != nil && len(post.GalleryData.Items) > 0 {
		return e.extractGallery(post)
	}

	sourceURL := strings.TrimSpace(post.URLOverride)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(post.URL)
	}
	if sourceURL == "" {
		return nil, errors.New("post URL is empty")
	}

	sourceURL = decodeMediaURL(sourceURL)

	return e.extractFromURL(ctx, post, sourceURL)
}

func (e *Extractor) extractFromURL(ctx context.Context, post reddit.RedditPost, sourceURL string) ([]Downloadable, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	host := strings.ToLower(parsed.Host)

	switch {
	case isRedditImageHost(host):
		return e.buildDownloadables(post, []string{sourceURL}, "")
	case isRedditVideoHost(host) || post.IsVideo:
		return e.extractRedditVideo(ctx, post, sourceURL)
	case isImgurHost(host):
		return e.extractImgur(ctx, post, sourceURL)
	case isGfycatHost(host) || isRedgifsHost(host):
		return e.extractGfycatRedgifs(ctx, post, sourceURL)
	case isDirectMediaURL(parsed):
		return e.buildDownloadables(post, []string{sourceURL}, "")
	default:
		return nil, fmt.Errorf("unsupported media host: %s", host)
	}
}

func (e *Extractor) extractGallery(post reddit.RedditPost) ([]Downloadable, error) {
	if post.MediaMeta == nil {
		return nil, errors.New("gallery metadata missing")
	}

	items := make([]Downloadable, 0, len(post.GalleryData.Items))
	for _, item := range post.GalleryData.Items {
		meta, ok := post.MediaMeta[item.MediaID]
		if !ok {
			e.logger.Warn("gallery media metadata missing", "media_id", item.MediaID)
			continue
		}

		mediaURL := strings.TrimSpace(meta.Source.URL)
		if mediaURL == "" && len(meta.Previews) > 0 {
			mediaURL = strings.TrimSpace(meta.Previews[0].URL)
		}
		if mediaURL == "" {
			e.logger.Warn("gallery media URL missing", "media_id", item.MediaID)
			continue
		}

		mediaURL = decodeMediaURL(mediaURL)
		ext, mediaType, err := extensionAndType(mediaURL, meta.Mime)
		if err != nil {
			return nil, err
		}

		// Sanitize title for filesystem and create filename in bdfr-html format: {TITLE}_{POSTID}.{ext}
		sanitizedTitle := sanitizeFilename(post.Title)
		filename := fmt.Sprintf("%s_%s%s", sanitizedTitle, post.ID, ext)
		items = append(items, Downloadable{
			PostID:    post.ID,
			URL:       mediaURL,
			Filename:  filename,
			MediaType: mediaType,
			Subreddit: post.Subreddit,
		})
	}

	return items, nil
}

func (e *Extractor) extractRedditVideo(ctx context.Context, post reddit.RedditPost, sourceURL string) ([]Downloadable, error) {
	if post.Media != nil && post.Media.RedditVideo != nil {
		fallback := strings.TrimSpace(post.Media.RedditVideo.FallbackURL)
		if fallback != "" {
			return e.buildDownloadables(post, []string{fallback}, "")
		}

		if post.Media.RedditVideo.DashURL != "" {
			base := baseRedditVideoURL(post.Media.RedditVideo.DashURL)
			best, err := e.selectBestRedditVideo(ctx, base)
			if err == nil {
				return e.buildDownloadables(post, []string{best}, "")
			}
		}
	}

	base := baseRedditVideoURL(sourceURL)
	if base == "" {
		return nil, errors.New("unable to determine Reddit video base URL")
	}
	best, err := e.selectBestRedditVideo(ctx, base)
	if err != nil {
		return nil, err
	}
	return e.buildDownloadables(post, []string{best}, "")
}

func (e *Extractor) extractImgur(ctx context.Context, post reddit.RedditPost, sourceURL string) ([]Downloadable, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("parse imgur URL: %w", err)
	}
	if strings.HasPrefix(strings.ToLower(parsed.Host), "i.imgur.com") {
		return e.buildDownloadables(post, []string{sourceURL}, "")
	}

	imageURL, err := e.fetchImgurImageURL(ctx, sourceURL)
	if err != nil {
		return nil, err
	}

	return e.buildDownloadables(post, []string{imageURL}, "")
}

func (e *Extractor) fetchImgurImageURL(ctx context.Context, pageURL string) (string, error) {
	body, err := e.fetchText(ctx, pageURL)
	if err != nil {
		return "", err
	}

	if match := imgurOGImageRegex.FindStringSubmatch(body); len(match) > 1 {
		return normalizeURL(match[1]), nil
	}
	if match := imgurMetaImageRegex.FindStringSubmatch(body); len(match) > 1 {
		return normalizeURL(match[1]), nil
	}
	if match := imgurDirectRegex.FindString(body); match != "" {
		return normalizeURL(match), nil
	}

	return "", errors.New("imgur image URL not found")
}

func (e *Extractor) extractGfycatRedgifs(ctx context.Context, post reddit.RedditPost, sourceURL string) ([]Downloadable, error) {
	mediaURL, err := e.fetchGfycatRedgifsURL(ctx, sourceURL)
	if err != nil {
		return nil, err
	}

	return e.buildDownloadables(post, []string{mediaURL}, "")
}

func (e *Extractor) fetchGfycatRedgifsURL(ctx context.Context, pageURL string) (string, error) {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("parse gfycat/redgifs URL: %w", err)
	}
	host := strings.ToLower(parsed.Host)
	mediaID := strings.Trim(strings.Trim(parsed.Path, "/"), " ")
	if strings.HasPrefix(mediaID, "watch/") {
		mediaID = strings.TrimPrefix(mediaID, "watch/")
	}
	if mediaID == "" {
		return "", errors.New("missing media ID")
	}

	if isGfycatHost(host) {
		apiURL := fmt.Sprintf("https://api.gfycat.com/v1/gfycats/%s", mediaID)
		if mediaURL, err := e.fetchGfycatAPI(ctx, apiURL); err == nil {
			return mediaURL, nil
		}
	}
	if isRedgifsHost(host) {
		apiURL := fmt.Sprintf("https://api.redgifs.com/v2/gifs/%s", mediaID)
		if mediaURL, err := e.fetchRedgifsAPI(ctx, apiURL); err == nil {
			return mediaURL, nil
		}
	}

	body, err := e.fetchText(ctx, pageURL)
	if err != nil {
		return "", err
	}
	if match := mp4URLRegex.FindString(body); match != "" {
		return match, nil
	}

	return "", errors.New("mp4 URL not found in gfycat/redgifs response")
}

func (e *Extractor) fetchGfycatAPI(ctx context.Context, apiURL string) (string, error) {
	response, err := e.fetchJSON(ctx, apiURL)
	if err != nil {
		return "", err
	}

	var payload struct {
		GfyItem struct {
			Mp4URL     string `json:"mp4Url"`
			ContentURL struct {
				MP4 struct {
					URL string `json:"url"`
				} `json:"mp4"`
				Mobile struct {
					URL string `json:"url"`
				} `json:"mobile"`
			} `json:"content_urls"`
		} `json:"gfyItem"`
	}

	if err := json.Unmarshal(response, &payload); err != nil {
		return "", fmt.Errorf("decode gfycat API: %w", err)
	}

	if payload.GfyItem.Mp4URL != "" {
		return payload.GfyItem.Mp4URL, nil
	}
	if payload.GfyItem.ContentURL.MP4.URL != "" {
		return payload.GfyItem.ContentURL.MP4.URL, nil
	}
	if payload.GfyItem.ContentURL.Mobile.URL != "" {
		return payload.GfyItem.ContentURL.Mobile.URL, nil
	}

	return "", errors.New("gfycat API did not return mp4 URL")
}

func (e *Extractor) fetchRedgifsAPI(ctx context.Context, apiURL string) (string, error) {
	response, err := e.fetchJSON(ctx, apiURL)
	if err != nil {
		return "", err
	}

	var payload struct {
		GIF struct {
			URLs struct {
				HD string `json:"hd"`
				SD string `json:"sd"`
			} `json:"urls"`
		} `json:"gif"`
	}

	if err := json.Unmarshal(response, &payload); err != nil {
		return "", fmt.Errorf("decode redgifs API: %w", err)
	}

	if payload.GIF.URLs.HD != "" {
		return payload.GIF.URLs.HD, nil
	}
	if payload.GIF.URLs.SD != "" {
		return payload.GIF.URLs.SD, nil
	}

	return "", errors.New("redgifs API did not return mp4 URL")
}

func (e *Extractor) fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", e.userAgent)
	req.Header.Set("Accept", "text/html,application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return string(body), nil
}

func (e *Extractor) fetchJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", e.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

func (e *Extractor) selectBestRedditVideo(ctx context.Context, baseURL string) (string, error) {
	qualities := []string{"1080", "720", "480", "360", "240"}
	for _, quality := range qualities {
		candidate := strings.TrimRight(baseURL, "/") + "/DASH_" + quality + ".mp4"
		exists, err := e.urlExists(ctx, candidate)
		if err != nil {
			continue
		}
		if exists {
			return candidate, nil
		}
	}

	return "", errors.New("no Reddit video quality found")
}

func (e *Extractor) urlExists(ctx context.Context, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", e.userAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

func (e *Extractor) buildDownloadables(post reddit.RedditPost, urls []string, mediaType string) ([]Downloadable, error) {
	items := make([]Downloadable, 0, len(urls))
	for i, mediaURL := range urls {
		ext, resolvedType, err := extensionAndType(mediaURL, "")
		if err != nil {
			return nil, err
		}
		if mediaType != "" {
			resolvedType = mediaType
		}
		sanitizedTitle := sanitizeFilename(post.Title)
		var filename string
		if len(urls) > 1 {
			filename = fmt.Sprintf("%s_%d_%s%s", sanitizedTitle, i+1, post.ID, ext)
		} else {
			filename = fmt.Sprintf("%s_%s%s", sanitizedTitle, post.ID, ext)
		}
		items = append(items, Downloadable{
			PostID:    post.ID,
			URL:       mediaURL,
			Filename:  filename,
			MediaType: resolvedType,
			Subreddit: post.Subreddit,
		})
	}

	return items, nil
}

func extensionAndType(rawURL, mime string) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parse URL: %w", err)
	}

	var ext string
	pathExt := strings.ToLower(path.Ext(parsed.Path))
	if pathExt != "" {
		ext = pathExt
	}
	if ext == "" {
		format := strings.ToLower(parsed.Query().Get("format"))
		if format != "" {
			ext = formatToExtension(format)
		}
	}
	if ext == "" && mime != "" {
		ext = mimeToExtension(mime)
	}

	mediaType, ok := supportedExtensions[ext]
	if !ok {
		return "", "", fmt.Errorf("unsupported media extension: %s", ext)
	}

	return ext, mediaType, nil
}

func formatToExtension(format string) string {
	switch strings.ToLower(format) {
	case "pjpg", "jpg", "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "gif":
		return ".gif"
	case "mp4":
		return ".mp4"
	case "webm":
		return ".webm"
	default:
		return ""
	}
}

func mimeToExtension(mime string) string {
	parts := strings.Split(strings.ToLower(mime), "/")
	if len(parts) != 2 {
		return ""
	}

	switch parts[1] {
	case "jpeg", "jpg":
		return ".jpg"
	case "png":
		return ".png"
	case "gif":
		return ".gif"
	case "mp4":
		return ".mp4"
	case "webm":
		return ".webm"
	default:
		return ""
	}
}

func decodeMediaURL(raw string) string {
	return strings.ReplaceAll(raw, "&amp;", "&")
}

func baseRedditVideoURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s/%s", parsed.Scheme, parsed.Host, parts[0])
}

func normalizeURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

func isRedditImageHost(host string) bool {
	return host == "i.redd.it" || host == "preview.redd.it"
}

func isRedditVideoHost(host string) bool {
	return host == "v.redd.it"
}

func isImgurHost(host string) bool {
	return host == "imgur.com" || strings.HasSuffix(host, ".imgur.com")
}

func isGfycatHost(host string) bool {
	return host == "gfycat.com" || strings.HasSuffix(host, ".gfycat.com")
}

func isRedgifsHost(host string) bool {
	return host == "redgifs.com" || strings.HasSuffix(host, ".redgifs.com")
}

func isDirectMediaURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	return isSupportedExtension(path.Ext(parsed.Path))
}

func isSupportedExtension(ext string) bool {
	_, ok := supportedExtensions[strings.ToLower(ext)]
	return ok
}

func sanitizeFilename(title string) string {
	if title == "" {
		return "untitled"
	}

	// Remove/replace invalid filesystem characters
	sanitized := strings.Map(func(r rune) rune {
		switch r {
		// Path separators
		case '/', '\\':
			return '-'
		// Windows reserved characters
		case ':', '*', '?', '"', '<', '>', '|':
			return '_'
		// Control characters
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31:
			return -1
		default:
			return r
		}
	}, title)

	// Trim leading/trailing spaces and dots
	sanitized = strings.TrimSpace(sanitized)
	sanitized = strings.Trim(sanitized, ".")

	// Collapse multiple spaces
	for strings.Contains(sanitized, "  ") {
		sanitized = strings.ReplaceAll(sanitized, "  ", " ")
	}

	// Limit length to avoid filesystem issues
	if len(sanitized) > 100 {
		sanitized = sanitized[:100]
		sanitized = strings.TrimSpace(sanitized)
		sanitized = strings.Trim(sanitized, ".")
	}

	if sanitized == "" {
		return "untitled"
	}
	return sanitized
}
