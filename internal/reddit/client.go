// Package reddit provides Reddit API client with OAuth2 authentication.
package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

const (
	// RedditOAuthEndpoint is the base URL for Reddit OAuth.
	RedditOAuthEndpoint = "https://www.reddit.com/api/v1"
	// RedditAPIEndpoint is the base URL for Reddit API.
	RedditAPIEndpoint = "https://oauth.reddit.com"
	// MaxPostsPerRequest is the maximum number of posts per API request.
	MaxPostsPerRequest = 100
	// MaxTotalPosts is the maximum total posts to fetch (Reddit limit).
	MaxTotalPosts = 1000
	// RateLimitPerMinute is the Reddit rate limit for OAuth apps.
	RateLimitPerMinute = 60
)

var (
	// ErrRateLimited is returned when rate limit is exceeded.
	ErrRateLimited = errors.New("rate limit exceeded")
	// ErrUnauthorized is returned when authentication fails.
	ErrUnauthorized = errors.New("unauthorized: check credentials")
	// ErrInvalidResponse is returned when the API returns an unexpected response.
	ErrInvalidResponse = errors.New("invalid API response")
	// ErrMaxPostsExceeded is returned when trying to fetch more than 1000 posts.
	ErrMaxPostsExceeded = errors.New("cannot fetch more than 1000 posts")
)

// Config holds the Reddit OAuth configuration.
//
// Config is exported and used externally; field order is part of the API.
//
//nolint:gosec,fieldalignment // G117: secret pattern fields - required for OAuth2 authentication.
type Config struct {
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
	RefreshToken string
	UserAgent    string
}

// TokenStore defines the interface for persisting OAuth tokens.
type TokenStore interface {
	// SaveToken saves the OAuth token.
	SaveToken(token *oauth2.Token) error
	// LoadToken loads the OAuth token.
	LoadToken() (*oauth2.Token, error)
}

// Client defines the interface for Reddit API operations.
type Client interface {
	// GetUpvoted fetches upvoted posts with the specified limit.
	GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error)
	// GetSaved fetches saved posts with the specified limit.
	GetSaved(ctx context.Context, limit int) ([]storage.Post, error)
	// Close cleans up the client resources.
	Close() error
}

// redditClient provides authenticated access to the Reddit API.
//
//nolint:fieldalignment // Internal struct, safe to reorder but not worth the churn.
type redditClient struct {
	config      *Config
	tokenStore  TokenStore
	oauthConfig *oauth2.Config
	token       *oauth2.Token
	rateLimiter *rateLimiter
	mu          sync.RWMutex
}

// rateLimiter implements token bucket rate limiting for Reddit API requests.
//
//nolint:fieldalignment // Internal struct with minimal memory impact.
type rateLimiter struct {
	lastRequest time.Time
	refillRate  time.Duration
	mu          sync.Mutex
	tokens      int
}

// newRateLimiter creates a new rate limiter with the specified requests per minute.
func newRateLimiter(requestsPerMinute int) *rateLimiter {
	return &rateLimiter{
		tokens:      requestsPerMinute,
		lastRequest: time.Now(),
		refillRate:  time.Minute / time.Duration(requestsPerMinute),
	}
}

// Wait blocks until a token is available for the next request.
// Returns an error if the context is canceled.
func (rl *rateLimiter) Wait(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Calculate tokens to refill based on time elapsed
	elapsed := time.Since(rl.lastRequest)
	tokensToAdd := int(elapsed / rl.refillRate)
	if tokensToAdd > 0 {
		rl.tokens = minInt(rl.tokens+tokensToAdd, RateLimitPerMinute)
		rl.lastRequest = time.Now()
	}

	// Wait for a token to be available
	if rl.tokens <= 0 {
		waitTime := rl.refillRate - elapsed%rl.refillRate
		rl.mu.Unlock()
		select {
		case <-ctx.Done():
			rl.mu.Lock()
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-time.After(waitTime):
			rl.mu.Lock()
			rl.tokens = 1
			rl.lastRequest = time.Now()
		}
	}

	rl.tokens--
	rl.lastRequest = time.Now()
	return nil
}

