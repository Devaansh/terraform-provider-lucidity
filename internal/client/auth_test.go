package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestTokenManager builds a TokenManager with sensible test defaults: the
// real proactive-renewal age (tests that care about it backdate issuedAt
// directly) and a near-zero retry base delay so backoff-retry tests stay fast.
func newTestTokenManager(httpClient *http.Client, baseURL, refreshToken string) *TokenManager {
	return NewTokenManager(httpClient, baseURL, Credentials{RefreshToken: refreshToken}, DefaultProactiveRefreshAge, time.Millisecond)
}

// writeTokenResponse answers a token request the way the revamped token
// endpoint documents: the access token inside the standard envelope.
func writeTokenResponse(w http.ResponseWriter, accessToken string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":   true,
		"data":      map[string]string{"accessToken": accessToken},
		"requestId": "token-request-id",
	})
}

func refreshServer(t *testing.T, accessToken func(call int) string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenEndpoint {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, accessToken(int(n)))
	}))
	return srv, &calls
}

func TestTokenManager_RefreshHappyPath(t *testing.T) {
	srv, _ := refreshServer(t, func(int) string { return "token-1" })
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")
	got, err := tm.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "token-1" {
		t.Fatalf("got %q, want token-1", got)
	}
}

func TestTokenManager_ProactiveRenewal(t *testing.T) {
	srv, calls := refreshServer(t, func(n int) string { return fmt.Sprintf("token-%d", n) })
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")

	first, err := tm.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if first != "token-1" {
		t.Fatalf("got %q, want token-1", first)
	}

	// Still fresh: no second call.
	if _, err := tm.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("expected 1 refresh call while fresh, got %d", got)
	}

	// Backdate issuedAt past the proactive-renewal mark.
	tm.mu.Lock()
	tm.issuedAt = time.Now().Add(-tm.proactiveRefreshAge - time.Second)
	tm.mu.Unlock()

	second, err := tm.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if second != "token-2" {
		t.Fatalf("got %q, want token-2 after proactive renewal", second)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("expected 2 refresh calls after proactive renewal, got %d", got)
	}
}

func TestTokenManager_SingleFlight(t *testing.T) {
	release := make(chan struct{})
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		<-release // hold every concurrent caller here to prove they collapse into one request
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "shared-token")
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")

	const n = 20
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			token, err := tm.AccessToken(context.Background())
			if err != nil {
				t.Errorf("AccessToken: %v", err)
				return
			}
			results <- token
		}()
	}

	// Give goroutines time to queue up behind the single in-flight refresh.
	time.Sleep(100 * time.Millisecond)
	close(release)

	for i := 0; i < n; i++ {
		if got := <-results; got != "shared-token" {
			t.Fatalf("got %q, want shared-token", got)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 refresh call under concurrency, got %d", got)
	}
}

func TestTokenManager_ExpiredRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "expired-secret")
	_, err := tm.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != AuthFailedMessage {
		t.Fatalf("got %q, want the exact required auth-failed message", err.Error())
	}
}

func TestTokenManager_RefreshRetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "token-after-retries")
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")
	got, err := tm.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "token-after-retries" {
		t.Fatalf("got %q, want token-after-retries", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", n)
	}
}

func TestTokenManager_RefreshExhaustsRetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")
	_, err := tm.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got %T, want *APIError", err)
	}
	if apiErr.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500", apiErr.HTTPStatus)
	}
	if n := atomic.LoadInt32(&calls); n != maxRetryAttempts {
		t.Fatalf("expected exactly %d calls (retry budget exhausted), got %d", maxRetryAttempts, n)
	}
}

func TestTokenManager_DebugLogNeverLeaksTokens(t *testing.T) {
	const refreshSecret = "super-secret-refresh-token-value"
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError) // force one retry into the log too
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "issued-access-token")
	}))
	defer srv.Close()

	tm := NewTokenManager(srv.Client(), srv.URL, Credentials{RefreshToken: refreshSecret}, DefaultProactiveRefreshAge, time.Millisecond)

	var captured strings.Builder
	tm.DebugLog = func(_ context.Context, msg string, fields map[string]any) {
		captured.WriteString(msg)
		for k, v := range fields {
			fmt.Fprintf(&captured, " %s=%v", k, v)
		}
		captured.WriteString("\n")
	}

	got, err := tm.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "issued-access-token" {
		t.Fatalf("got %q, want issued-access-token", got)
	}

	logOutput := captured.String()
	if logOutput == "" {
		t.Fatal("expected DebugLog to be called at least once")
	}
	if strings.Contains(logOutput, refreshSecret) {
		t.Fatalf("refresh token leaked into debug log: %s", logOutput)
	}
	if strings.Contains(logOutput, "issued-access-token") {
		t.Fatalf("issued access token leaked into debug log: %s", logOutput)
	}
	if !strings.Contains(logOutput, "status=500") || !strings.Contains(logOutput, "status=200") {
		t.Fatalf("expected both the failed and successful attempt's status logged, got: %s", logOutput)
	}
}

// TestTokenManager_RefreshSurvivesCallerCancellation proves the
// context.WithoutCancel fix: a refresh is shared with any other goroutine
// that arrives while it's in flight, so it must not abort just because the
// particular caller that happened to initiate it later cancels its own
// context. Without the fix, this test fails — the in-flight HTTP request is
// tied to ctx via http.NewRequestWithContext, so cancelling ctx aborts it
// and AccessToken surfaces context.Canceled instead of the real result.
func TestTokenManager_RefreshSurvivesCallerCancellation(t *testing.T) {
	reachedServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reachedServer)
		time.Sleep(20 * time.Millisecond) // give the test time to cancel before responding
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "survived-token")
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-reachedServer
		cancel()
	}()

	token, err := tm.AccessToken(ctx)
	if err != nil {
		t.Fatalf("AccessToken: %v (expected it to survive the caller's own context cancellation)", err)
	}
	if token != "survived-token" {
		t.Fatalf("got %q, want survived-token", token)
	}
}

