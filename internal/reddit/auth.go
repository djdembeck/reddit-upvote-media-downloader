// Package reddit provides Reddit API client with OAuth2 authentication.
package reddit

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// OAuth2CodeFlow performs OAuth2 code flow to obtain a refresh token.
// It opens a browser for user authentication and listens for the callback.
func OAuth2CodeFlow(clientID, clientSecret, userAgent string) (string, error) {
	// Generate random state for CSRF protection
	state, err := generateRandomState(16)
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}

	// Create OAuth config
	oauthConfig := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: RedditOAuthEndpoint + "/access_token",
			AuthURL:  RedditOAuthEndpoint + "/authorize",
		},
		Scopes: []string{"identity", "history", "read", "save"},
	}

	// Build the authorization URL
	authURL := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)

	// Open browser for user authentication
	fmt.Println("Opening browser for authentication...")
	fmt.Printf("If the browser doesn't open, visit: %s\n", authURL)
	_ = openURL(authURL)

	// Start local server to receive callback
	fmt.Println("Waiting for Reddit callback on http://localhost:7765...")
	refreshToken, err := waitForCallback(7765, state, oauthConfig, clientSecret, userAgent)
	if err != nil {
		return "", fmt.Errorf("waiting for callback: %w", err)
	}

	fmt.Println("Successfully obtained refresh token!")
	return refreshToken, nil
}

// generateRandomState generates a random string for OAuth state parameter.
func generateRandomState(n int) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		result[i] = letters[num.Int64()]
	}
	return string(result), nil
}

// openURL opens a URL in the default browser.
func openURL(url string) error {
	return nil
}

// waitForCallback starts an HTTP server and waits for the OAuth callback.
func waitForCallback(port int, state string, oauthConfig *oauth2.Config, clientSecret, userAgent string) (string, error) {
	resultChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check for error in query params
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			fmt.Fprintf(w, "<html><body><h1>Error: %s</h1><p>%s</p></body></html>", errMsg, r.URL.Query().Get("error_description"))
			errorChan <- fmt.Errorf("oauth error: %s - %s", errMsg, r.URL.Query().Get("error_description"))
			return
		}

		// Verify state matches
		if r.URL.Query().Get("state") != state {
			fmt.Fprintf(w, "<html><body><h1>State mismatch!</h1></body></html>")
			errorChan <- errors.New("state mismatch")
			return
		}

		// Exchange code for token
		code := r.URL.Query().Get("code")
		token, err := exchangeCodeForToken(code, oauthConfig, clientSecret, userAgent)
		if err != nil {
			fmt.Fprintf(w, "<html><body><h1>Error exchanging code: %v</h1></body></html>", err)
			errorChan <- err
			return
		}

		// Success - show token
		fmt.Fprintf(w, "<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>")
		// Write token to file for automation
		_ = os.WriteFile("./refresh_token.txt", []byte(token), 0600)
		fmt.Println("\nRefresh token saved to ./refresh_token.txt")
		resultChan <- token
	})

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{Addr: addr}
	go server.ListenAndServe()
	defer server.Close()

	// Wait for callback with timeout
	select {
	case token := <-resultChan:
		return token, nil
	case err := <-errorChan:
		return "", err
	case <-time.After(5 * time.Minute):
		return "", errors.New("timeout waiting for OAuth callback")
	}
}

// exchangeCodeForToken exchanges an authorization code for a refresh token.
func exchangeCodeForToken(code string, oauthConfig *oauth2.Config, clientSecret, userAgent string) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", "http://localhost:7765")

	req, err := http.NewRequest("POST", oauthConfig.Endpoint.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.SetBasicAuth(oauthConfig.ClientID, clientSecret)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}

	return token.RefreshToken, nil
}