// minInt returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// NewClient creates a new authenticated Reddit client.
// If tokenStore is nil, tokens will not be persisted.
//
//nolint:cyclop
func NewClient(config *Config, tokenStore TokenStore) (*redditClient, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}
	if config.ClientID == "" || config.ClientSecret == "" {
		return nil, errors.New("client ID and client secret are required")
	}
	if config.Username == "" {
		return nil, errors.New("username is required")
	}
	if config.UserAgent == "" {
		config.UserAgent = "reddit-media-downloader/1.0"
	}

	oauthConfig := &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: RedditOAuthEndpoint + "/access_token",
			AuthURL:  RedditOAuthEndpoint + "/authorize",
		},
		Scopes: []string{"identity", "history", "read"},
	}

	client := &redditClient{
		config:      config,
		tokenStore:  tokenStore,
		oauthConfig: oauthConfig,
		rateLimiter: newRateLimiter(RateLimitPerMinute),
	}

	// Try to load existing token
	if tokenStore != nil {
		token, err := tokenStore.LoadToken()
		if err == nil && token != nil {
			client.token = token
		}
	}

	// Authenticate if no valid token exists
	if client.token == nil || !client.token.Valid() {
		if err := client.authenticate(context.Background()); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	return client, nil
}

// authenticate performs OAuth2 authentication with client credentials.
// It will use refresh_token grant if a refresh token exists, otherwise
// falls back to password grant.
//
//nolint:cyclop
func (c *redditClient) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try refreshing with existing token
	if err := c.tryRefreshWithExistingToken(ctx); err == nil {
		return nil
	}

	// Try refreshing with config token
	if err := c.tryRefreshWithConfigToken(ctx); err == nil {
		return nil
	}

	// Fallback: password grant
	return c.authenticateWithPassword(ctx)
}

// tryRefreshWithExistingToken attempts to refresh token using existing token.
func (c *redditClient) tryRefreshWithExistingToken(ctx context.Context) error {
	if c.token == nil || c.token.RefreshToken == "" {
		return errors.New("no existing refresh token")
	}

	tokenSource := c.oauthConfig.TokenSource(ctx, c.token)
	if err := c.refreshAndSaveToken(ctx, tokenSource); err != nil {
		slog.Warn("Token refresh failed", "error", err, "source", "existing token source")
		return err
	}
	return nil
}

// tryRefreshWithConfigToken attempts to refresh token using config refresh token.
func (c *redditClient) tryRefreshWithConfigToken(ctx context.Context) error {
	if c.config.RefreshToken == "" {
		return errors.New("no refresh token in config")
	}

	tokenSource := c.oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: c.config.RefreshToken})
	if err := c.refreshAndSaveToken(ctx, tokenSource); err != nil {
		slog.Warn("Token refresh failed", "error", err, "source", "refresh token from config")
		return err
	}
	return nil
}

