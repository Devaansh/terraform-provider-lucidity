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

const refreshEndpoint = "/external/api/v1/auth/user-token/refresh"

// AccessTokenTTL is Lucidity's documented access-token lifetime.
const AccessTokenTTL = 15 * time.Minute

// DefaultProactiveRefreshAge is how long to wait before renewing an access
// token when the provider's proactive_refresh_buffer_minutes attribute is
// unset — a 3-minute buffer ahead of the real expiry, per CLAUDE.md.
const DefaultProactiveRefreshAge = AccessTokenTTL - 3*time.Minute

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

	// proactiveRefreshAge and retryBaseDelay are configured by the caller
	// (NewClient) from provider attributes / test overrides, rather than
	// being package constants, so both are adjustable per Client instance.
	proactiveRefreshAge time.Duration
	retryBaseDelay      time.Duration

	mu          sync.Mutex
	accessToken string
	issuedAt    time.Time
	refreshing  chan struct{} // non-nil while a refresh is in flight; closed when it completes
	refreshErr  error         // result of the in-flight refresh, valid once refreshing is closed

	// DebugLog, if set, receives non-secret call metadata (status, attempt)
	// after every refresh attempt. Never passes token material. Wired by
	// NewClient to Client.logDebug, same contract as Client.DebugLog.
	DebugLog func(ctx context.Context, msg string, fields map[string]any)
}

func (m *TokenManager) logDebug(ctx context.Context, msg string, fields map[string]any) {
	if m.DebugLog != nil {
		m.DebugLog(ctx, msg, fields)
	}
}

// NewTokenManager constructs a TokenManager. baseURL must not have a
// trailing slash requirement enforced by the caller; it is trimmed here.
func NewTokenManager(httpClient *http.Client, baseURL, refreshToken string, proactiveRefreshAge, retryBaseDelay time.Duration) *TokenManager {
	return &TokenManager{
		httpClient:          httpClient,
		baseURL:             strings.TrimRight(baseURL, "/"),
		refreshToken:        refreshToken,
		proactiveRefreshAge: proactiveRefreshAge,
		retryBaseDelay:      retryBaseDelay,
	}
}

// AccessToken returns a currently-valid access token, transparently
// refreshing it if absent or past the proactive-renewal age.
func (m *TokenManager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	if m.accessToken != "" && time.Since(m.issuedAt) < m.proactiveRefreshAge {
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

	// The refresh is shared: other goroutines are waiting on `done` above
	// regardless of which caller's context happened to start it. Detach from
	// this caller's cancellation (context.WithoutCancel keeps values, drops
	// cancellation) so one goroutine's ctx being cancelled doesn't abort a
	// refresh every other waiter still needs. Still bounded — NewClient's
	// http.Client has its own 30s Timeout regardless of context.
	token, err := m.doRefresh(context.WithoutCancel(ctx))

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

// ForceRefresh discards the cached access token and fetches a new one — but
// only if the cache still holds failedToken, the token that actually got the
// 401. If another goroutine already refreshed in the interim (its result
// lands here as m.accessToken != failedToken), that fresher token is
// returned directly instead of triggering a redundant refresh. This is the
// single sanctioned retry path for a 401 returned by any other endpoint:
// refresh once, retry once, surface the error if it recurs.
func (m *TokenManager) ForceRefresh(ctx context.Context, failedToken string) (string, error) {
	m.mu.Lock()
	if m.accessToken != "" && m.accessToken != failedToken {
		token := m.accessToken
		m.mu.Unlock()
		return token, nil
	}
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

// doRefresh exchanges the refresh token for a new access token, retrying 5xx
// responses with backoff exactly like Client.Do does for every other
// endpoint (CLAUDE.md's retry policy isn't specific to non-auth calls).
// Deliberately does not retry raw transport errors — same policy as Do.
func (m *TokenManager) doRefresh(ctx context.Context) (string, error) {
	reqBody, err := json.Marshal(refreshRequestBody{RefreshToken: m.refreshToken})
	if err != nil {
		return "", fmt.Errorf("lucidity: encoding refresh request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		status, respBody, err := m.doRefreshOnce(ctx, reqBody)
		if err != nil {
			return "", err
		}

		// Non-secret metadata only — never reqBody (the refresh token) or
		// respBody (the freshly-issued access token).
		m.logDebug(ctx, "lucidity refresh token call", map[string]any{
			"status":  status,
			"attempt": attempt,
		})

		switch {
		case status == http.StatusUnauthorized:
			return "", AuthError{}
		case status == http.StatusOK:
			return parseRefreshResponse(respBody)
		case status >= 500 && attempt < maxRetryAttempts-1:
			select {
			case <-time.After(backoffDelay(m.retryBaseDelay, attempt)):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			continue
		default:
			return "", parseErrorBody(status, respBody)
		}
	}
}

func (m *TokenManager) doRefreshOnce(ctx context.Context, reqBody []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+refreshEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: building refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: reading refresh response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// parseRefreshResponse parses the refresh endpoint's success response, which
// is the bare {"accessToken": "…"} shape, not the generic
// {success,data,error,requestId} envelope used by every other endpoint — the
// API docs specify this explicitly.
func parseRefreshResponse(respBody []byte) (string, error) {
	var parsed refreshResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil || parsed.AccessToken == "" {
		return "", fmt.Errorf("lucidity: unexpected refresh response shape")
	}
	return parsed.AccessToken, nil
}
