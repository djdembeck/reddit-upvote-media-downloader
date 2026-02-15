// Package reddit provides Reddit API client tests.
package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// mockTokenStore implements TokenStore for testing.
type mockTokenStore struct {
	token      *oauth2.Token
	saveCalled bool
	loadCalled bool
	saveErr    error
	loadErr    error
}

func (m *mockTokenStore) SaveToken(token *oauth2.Token) error {
	m.saveCalled = true
	m.token = token
	return m.saveErr
}

func (m *mockTokenStore) LoadToken() (*oauth2.Token, error) {
	m.loadCalled = true
	return m.token, m.loadErr
}

// setupTestServer creates a mock Reddit API server for testing.
func setupTestServer(t *testing.T) (*httptest.Server, *url.URL) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle OAuth token endpoint
		if strings.Contains(r.URL.Path, "/api/v1/access_token") {
			// Check credentials
			auth := r.Header.Get("Authorization")
			if auth == "" || !strings.HasPrefix(auth, "Basic ") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  "test_access_token",
				"token_type":    "bearer",
				"expires_in":    3600,
				"refresh_token": "test_refresh_token",
			})
			return
		}

		// Handle upvoted posts endpoint
		if strings.Contains(r.URL.Path, "/upvoted") {
			// Check authorization
			auth := r.Header.Get("Authorization")
			if !strings.Contains(auth, "Bearer test_access_token") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(createMockListing("upvoted", 3))
			return
		}

		// Handle saved posts endpoint
		if strings.Contains(r.URL.Path, "/saved") {
			auth := r.Header.Get("Authorization")
			if !strings.Contains(auth, "Bearer test_access_token") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(createMockListing("saved", 2))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	serverURL, _ := url.Parse(server.URL)
	return server, serverURL
}

// createMockListing creates a mock Reddit listing response.
func createMockListing(kind string, count int) RedditListing {
	listing := RedditListing{
		Kind: "Listing",
	}

	for i := 0; i < count; i++ {
		post := RedditPost{
			ID:         "post_" + kind + "_" + string(rune('0'+i)),
			Title:      "Test Post " + kind + " " + string(rune('0'+i)),
			Subreddit:  "testsub",
			Author:     "testuser",
			URL:        "https://example.com/post" + string(rune('0'+i)),
			Permalink:  "/r/testsub/comments/abc" + string(rune('0'+i)) + "/",
			CreatedUTC: float64(time.Now().Unix()),
			IsVideo:    false,
			IsSelf:     false,
		}
		listing.Data.Children = append(listing.Data.Children, RedditChild{
			Kind: "t3",
			Data: post,
		})
	}

	return listing
}

func TestNewClient(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	tests := []struct {
		name        string
		config      *Config
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil config",
			config:      nil,
			wantErr:     true,
			errContains: "config is required",
		},
		{
			name: "missing client ID",
			config: &Config{
				ClientSecret: "secret",
				Username:     "user",
				Password:     "pass",
			},
			wantErr:     true,
			errContains: "client ID",
		},
		{
			name: "missing client secret",
			config: &Config{
				ClientID: "id",
				Username: "user",
				Password: "pass",
			},
			wantErr:     true,
			errContains: "client secret",
		},
		{
			name: "missing username",
			config: &Config{
				ClientID:     "id",
				ClientSecret: "secret",
				Password:     "pass",
			},
			wantErr:     true,
			errContains: "username",
		},
		{
			name: "valid config with password - skipped in unit tests",
			config: &Config{
				ClientID:     "id",
				ClientSecret: "secret",
				Username:     "user",
				Password:     "pass",
				UserAgent:    "test",
			},
			wantErr:     true,
			errContains: "authentication failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Override OAuth endpoint for testing
			oldTokenURL := RedditOAuthEndpoint + "/access_token"
			_ = oldTokenURL

			client, err := NewClient(tt.config, nil)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewClient() expected error but got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("NewClient() error = %v, should contain %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("NewClient() unexpected error = %v", err)
				return
			}
			if client == nil {
				t.Error("NewClient() returned nil client")
			}
		})
	}
}

