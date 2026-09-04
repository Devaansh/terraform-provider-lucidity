package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	headerAuthType = "X-Authtype"
	authTypeValue  = "lucidity_access_token"

	defaultMaxParallelRequests = 10
	maxRetryAttempts           = 4 // 1 initial + 3 retries
	defaultRetryBaseDelay      = 500 * time.Millisecond
)

// envelope is the generic {success,data,error,requestId} response shape used
// by every Lucidity endpoint except the raw refresh-token response.
type envelope struct {
	Success   bool            `json:"success"`
	Data      json.RawMessage `json:"data"`
	Error     *envelopeError  `json:"error"`
	RequestID string          `json:"requestId"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is a parsed Lucidity API error, carrying the requestId needed for
// support escalation per CLAUDE.md.
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("lucidity API error [%s]: %s (requestId=%s)", e.Code, e.Message, e.RequestID)
	}
	return fmt.Sprintf("lucidity API error [%s]: %s", e.Code, e.Message)
}

func parseErrorBody(status int, body []byte) error {
	var env envelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil {
		return &APIError{
			HTTPStatus: status,
			Code:       env.Error.Code,
			Message:    env.Error.Message,
			RequestID:  env.RequestID,
		}
	}
	// Render unmapped/unparseable bodies generically but completely, per
	// CLAUDE.md (CONFLICT semantics in particular are unconfirmed upstream).
	return &APIError{
		HTTPStatus: status,
		Code:       "UNKNOWN",
		Message:    fmt.Sprintf("unexpected response body: %s", truncate(string(body), 500)),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Client is the Lucidity API HTTP client: it attaches auth headers, retries
// 5xx responses with backoff, retries exactly once on a 401 after forcing a
// token refresh, bounds concurrency, and never logs secret material.
type Client struct {
	httpClient *http.Client
	baseURL    string
	tokens     *TokenManager
	sem        chan struct{}

	retryBaseDelay      time.Duration // overridable in tests to avoid slow sleeps
	proactiveRefreshAge time.Duration

	// DebugLog, if set, receives non-secret call metadata (method, path,
	// status, requestId) after every attempt. Client never passes token or
	// body material to it. Providers wire this to tflog.Debug.
	DebugLog func(ctx context.Context, msg string, fields map[string]any)
}

func (c *Client) logDebug(ctx context.Context, msg string, fields map[string]any) {
	if c.DebugLog != nil {
		c.DebugLog(ctx, msg, fields)
	}
}

func bestEffortRequestID(body []byte) string {
	var env envelope
	if err := json.Unmarshal(body, &env); err == nil {
		return env.RequestID
	}
	return ""
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithMaxParallelRequests bounds in-flight requests. Defaults to 10, mirrors
// the provider's max_parallel_requests attribute.
func WithMaxParallelRequests(n int) Option {
	return func(c *Client) {
		if n <= 0 {
			n = defaultMaxParallelRequests
		}
		c.sem = make(chan struct{}, n)
	}
}

// WithHTTPClient overrides the underlying *http.Client (tests only).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithProactiveRefreshAge overrides how long an access token is used before
// it's proactively renewed ahead of its real expiry (AccessTokenTTL).
// Defaults to DefaultProactiveRefreshAge; driven by the provider's
// proactive_refresh_buffer_minutes attribute.
func WithProactiveRefreshAge(d time.Duration) Option {
	return func(c *Client) { c.proactiveRefreshAge = d }
}

// withRetryBaseDelay overrides the exponential-backoff base delay (tests only).
func withRetryBaseDelay(d time.Duration) Option {
	return func(c *Client) { c.retryBaseDelay = d }
}

// NewClient constructs a Client. refreshToken must already be resolved from
// whichever provider-config source the user chose (attribute, file, command,
// or env var) — Client itself is agnostic to where it came from.
func NewClient(baseURL, refreshToken string, opts ...Option) *Client {
	hc := &http.Client{Timeout: 30 * time.Second}
	c := &Client{
		httpClient:          hc,
		baseURL:             baseURL,
		sem:                 make(chan struct{}, defaultMaxParallelRequests),
		retryBaseDelay:      defaultRetryBaseDelay,
		proactiveRefreshAge: DefaultProactiveRefreshAge,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.tokens = NewTokenManager(c.httpClient, baseURL, refreshToken, c.proactiveRefreshAge, c.retryBaseDelay)
	return c
}

// Do executes an authenticated API call against path with the given JSON
// body (nil for none), decoding the envelope's data field into out (nil to
// discard it).
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("lucidity: encoding request body: %w", err)
		}
		bodyBytes = b
	}

	// forcedRefresh caps the 401-forced-refresh retry at exactly one
	// occurrence (a second 401 always returns AuthError below) — it does NOT
	// share the 5xx backoff budget tracked by attempt. Keeping the two
	// independent is deliberate: they used to share one bounded counter, and
	// a 401 landing on what would have been the final 5xx attempt silently
	// dropped the forced-refresh retry entirely (fixed 2026-09-03).
	forcedRefresh := false
	for attempt := 0; ; attempt++ {
		token, err := c.tokens.AccessToken(ctx)
		if err != nil {
			return err
		}

		status, respBody, err := c.doOnce(ctx, method, path, bodyBytes, token)
		if err != nil {
			return err
		}

		switch {
		case status == http.StatusUnauthorized && !forcedRefresh:
			forcedRefresh = true
			if _, err := c.tokens.ForceRefresh(ctx); err != nil {
				return err
			}
			continue // single sanctioned retry, per CLAUDE.md — doesn't consume attempt
		case status == http.StatusUnauthorized:
			return AuthError{}
		case status >= 500:
			if attempt >= maxRetryAttempts-1 {
				return parseErrorBody(status, respBody)
			}
			select {
			case <-time.After(c.backoff(attempt)):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		case status >= 400:
			return parseErrorBody(status, respBody) // never auto-retry 4xx
		default:
			return decodeEnvelope(respBody, out)
		}
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	return backoffDelay(c.retryBaseDelay, attempt)
}

// backoffDelay is the shared exponential-backoff calculation used by both
// Client.Do (via Client.backoff) and TokenManager.doRefresh, so the two
// retry loops behave identically.
func backoffDelay(base time.Duration, attempt int) time.Duration {
	return base * time.Duration(math.Pow(2, float64(attempt)))
}

func (c *Client) doOnce(ctx context.Context, method, path string, bodyBytes []byte, accessToken string) (int, []byte, error) {
	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: building request: %w", err)
	}
	req.Header.Set(headerAuthType, authTypeValue)
	req.Header.Set("Authorization", accessToken) // no "Bearer " prefix, enforced here only
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("lucidity: reading response: %w", err)
	}

	c.logDebug(ctx, "lucidity api call", map[string]any{
		"method":    method,
		"path":      path,
		"status":    resp.StatusCode,
		"requestId": bestEffortRequestID(respBody),
	})

	return resp.StatusCode, respBody, nil
}

func decodeEnvelope(body []byte, out any) error {
	if out == nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("lucidity: decoding response envelope: %w", err)
	}
	if len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("lucidity: decoding response data: %w", err)
	}
	return nil
}