// authenticateWithPassword performs password grant authentication.
//
//nolint:cyclop
func (c *redditClient) authenticateWithPassword(ctx context.Context) error {
	if c.config.Password == "" {
		return errors.New("password is required for password grant (use --auth to get a refresh token)")
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("username", c.config.Username)
	data.Set("password", c.config.Password)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.oauthConfig.Endpoint.TokenURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return fmt.Errorf("creating token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)

	//nolint:gosec // G704: SSRF via taint analysis - this is a Reddit API call to known endpoint
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}

	// Set token expiry based on expires_in if not provided
	if token.Expiry.IsZero() && token.AccessToken != "" {
		token.Expiry = time.Now().Add(1 * time.Hour)
	}

	c.token = &token

	// Save token if store is available
	if c.tokenStore != nil {
		if err := c.tokenStore.SaveToken(c.token); err != nil {
			slog.Warn("Failed to save token", "error", err)
		}
	}

	return nil
}

// refreshAndSaveToken refreshes the token using the provided tokenSource and saves it.
// It logs any save errors using slog for diagnostics.
// Includes retry with exponential backoff for transient errors (network issues, rate limits).
func (c *redditClient) refreshAndSaveToken(ctx context.Context, ts oauth2.TokenSource) error {
	var lastErr error
	maxRetries := 3
	baseDelay := 1 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		newToken, err := ts.Token()
		if err == nil {
			c.token = newToken

			// Save token if store is available and log any errors
			if c.tokenStore != nil {
				if err := c.tokenStore.SaveToken(c.token); err != nil {
					slog.Warn("Failed to save token", "error", err, "source", "refresh")
				}
			}
			return nil
		}

		// Check if it's a 429 error
		lastErr = err
		if attempt < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<attempt) // Exponential backoff: 1s, 2s, 4s
			slog.Warn(
				"Token refresh attempt failed, retrying",
				"attempt",
				attempt+1,
				"maxRetries",
				maxRetries,
				"delay",
				delay,
				"error",
				err,
			)
			select {
			case <-ctx.Done():
				return fmt.Errorf("context canceled: %w", ctx.Err())
			case <-time.After(delay):
				continue
			}
		}
	}

	return fmt.Errorf("refreshing token after %d retries: %w", maxRetries, lastErr)
}

// ensureValidToken refreshes the token if it's expired.
func (c *redditClient) ensureValidToken(ctx context.Context) error {
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	if token != nil && token.Valid() {
		return nil
	}

	// Token is expired or invalid, re-authenticate
	return c.authenticate(ctx)
}

// doRequest makes an authenticated HTTP request with rate limiting.
//
//nolint:cyclop
func (c *redditClient) doRequest(ctx context.Context, method, endpoint string, params url.Values) (*http.Response, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	if err := c.ensureValidToken(ctx); err != nil {
		return nil, err
	}

	req, err := c.buildRequest(ctx, method, endpoint, params)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	//nolint:gosec // G704: SSRF via taint analysis - this is a Reddit API call to known endpoint
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("failed to close response body", "error", closeErr)
		}
		return nil, ErrRateLimited
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return c.handleUnauthorizedRequest(ctx, httpClient, method, endpoint, params, resp)
	}

	return resp, nil
}

// buildRequest builds an HTTP request with proper headers and authentication.
func (c *redditClient) buildRequest(ctx context.Context, method, endpoint string, params url.Values) (*http.Request, error) {
	reqURL := RedditAPIEndpoint + endpoint
	if params != nil && method == "GET" {
		reqURL = reqURL + "?" + params.Encode()
	}

	var body *strings.Reader
	if params != nil && method != "GET" {
		body = strings.NewReader(params.Encode())
	} else {
		body = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	c.mu.RLock()
	if c.token != nil {
		req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	}
	c.mu.RUnlock()

	return req, nil
}

// handleUnauthorizedRequest handles 401 responses by re-authenticating and retrying.
func (c *redditClient) handleUnauthorizedRequest(ctx context.Context, httpClient *http.Client, method, endpoint string, params url.Values, resp *http.Response) (*http.Response, error) {
	if closeErr := resp.Body.Close(); closeErr != nil {
		slog.Error("failed to close response body", "error", closeErr)
	}

	if authErr := c.authenticate(ctx); authErr != nil {
		return nil, fmt.Errorf("%w: authentication failed: %v", ErrUnauthorized, authErr)
	}

	c.mu.RLock()
	accessToken := ""
	if c.token != nil {
		accessToken = c.token.AccessToken
	}
	c.mu.RUnlock()

	if accessToken == "" {
		return nil, fmt.Errorf("%w: no access token after authentication", ErrUnauthorized)
	}

	reqURL := RedditAPIEndpoint + endpoint
	if params != nil && method == "GET" {
		reqURL = reqURL + "?" + params.Encode()
	}

	var body *strings.Reader
	if params != nil && method != "GET" {
		body = strings.NewReader(params.Encode())
	} else {
		body = strings.NewReader("")
	}

	retryReq, retryReqErr := http.NewRequestWithContext(ctx, method, reqURL, body)
	if retryReqErr != nil {
		return nil, fmt.Errorf("creating retry request: %w", retryReqErr)
	}

	retryReq.Header.Set("User-Agent", c.config.UserAgent)
	retryReq.Header.Set("Accept", "application/json")
	retryReq.Header.Set("Authorization", "Bearer "+accessToken)

	//nolint:gosec // G704: SSRF via taint analysis - this is a Reddit API call to known endpoint
	retryResp, retryErr := httpClient.Do(retryReq)
	if retryErr != nil {
		return nil, fmt.Errorf("retry request failed: %w", retryErr)
	}

	if retryResp.StatusCode == http.StatusUnauthorized {
		if closeErr := retryResp.Body.Close(); closeErr != nil {
			slog.Error("failed to close retry response body", "error", closeErr)
		}
		return nil, ErrUnauthorized
	}

	return retryResp, nil
}

// GetUpvoted fetches upvoted posts for the authenticated user.
// Returns up to 'limit' posts (max 1000 per Reddit API limit).
func (c *redditClient) GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error) {
	return c.getUserPosts(ctx, "upvoted", limit)
}