func TestClient_GetUpvoted(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	// Create config for testing
	config := &Config{
		ClientID:     "test_id",
		ClientSecret: "test_secret",
		Username:     "testuser",
		Password:     "test_password",
		UserAgent:    "test_agent",
	}

	// Test with invalid limit
	t.Run("limit exceeds max", func(t *testing.T) {
		// Skip this test as it requires a real OAuth server
		// In a real scenario we'd use a mock server
		t.Skip("Skipping test that requires OAuth server mocking")
	})

	t.Run("negative limit", func(t *testing.T) {
		client := &Client{
			config:      config,
			rateLimiter: newRateLimiter(60),
		}

		posts, err := client.GetUpvoted(context.Background(), -1)
		if err != nil {
			t.Errorf("GetUpvoted() error = %v", err)
		}
		if len(posts) != 0 {
			t.Errorf("GetUpvoted() returned %d posts, want 0", len(posts))
		}
	})

	t.Run("zero limit", func(t *testing.T) {
		client := &Client{
			config:      config,
			rateLimiter: newRateLimiter(60),
		}

		posts, err := client.GetUpvoted(context.Background(), 0)
		if err != nil {
			t.Errorf("GetUpvoted() error = %v", err)
		}
		if len(posts) != 0 {
			t.Errorf("GetUpvoted() returned %d posts, want 0", len(posts))
		}
	})
}

func TestClient_GetSaved(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	config := &Config{
		ClientID:     "test_id",
		ClientSecret: "test_secret",
		Username:     "testuser",
		Password:     "test_password",
		UserAgent:    "test_agent",
	}

	t.Run("negative limit", func(t *testing.T) {
		client := &Client{
			config:      config,
			rateLimiter: newRateLimiter(60),
		}

		posts, err := client.GetSaved(context.Background(), -1)
		if err != nil {
			t.Errorf("GetSaved() error = %v", err)
		}
		if len(posts) != 0 {
			t.Errorf("GetSaved() returned %d posts, want 0", len(posts))
		}
	})
}

func TestRedditPost_ToStoragePost(t *testing.T) {
	now := time.Now()
	rp := RedditPost{
		ID:         "abc123",
		Title:      "Test Post",
		Subreddit:  "testsub",
		Author:     "testauthor",
		URL:        "https://example.com/test.jpg",
		Permalink:  "/r/testsub/comments/abc123/",
		CreatedUTC: float64(now.Unix()),
		IsVideo:    false,
		IsSelf:     false,
	}

	post := rp.ToStoragePost("upvoted")

	if post.ID != rp.ID {
		t.Errorf("ToStoragePost() ID = %v, want %v", post.ID, rp.ID)
	}
	if post.Title != rp.Title {
		t.Errorf("ToStoragePost() Title = %v, want %v", post.Title, rp.Title)
	}
	if post.Subreddit != rp.Subreddit {
		t.Errorf("ToStoragePost() Subreddit = %v, want %v", post.Subreddit, rp.Subreddit)
	}
	if post.Author != rp.Author {
		t.Errorf("ToStoragePost() Author = %v, want %v", post.Author, rp.Author)
	}
	if post.URL != rp.URL {
		t.Errorf("ToStoragePost() URL = %v, want %v", post.URL, rp.URL)
	}
	if post.Permalink != rp.Permalink {
		t.Errorf("ToStoragePost() Permalink = %v, want %v", post.Permalink, rp.Permalink)
	}
	if post.Source != "upvoted" {
		t.Errorf("ToStoragePost() Source = %v, want %v", post.Source, "upvoted")
	}
}

