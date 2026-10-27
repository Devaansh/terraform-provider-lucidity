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

// tokenEndpoint is the revamped API's single token endpoint for both users
// (refresh token) and service accounts (client credentials), per the
// "Access Token Generation - User and Service Accounts" doc. It replaces the
// legacy /external/api/v1/auth/user-token/refresh.
const tokenEndpoint = "/v1/oauth2/token"

// AccessTokenTTL is Lucidity's documented access-token lifetime.
const AccessTokenTTL = 15 * time.Minute

// DefaultProactiveRefreshAge is how long to wait before renewing an access
// token when the provider's proactive_refresh_buffer_minutes attribute is
// unset — a 3-minute buffer ahead of the real expiry.
const DefaultProactiveRefreshAge = AccessTokenTTL - 3*time.Minute

// AuthFailedMessage is the diagnostic text for a 401 when the provider
// authenticates with a user's refresh token.
const AuthFailedMessage = "authentication failed (401). Lucidity rejected the refresh token — " +
	"it may be wrong, expired (default lifetime 30 days) or revoked. " +
	"Generate a new one in the Lucidity dashboard."

// ServiceAccountAuthFailedMessage is the diagnostic text for a 401 when the
// provider authenticates with service account client credentials. The
// causes are the ones the token endpoint documents for 401 UNAUTHORIZED.
const ServiceAccountAuthFailedMessage = "authentication failed (401). Lucidity rejected the service account credentials — " +
	"the client ID or secret may be wrong, the credential may be revoked or expired, " +
	"or the service account may be disabled or deleted. Check the service account in Lucidity, or create a new credential for it."

// AuthError is returned whenever the credentials are rejected, whether by
// the token endpoint directly or after a forced refresh+retry on a 401 from
// another endpoint.
type AuthError struct {
	// ServiceAccount is true when the rejected credentials were client
	// credentials rather than a refresh token.
	ServiceAccount bool
}

func (e AuthError) Error() string {
	if e.ServiceAccount {
		return ServiceAccountAuthFailedMessage
	}
	return AuthFailedMessage
}

// Credentials are what the provider exchanges for access tokens: exactly
// one of a user's RefreshToken, or a service account's ClientID and
// ClientSecret. Never logged.
type Credentials struct {
	RefreshToken string
	ClientID     string
	ClientSecret string
}

// IsServiceAccount reports whether these are client credentials.
func (c Credentials) IsServiceAccount() bool { return c.ClientID != "" }

// TokenManager exchanges credentials (a refresh token or client
// credentials) for short-lived access tokens. It proactively renews ahead of
// the 15-minute expiry and collapses concurrent callers into a single
// in-flight request, since Terraform runs operations concurrently. Renewal
// re-sends the same credentials: the token endpoint issues no refresh token.
//
// The credentials and every access token they buy live in memory only:
// TokenManager never persists or logs any of them.
type TokenManager struct {
	httpClient  *http.Client
	baseURL     string
	credentials Credentials

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
func NewTokenManager(httpClient *http.Client, baseURL string, credentials Credentials, proactiveRefreshAge, retryBaseDelay time.Duration) *TokenManager {
	return &TokenManager{
		httpClient:          httpClient,
		baseURL:             strings.TrimRight(baseURL, "/"),
		credentials:         credentials,
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

// tokenRequestBody is the POST /v1/oauth2/token body: either refreshToken
// (user) or clientId + clientSecret (service account), never both.
type tokenRequestBody struct {
	RefreshToken string `json:"refreshToken,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// tokenResponseData is the token endpoint's success payload, inside the
// standard {success,data,requestId} envelope.
type tokenResponseData struct {
	AccessToken string `json:"accessToken"`
}

// doRefresh exchanges the credentials for a new access token, retrying 5xx
// responses with backoff exactly like Client.Do does for every other
// endpoint (the retry policy below isn't specific to non-auth calls).
// Deliberately does not retry raw transport errors — same policy as Do.
func (m *TokenManager) doRefresh(ctx context.Context) (string, error) {
	body := tokenRequestBody{RefreshToken: m.credentials.RefreshToken}
	if m.credentials.IsServiceAccount() {
		body = tokenRequestBody{ClientID: m.credentials.ClientID, ClientSecret: m.credentials.ClientSecret}
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("lucidity: encoding token request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		status, respBody, err := m.doRefreshOnce(ctx, reqBody)
		if err != nil {
			return "", err
		}

		// Non-secret metadata only — never reqBody (the credentials) or
		// respBody (the freshly-issued access token).
		m.logDebug(ctx, "lucidity token call", map[string]any{
			"status":         status,
			"attempt":        attempt,
			"serviceAccount": m.credentials.IsServiceAccount(),
			"requestId":      bestEffortRequestID(respBody),
		})

		switch {
		case status == http.StatusUnauthorized:
			return "", AuthError{ServiceAccount: m.credentials.IsServiceAccount()}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+tokenEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: reading token response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// parseRefreshResponse parses the token endpoint's success response: the
// accessToken inside the standard {success,data,requestId} envelope.
// decodeEnvelope also accepts a bare {"accessToken": "…"}, the legacy
// refresh endpoint's shape, so a backend still answering that way works.
func parseRefreshResponse(respBody []byte) (string, error) {
	var parsed tokenResponseData
	if err := decodeEnvelope(respBody, &parsed); err != nil || parsed.AccessToken == "" {
		return "", fmt.Errorf("lucidity: unexpected token response shape")
	}
	return parsed.AccessToken, nil
}