func TestTokenManager_ForceRefresh_SkipsRefetchIfAlreadyFresh(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "should-not-be-issued")
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")
	tm.mu.Lock()
	tm.accessToken = "already-fresh-token"
	tm.issuedAt = time.Now()
	tm.mu.Unlock()

	got, err := tm.ForceRefresh(context.Background(), "stale-token-that-does-not-match")
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if got != "already-fresh-token" {
		t.Fatalf("got %q, want already-fresh-token (no refetch)", got)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("expected 0 refresh-server calls, got %d", n)
	}
}

func TestTokenManager_ForceRefresh_RefetchesIfStillStale(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		writeTokenResponse(w, "new-token")
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "refresh-secret")
	tm.mu.Lock()
	tm.accessToken = "stale-token"
	tm.issuedAt = time.Now()
	tm.mu.Unlock()

	got, err := tm.ForceRefresh(context.Background(), "stale-token")
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if got != "new-token" {
		t.Fatalf("got %q, want new-token", got)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected exactly 1 refresh-server call, got %d", n)
	}
}

// tokenCall captures what a token request carried.
type tokenCall struct {
	path          string
	authorization string
	body          map[string]any
}

func capturingTokenServer(t *testing.T, calls *[]tokenCall) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, tokenCall{path: r.URL.Path, authorization: r.Header.Get("Authorization"), body: body})
		writeTokenResponse(w, "issued")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Users send only refreshToken, to /v1/oauth2/token, with no Authorization.
func TestTokenManager_UserRequestShape(t *testing.T) {
	var calls []tokenCall
	srv := capturingTokenServer(t, &calls)

	tm := newTestTokenManager(srv.Client(), srv.URL, "access.secret")
	if _, err := tm.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	c := calls[0]
	if c.path != "/v1/oauth2/token" || c.authorization != "" {
		t.Fatalf("got path %q authorization %q, want /v1/oauth2/token and none", c.path, c.authorization)
	}
	if len(c.body) != 1 || c.body["refreshToken"] != "access.secret" {
		t.Fatalf("got body %v, want only refreshToken", c.body)
	}
}

// Service accounts send only clientId and clientSecret — never both shapes.
func TestTokenManager_ServiceAccountRequestShape(t *testing.T) {
	var calls []tokenCall
	srv := capturingTokenServer(t, &calls)

	tm := NewTokenManager(srv.Client(), srv.URL, Credentials{ClientID: "svc-1", ClientSecret: "s3cret"}, DefaultProactiveRefreshAge, time.Millisecond)
	if _, err := tm.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	c := calls[0]
	if len(c.body) != 2 || c.body["clientId"] != "svc-1" || c.body["clientSecret"] != "s3cret" {
		t.Fatalf("got body %v, want only clientId and clientSecret", c.body)
	}
}

func TestTokenManager_ServiceAccount401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":   false,
			"error":     map[string]string{"code": "UNAUTHORIZED", "message": "invalid client credentials"},
			"requestId": "r-401",
		})
	}))
	defer srv.Close()

	tm := NewTokenManager(srv.Client(), srv.URL, Credentials{ClientID: "svc-1", ClientSecret: "wrong"}, DefaultProactiveRefreshAge, time.Millisecond)
	_, err := tm.AccessToken(context.Background())
	if err == nil || err.Error() != ServiceAccountAuthFailedMessage {
		t.Fatalf("got %v, want the service account auth-failed message", err)
	}
}

// A 400 from the token endpoint keeps its documented error.code.
func TestTokenManager_ValidationErrorKeepsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":   false,
			"error":     map[string]string{"code": "INVALID_REQUEST", "message": "refresh token must be <accessKey>.<secretKey>"},
			"requestId": "r-400",
		})
	}))
	defer srv.Close()

	tm := newTestTokenManager(srv.Client(), srv.URL, "no-dot")
	_, err := tm.AccessToken(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "INVALID_REQUEST" || apiErr.RequestID != "r-400" {
		t.Fatalf("got %v, want *APIError INVALID_REQUEST with requestId r-400", err)
	}
}

// The legacy refresh endpoint answered with a bare {"accessToken": ...};
// that shape still parses.
func TestTokenManager_AcceptsBareTokenResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "bare-token"})
	}))
	defer srv.Close()

	got, err := newTestTokenManager(srv.Client(), srv.URL, "a.b").AccessToken(context.Background())
	if err != nil || got != "bare-token" {
		t.Fatalf("got %q, %v; want bare-token", got, err)
	}
}

func TestTokenManager_DebugLogNeverLeaksClientSecret(t *testing.T) {
	const secret = "super-secret-client-secret-value"
	var calls []tokenCall
	srv := capturingTokenServer(t, &calls)

	tm := NewTokenManager(srv.Client(), srv.URL, Credentials{ClientID: "svc-1", ClientSecret: secret}, DefaultProactiveRefreshAge, time.Millisecond)
	var captured strings.Builder
	tm.DebugLog = func(_ context.Context, msg string, fields map[string]any) {
		captured.WriteString(msg)
		for k, v := range fields {
			fmt.Fprintf(&captured, " %s=%v", k, v)
		}
	}
	if _, err := tm.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if captured.Len() == 0 || strings.Contains(captured.String(), secret) || strings.Contains(captured.String(), "issued") {
		t.Fatalf("debug log must exist and contain neither the secret nor the access token, got: %s", captured.String())
	}
}