// GetSaved fetches saved posts for the authenticated user.
// Returns up to 'limit' posts (max 1000 per Reddit API limit).
func (c *redditClient) GetSaved(ctx context.Context, limit int) ([]storage.Post, error) {
	return c.getUserPosts(ctx, "saved", limit)
}

// getUserPosts fetches posts from a user endpoint (upvoted or saved).
//
//nolint:cyclop
func (c *redditClient) getUserPosts(ctx context.Context, endpoint string, limit int) ([]storage.Post, error) {
	if limit <= 0 {
		return []storage.Post{}, nil
	}
	if limit > MaxTotalPosts {
		return nil, ErrMaxPostsExceeded
	}

	var allPosts []storage.Post
	var after *string

	for len(allPosts) < limit {
		// Calculate how many to fetch in this request
		remaining := limit - len(allPosts)
		fetchCount := minInt(remaining, MaxPostsPerRequest)

		params := url.Values{}
		params.Set("limit", strconv.Itoa(fetchCount))
		params.Set("sort", "new") // Ensure newest posts first
		if after != nil {
			params.Set("after", *after)
		}

		// Build endpoint URL
		path := fmt.Sprintf("/user/%s/%s", c.config.Username, endpoint)

		resp, err := c.doRequest(ctx, "GET", path, params)
		if err != nil {
			return nil, fmt.Errorf("fetching %s posts: %w", endpoint, err)
		}

		var listing Listing
		if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Error("failed to close response body", "error", closeErr)
			}
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("failed to close response body", "error", closeErr)
		}

		// Check for errors in response
		if listing.Kind != "Listing" {
			return nil, ErrInvalidResponse
		}

		// Convert posts
		source := endpoint
		for _, child := range listing.Data.Children {
			post := child.Data.ToStoragePost(source)
			allPosts = append(allPosts, post)
		}

		// Check for more pages
		after = listing.Data.After
		if after == nil || len(listing.Data.Children) == 0 {
			// No more posts
			break
		}
	}

	// Trim to exact limit if we fetched more
	if len(allPosts) > limit {
		allPosts = allPosts[:limit]
	}

	return allPosts, nil
}

// Close closes the client and cleans up resources.
func (c *redditClient) Close() error {
	// Save token before closing if store is available
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	if c.tokenStore != nil && token != nil {
		if err := c.tokenStore.SaveToken(token); err != nil {
			return fmt.Errorf("save token: %w", err)
		}
	}
	return nil
}

// GetUsername returns the username of the authenticated user.
func (c *redditClient) GetUsername() string {
	return c.config.Username
}

// IsAuthenticated returns true if the client has a valid token.
func (c *redditClient) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token != nil && c.token.Valid()
}

// RefreshToken manually refreshes the access token.
func (c *redditClient) RefreshToken(ctx context.Context) error {
	return c.authenticate(ctx)
}