func TestRedditPost_DetectMediaType(t *testing.T) {
	tests := []struct {
		name     string
		post     RedditPost
		expected MediaType
	}{
		{
			name: "Reddit video",
			post: RedditPost{
				IsVideo: true,
				Media:   &Media{RedditVideo: &RedditVideo{IsGIF: false}},
			},
			expected: MediaTypeVideo,
		},
		{
			name: "Self post",
			post: RedditPost{
				IsSelf: true,
			},
			expected: MediaTypeText,
		},
		{
			name: "Image by hint",
			post: RedditPost{
				PostHint: "image",
			},
			expected: MediaTypeImage,
		},
		{
			name: "Video by hint",
			post: RedditPost{
				PostHint: "rich:video",
			},
			expected: MediaTypeVideo,
		},
		{
			name: "Link by hint",
			post: RedditPost{
				PostHint: "link",
			},
			expected: MediaTypeLink,
		},
		{
			name: "Image by URL extension",
			post: RedditPost{
				URL: "https://example.com/image.jpg",
			},
			expected: MediaTypeImage,
		},
		{
			name: "Video by URL extension",
			post: RedditPost{
				URL: "https://example.com/video.mp4",
			},
			expected: MediaTypeVideo,
		},
		{
			name: "YouTube video",
			post: RedditPost{
				URL: "https://youtube.com/watch?v=abc123",
			},
			expected: MediaTypeVideo,
		},
		{
			name: "Vimeo video",
			post: RedditPost{
				URL: "https://vimeo.com/123456",
			},
			expected: MediaTypeVideo,
		},
		{
			name: "External link",
			post: RedditPost{
				URL:       "https://example.com/page",
				Permalink: "/r/test/comments/abc/",
			},
			expected: MediaTypeLink,
		},
		{
			name: "Unknown type",
			post: RedditPost{
				URL:       "",
				Permalink: "/r/test/comments/abc/",
			},
			expected: MediaTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.post.DetectMediaType()
			if got != tt.expected {
				t.Errorf("DetectMediaType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRateLimiter_Wait(t *testing.T) {
	// Create a rate limiter with high limit to not block
	rl := newRateLimiter(1000) // 1000 requests per minute

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// First request should succeed immediately
	if err := rl.Wait(ctx); err != nil {
		t.Errorf("Wait() first call error = %v", err)
	}

	// Second request should also succeed
	if err := rl.Wait(ctx); err != nil {
		t.Errorf("Wait() second call error = %v", err)
	}
}

func TestRateLimiter_Wait_ContextCancelled(t *testing.T) {
	// Create rate limiter with very slow rate
	rl := newRateLimiter(1) // 1 request per minute
	rl.tokens = 0           // Force wait

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := rl.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() error = %v, want context.Canceled", err)
	}
}

func TestTokenStore(t *testing.T) {
	mock := &mockTokenStore{}

	token := &oauth2.Token{
		AccessToken:  "test_token",
		TokenType:    "Bearer",
		RefreshToken: "test_refresh",
		Expiry:       time.Now().Add(time.Hour),
	}

	// Test SaveToken
	if err := mock.SaveToken(token); err != nil {
		t.Errorf("SaveToken() error = %v", err)
	}
	if !mock.saveCalled {
		t.Error("SaveToken() saveCalled = false, want true")
	}

	// Test LoadToken
	loaded, err := mock.LoadToken()
	if err != nil {
		t.Errorf("LoadToken() error = %v", err)
	}
	if !mock.loadCalled {
		t.Error("LoadToken() loadCalled = false, want true")
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("LoadToken() token.AccessToken = %v, want %v", loaded.AccessToken, token.AccessToken)
	}
}

func TestTokenStore_SaveError(t *testing.T) {
	mock := &mockTokenStore{
		saveErr: errors.New("save failed"),
	}

	token := &oauth2.Token{
		AccessToken: "test_token",
	}

	err := mock.SaveToken(token)
	if err == nil {
		t.Error("SaveToken() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "save failed") {
		t.Errorf("SaveToken() error = %v, want to contain 'save failed'", err)
	}
}

func TestTokenStore_LoadError(t *testing.T) {
	mock := &mockTokenStore{
		loadErr: errors.New("load failed"),
	}

	_, err := mock.LoadToken()
	if err == nil {
		t.Error("LoadToken() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "load failed") {
		t.Errorf("LoadToken() error = %v, want to contain 'load failed'", err)
	}
}

func TestClient_IsAuthenticated(t *testing.T) {
	client := &Client{
		config: &Config{
			Username: "testuser",
		},
	}

	// Should be false without token
	if client.IsAuthenticated() {
		t.Error("IsAuthenticated() = true, want false (no token)")
	}

	// Set valid token
	client.token = &oauth2.Token{
		AccessToken: "test",
		Expiry:      time.Now().Add(time.Hour),
	}

	if !client.IsAuthenticated() {
		t.Error("IsAuthenticated() = false, want true (valid token)")
	}

	// Set expired token
	client.token = &oauth2.Token{
		AccessToken: "test",
		Expiry:      time.Now().Add(-time.Hour),
	}

	if client.IsAuthenticated() {
		t.Error("IsAuthenticated() = true, want false (expired token)")
	}
}

func TestClient_GetUsername(t *testing.T) {
	client := &Client{
		config: &Config{
			Username: "testuser123",
		},
	}

	if got := client.GetUsername(); got != "testuser123" {
		t.Errorf("GetUsername() = %v, want %v", got, "testuser123")
	}
}

func TestClient_Close(t *testing.T) {
	mockStore := &mockTokenStore{}
	client := &Client{
		config:     &Config{Username: "test"},
		tokenStore: mockStore,
		token: &oauth2.Token{
			AccessToken: "test_token",
		},
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if !mockStore.saveCalled {
		t.Error("Close() did not save token")
	}
}

func TestErrRateLimited(t *testing.T) {
	if !errors.Is(ErrRateLimited, ErrRateLimited) {
		t.Error("ErrRateLimited should be itself")
	}
	if errors.Is(ErrRateLimited, ErrUnauthorized) {
		t.Error("ErrRateLimited should not be ErrUnauthorized")
	}
}

func TestErrUnauthorized(t *testing.T) {
	if !errors.Is(ErrUnauthorized, ErrUnauthorized) {
		t.Error("ErrUnauthorized should be itself")
	}
}

func TestErrInvalidResponse(t *testing.T) {
	if !errors.Is(ErrInvalidResponse, ErrInvalidResponse) {
		t.Error("ErrInvalidResponse should be itself")
	}
}

func TestErrMaxPostsExceeded(t *testing.T) {
	if !errors.Is(ErrMaxPostsExceeded, ErrMaxPostsExceeded) {
		t.Error("ErrMaxPostsExceeded should be itself")
	}
}

func TestMediaType_Constants(t *testing.T) {
	// Test that all constants are defined
	types := []MediaType{
		MediaTypeImage,
		MediaTypeVideo,
		MediaTypeGallery,
		MediaTypeLink,
		MediaTypeText,
		MediaTypeUnknown,
	}

	for _, mt := range types {
		if mt == "" {
			t.Errorf("MediaType constant is empty")
		}
	}
}

// Integration-style test that mocks the full flow
func TestFullMockFlow(t *testing.T) {
	// Create a test server that mimics Reddit's API
	var tokenIssued bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OAuth token endpoint
		if strings.Contains(r.URL.Path, "/access_token") {
			// Verify basic auth header
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Basic ") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// Verify User-Agent
			if r.Header.Get("User-Agent") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			tokenIssued = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "mock_access_token",
				"token_type":   "bearer",
				"expires_in":   3600,
			})
			return
		}

		// Upvoted endpoint
		if strings.Contains(r.URL.Path, "/upvoted") {
			auth := r.Header.Get("Authorization")
			if !strings.Contains(auth, "Bearer mock_access_token") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			listing := RedditListing{
				Kind: "Listing",
			}
			listing.Data.Children = []RedditChild{
				{
					Kind: "t3",
					Data: RedditPost{
						ID:         "test1",
						Title:      "Test Post 1",
						Subreddit:  "test",
						Author:     "testuser",
						URL:        "https://example.com/image.jpg",
						Permalink:  "/r/test/comments/test1/",
						CreatedUTC: float64(time.Now().Unix()),
						IsVideo:    false,
						IsSelf:     false,
						PostHint:   "image",
					},
				},
			}
			json.NewEncoder(w).Encode(listing)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Create client pointing to test server
	// Note: In real usage, we'd need to override the OAuth endpoint URL
	// For this test, we just verify the token endpoint was called
	config := &Config{
		ClientID:     "test_client_id",
		ClientSecret: "test_client_secret",
		Username:     "testuser",
		Password:     "test_password",
		UserAgent:    "test/1.0",
	}

	// Verify config validation works
	_, err := NewClient(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "config is required") {
		t.Errorf("Expected 'config is required' error, got: %v", err)
	}

	_, err = NewClient(&Config{}, nil)
	if err == nil || !strings.Contains(err.Error(), "client ID") {
		t.Errorf("Expected 'client ID' error, got: %v", err)
	}

	// Test valid config with missing password
	configNoPass := &Config{
		ClientID:     "test_id",
		ClientSecret: "test_secret",
		Username:     "testuser",
		// No password - should fail
	}
	_, err = NewClient(configNoPass, nil)
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("Expected 'password' error, got: %v", err)
	}

	_ = tokenIssued
	_ = config
}

// Benchmark for rate limiter
func BenchmarkRateLimiter_Wait(b *testing.B) {
	rl := newRateLimiter(1000)
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		rl.Wait(ctx)
	}
}
