// Package reddit provides Reddit API client with OAuth2 authentication.
package reddit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// OAuth2CodeFlow performs OAuth2 code flow to obtain a refresh token.
// It opens a browser for user authentication and listens for the callback.
func OAuth2CodeFlow(clientID, clientSecret, userAgent string) (string, error) {
	// Try multiple ports in case of conflict
	defaultPorts := []int{7765, 7766, 7767, 7768, 7769}
	var lastErr error
	for _, port := range defaultPorts {
		token, err := tryOAuth2Flow(clientID, clientSecret, userAgent, port)
		if err == nil {
			return token, nil
		}
		lastErr = err
		// If it's a port conflict, try next port
		if !isPortConflict(err) {
			break
		}
		fmt.Printf("Port %d in use, trying next...\n", port)
	}
	return "", fmt.Errorf("all OAuth ports in use: %w", lastErr)
}

// tryOAuth2Flow attempts OAuth2 flow on a specific port
func tryOAuth2Flow(clientID, clientSecret, userAgent string, port int) (string, error) {
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
		RedirectURL: fmt.Sprintf("http://localhost:%d/callback", port),
		Scopes:      []string{"identity", "history", "read", "save"},
	}

	// Build the authorization URL with duration=permanent to get refresh tokens
	authURL := oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("duration", "permanent"))

	// Open browser for user authentication
	fmt.Println("Opening browser for authentication...")
	fmt.Printf("If the browser doesn't open, visit: %s\n", authURL)
	if err := openURL(authURL); err != nil {
		fmt.Printf("Warning: Failed to open browser: %v\n", err)
	}

	// Start local server to receive callback
	fmt.Printf("Waiting for Reddit callback on http://localhost:%d/callback...\n", port)
	refreshToken, err := waitForCallback(port, state, oauthConfig)
	if err != nil {
		return "", fmt.Errorf("waiting for callback: %w", err)
	}

	fmt.Println("Successfully obtained refresh token!")
	return refreshToken, nil
}

// isPortConflict checks if the error is due to port already in use
func isPortConflict(err error) bool {
	if err == nil {
		return false
	}
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "address already in use") ||
		strings.Contains(errLower, "bind: address already in use") ||
		strings.Contains(errLower, "only one usage of each socket address") ||
		strings.Contains(errLower, "eaddrinuse")
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
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// waitForCallback starts an HTTP server and waits for the OAuth callback.
func waitForCallback(port int, state string, oauthConfig *oauth2.Config) (string, error) {
	resultChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	// Create local ServeMux to avoid global registration issues
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// Check for error in query params - escape user input to prevent XSS
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			escapedErrMsg := html.EscapeString(errMsg)
			escapedDesc := html.EscapeString(r.URL.Query().Get("error_description"))
			fmt.Fprintf(w, "<html><body><h1>Error: %s</h1><p>%s</p></body></html>", escapedErrMsg, escapedDesc)
			errorChan <- fmt.Errorf("oauth error: %s - %s", escapedErrMsg, escapedDesc)
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
		token, err := exchangeCodeForToken(code, oauthConfig)
		if err != nil {
			escapedErr := html.EscapeString(err.Error())
			fmt.Fprintf(w, "<html><body><h1>Error exchanging code: %s</h1></body></html>", escapedErr)
			errorChan <- fmt.Errorf("exchanging code for token: %s", escapedErr)
			return
		}

		// Success - show token
		fmt.Fprintf(w, "<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>")
		resultChan <- token
	})

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{Addr: addr, Handler: mux}

	// Start server in goroutine and capture errors
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe()
	}()

	// Wait for callback with timeout
	timer := time.NewTimer(30 * time.Second)
	defer func() {
		timer.Stop()
	}()

	select {
	case token := <-resultChan:
		_ = server.Close()
		return token, nil
	case err := <-errorChan:
		_ = server.Close()
		return "", err
	case err := <-errChan:
		_ = server.Close()
		return "", fmt.Errorf("server error: %w", err)
	case <-time.After(5 * time.Minute):
		_ = server.Close()
		return "", errors.New("timeout waiting for OAuth callback")
	}
}

// exchangeCodeForToken exchanges an authorization code for a refresh token.
func exchangeCodeForToken(code string, oauthConfig *oauth2.Config) (string, error) {
	// Use oauth2.Config.Exchange with context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("exchanging code for token: %w", err)
	}

	// Verify we got a refresh token
	if token.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token returned - ensure authorization was requested with duration=permanent")
	}

	return token.RefreshToken, nil
}
