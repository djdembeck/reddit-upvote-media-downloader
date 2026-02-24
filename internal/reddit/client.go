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

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
	"golang.org/x/oauth2"
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

// RedditClient defines the interface for Reddit API operations.
type RedditClient interface {
	// GetUpvoted fetches upvoted posts with the specified limit.
	GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error)
	// GetSaved fetches saved posts with the specified limit.
	GetSaved(ctx context.Context, limit int) ([]storage.Post, error)
	// Close cleans up the client resources.
	Close() error
}

// Client provides authenticated access to the Reddit API.
type Client struct {
	config      *Config
	tokenStore  TokenStore
	httpClient  *http.Client
	oauthConfig *oauth2.Config
	token       *oauth2.Token
	mu          sync.RWMutex

	// Rate limiting
	rateLimiter *rateLimiter
}

// rateLimiter implements token bucket rate limiting for Reddit API requests.
type rateLimiter struct {
	mu          sync.Mutex
	tokens      int
	lastRequest time.Time
	refillRate  time.Duration
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
// Returns an error if the context is cancelled.
func (rl *rateLimiter) Wait(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Calculate tokens to refill based on time elapsed
	elapsed := time.Since(rl.lastRequest)
	tokensToAdd := int(elapsed / rl.refillRate)
	if tokensToAdd > 0 {
		rl.tokens = min(rl.tokens+tokensToAdd, RateLimitPerMinute)
		rl.lastRequest = time.Now()
	}

	// Wait for a token to be available
	if rl.tokens <= 0 {
		waitTime := rl.refillRate - elapsed%rl.refillRate
		rl.mu.Unlock()
		select {
		case <-ctx.Done():
			rl.mu.Lock()
			return ctx.Err()
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

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// NewClient creates a new authenticated Reddit client.
// If tokenStore is nil, tokens will not be persisted.
func NewClient(config *Config, tokenStore TokenStore) (*Client, error) {
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

	client := &Client{
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
func (c *Client) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we have a refresh token available
	if c.token != nil && c.token.RefreshToken != "" {
		// Use oauth2.TokenSource to refresh the token
		tokenSource := c.oauthConfig.TokenSource(ctx, c.token)
		if err := c.refreshAndSaveToken(ctx, tokenSource); err != nil {
			// If refresh fails, continue to password grant
		} else {
			return nil
		}
	}

	// Check if we have a refresh token in config (set via --auth or REDDIT_REFRESH_TOKEN)
	if c.config.RefreshToken != "" {
		// Use refresh token to get new access token
		tokenSource := c.oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: c.config.RefreshToken})
		if err := c.refreshAndSaveToken(ctx, tokenSource); err != nil {
			// Refresh failed, continue to fallback
		} else {
			return nil
		}
	}

	// Fallback: password grant (for backward compatibility)

	// Fallback: password grant (for backward compatibility)
	if c.config.Password == "" {
		return errors.New("password is required for password grant (use --auth to get a refresh token)")
	}

	// Build token request manually to include User-Agent header
	httpClient := &http.Client{Timeout: 30 * time.Second}

	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("username", c.config.Username)
	data.Set("password", c.config.Password)

	req, err := http.NewRequestWithContext(ctx, "POST", c.oauthConfig.Endpoint.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}

	// Set token expiry based on expires_in if not provided
	if token.Expiry.IsZero() && token.AccessToken != "" {
		// Reddit tokens typically expire in 1 hour (3600 seconds)
		token.Expiry = time.Now().Add(1 * time.Hour)
	}

	c.token = &token

	// Save token if store is available
	if c.tokenStore != nil {
		if err := c.tokenStore.SaveToken(c.token); err != nil {
			// Log but don't fail if save fails
			// fmt.Printf("warning: failed to save token: %v\n", err)
		}
	}

	return nil
}

// refreshAndSaveToken refreshes the token using the provided tokenSource and saves it.
// It logs any save errors using slog for diagnostics.
func (c *Client) refreshAndSaveToken(ctx context.Context, ts oauth2.TokenSource) error {
	newToken, err := ts.Token()
	if err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}

	c.token = newToken

	// Save token if store is available and log any errors
	if c.tokenStore != nil {
		if err := c.tokenStore.SaveToken(c.token); err != nil {
			slog.Warn("Failed to save token", "error", err, "source", "refresh")
		}
	}

	return nil
}

// ensureValidToken refreshes the token if it's expired.
func (c *Client) ensureValidToken(ctx context.Context) error {
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
func (c *Client) doRequest(ctx context.Context, method, endpoint string, params url.Values) (*http.Response, error) {
	// Wait for rate limit
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	// Ensure token is valid
	if err := c.ensureValidToken(ctx); err != nil {
		return nil, err
	}

	// Build request URL
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

	// Set headers
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	// Add authorization header
	c.mu.RLock()
	if c.token != nil {
		req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	}
	c.mu.RUnlock()

	// Make request
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Handle rate limit errors
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, ErrRateLimited
	}

	// Handle unauthorized errors
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		// Try to refresh token and retry once
		if err := c.authenticate(ctx); err != nil {
			return nil, ErrUnauthorized
		}
		return c.doRequest(ctx, method, endpoint, params)
	}

	return resp, nil
}

// GetUpvoted fetches upvoted posts for the authenticated user.
// Returns up to 'limit' posts (max 1000 per Reddit API limit).
func (c *Client) GetUpvoted(ctx context.Context, limit int) ([]storage.Post, error) {
	return c.getUserPosts(ctx, "upvoted", limit)
}

// GetSaved fetches saved posts for the authenticated user.
// Returns up to 'limit' posts (max 1000 per Reddit API limit).
func (c *Client) GetSaved(ctx context.Context, limit int) ([]storage.Post, error) {
	return c.getUserPosts(ctx, "saved", limit)
}

// getUserPosts fetches posts from a user endpoint (upvoted or saved).
func (c *Client) getUserPosts(ctx context.Context, endpoint string, limit int) ([]storage.Post, error) {
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
		fetchCount := min(remaining, MaxPostsPerRequest)

		params := url.Values{}
		params.Set("limit", strconv.Itoa(fetchCount))
		if after != nil {
			params.Set("after", *after)
		}

		// Build endpoint URL
		path := fmt.Sprintf("/user/%s/%s", c.config.Username, endpoint)

		resp, err := c.doRequest(ctx, "GET", path, params)
		if err != nil {
			return nil, fmt.Errorf("fetching %s posts: %w", endpoint, err)
		}

		var listing RedditListing
		if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		resp.Body.Close()

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
func (c *Client) Close() error {
	// Save token before closing if store is available
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	if c.tokenStore != nil && token != nil {
		return c.tokenStore.SaveToken(token)
	}
	return nil
}

// GetUsername returns the username of the authenticated user.
func (c *Client) GetUsername() string {
	return c.config.Username
}

// IsAuthenticated returns true if the client has a valid token.
func (c *Client) IsAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token != nil && c.token.Valid()
}

// RefreshToken manually refreshes the access token.
func (c *Client) RefreshToken(ctx context.Context) error {
	return c.authenticate(ctx)
}
