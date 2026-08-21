// Package client implements the Lucidity HTTP API client: token exchange,
// request signing, envelope parsing, and retry/concurrency policy.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	refreshEndpoint = "/external/api/v1/auth/user-token/refresh"

	// accessTokenTTL is Lucidity's documented access-token lifetime.
	accessTokenTTL = 15 * time.Minute
	// proactiveRefreshAge is when we renew ahead of expiry, per CLAUDE.md.
	proactiveRefreshAge = 12 * time.Minute
)

// AuthFailedMessage is the required, verbatim diagnostic text for a 401
// caused by a missing, expired, or revoked refresh token.
const AuthFailedMessage = "authentication failed (401). Check your refresh token — " +
	"make sure it has not expired (default lifetime 30 days) and was not revoked. " +
	"Generate a new token from the Lucidity dashboard (Users → your admin user → Generate Token)."

// AuthError is returned whenever the refresh token is rejected, whether from
// the refresh endpoint directly or after a forced refresh+retry on a 401
// from another endpoint.
type AuthError struct{}

func (AuthError) Error() string { return AuthFailedMessage }

// TokenManager exchanges a long-lived refresh token for short-lived access
// tokens. It proactively renews ahead of the 15-minute expiry and collapses
// concurrent callers into a single in-flight refresh, since Terraform runs
// operations concurrently.
//
// The refresh token and every access token it issues live in memory only:
// TokenManager never persists or logs either.
type TokenManager struct {
	httpClient   *http.Client
	baseURL      string
	refreshToken string

	mu          sync.Mutex
	accessToken string
	issuedAt    time.Time
	refreshing  chan struct{} // non-nil while a refresh is in flight; closed when it completes
	refreshErr  error         // result of the in-flight refresh, valid once refreshing is closed
}

// NewTokenManager constructs a TokenManager. baseURL must not have a
// trailing slash requirement enforced by the caller; it is trimmed here.
func NewTokenManager(httpClient *http.Client, baseURL, refreshToken string) *TokenManager {
	return &TokenManager{
		httpClient:   httpClient,
		baseURL:      strings.TrimRight(baseURL, "/"),
		refreshToken: refreshToken,
	}
}

// AccessToken returns a currently-valid access token, transparently
// refreshing it if absent or past the proactive-renewal age.
func (m *TokenManager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	if m.accessToken != "" && time.Since(m.issuedAt) < proactiveRefreshAge {
		token := m.accessToken
		m.mu.Unlock()
		return token, nil
	}

	if m.refreshing != nil {
		waitCh := m.refreshing
		m.mu.Unlock()
		select {
		case <-waitCh:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		m.mu.Lock()
		token, err := m.accessToken, m.refreshErr
		m.mu.Unlock()
		if err != nil {
			return "", err
		}
		return token, nil
	}

	done := make(chan struct{})
	m.refreshing = done
	m.mu.Unlock()

	token, err := m.doRefresh(ctx)

	m.mu.Lock()
	m.refreshErr = err
	if err == nil {
		m.accessToken = token
		m.issuedAt = time.Now()
	}
	m.refreshing = nil
	m.mu.Unlock()
	close(done)

	return token, err
}

// ForceRefresh discards any cached access token and fetches a new one. This
// is the single sanctioned retry path for a 401 returned by any other
// endpoint: refresh once, retry once, surface the error if it recurs.
func (m *TokenManager) ForceRefresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	m.accessToken = ""
	m.issuedAt = time.Time{}
	m.mu.Unlock()
	return m.AccessToken(ctx)
}

type refreshRequestBody struct {
	RefreshToken string `json:"refreshToken"`
}

type refreshResponseBody struct {
	AccessToken string `json:"accessToken"`
}

func (m *TokenManager) doRefresh(ctx context.Context) (string, error) {
	reqBody, err := json.Marshal(refreshRequestBody{RefreshToken: m.refreshToken})
	if err != nil {
		return "", fmt.Errorf("lucidity: encoding refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+refreshEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("lucidity: building refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("lucidity: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("lucidity: reading refresh response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return "", AuthError{}
	}
	if resp.StatusCode != http.StatusOK {
		return "", parseErrorBody(resp.StatusCode, respBody)
	}

	// The refresh endpoint's success response is the bare {"accessToken": "…"}
	// shape, not the generic {success,data,error,requestId} envelope used by
	// every other endpoint — the API docs specify this explicitly.
	var parsed refreshResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil || parsed.AccessToken == "" {
		return "", fmt.Errorf("lucidity: unexpected refresh response shape")
	}

	return parsed.AccessToken, nil
}
