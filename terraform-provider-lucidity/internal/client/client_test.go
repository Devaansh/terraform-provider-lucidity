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

const protectedPath = "/external/client/api/v1/tenants"

type protectedResult struct {
	Foo string `json:"foo"`
}

func newMockServer(t *testing.T, refreshTokenCalls *int32, protectedHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(refreshEndpoint, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(refreshTokenCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(refreshResponseBody{AccessToken: fmt.Sprintf("access-token-%d", n)})
	})
	mux.HandleFunc(protectedPath, protectedHandler)
	return httptest.NewServer(mux)
}

func TestClient_401RetrySucceedsAfterForcedRefresh(t *testing.T) {
	var refreshCalls, protectedCalls int32
	srv := newMockServer(t, &refreshCalls, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&protectedCalls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope{
			Success:   true,
			Data:      json.RawMessage(`{"foo":"bar"}`),
			RequestID: "req-123",
		})
	})
	defer srv.Close()

	c := NewClient(srv.URL, "refresh-secret", WithHTTPClient(srv.Client()), withRetryBaseDelay(time.Millisecond))

	var out protectedResult
	if err := c.Do(context.Background(), http.MethodGet, protectedPath, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Foo != "bar" {
		t.Fatalf("got %+v, want Foo=bar", out)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 2 {
		t.Fatalf("expected 2 refresh calls (initial + forced), got %d", got)
	}
	if got := atomic.LoadInt32(&protectedCalls); got != 2 {
		t.Fatalf("expected 2 protected-endpoint calls (401 then success), got %d", got)
	}
}

func TestClient_401RetryStillFails(t *testing.T) {
	var refreshCalls int32
	srv := newMockServer(t, &refreshCalls, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer srv.Close()

	c := NewClient(srv.URL, "refresh-secret", WithHTTPClient(srv.Client()), withRetryBaseDelay(time.Millisecond))

	err := c.Do(context.Background(), http.MethodGet, protectedPath, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != AuthFailedMessage {
		t.Fatalf("got %q, want the exact required auth-failed message", err.Error())
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 2 {
		t.Fatalf("expected exactly 2 refresh calls (initial + one forced retry, no more), got %d", got)
	}
}

func TestClient_RetriesOn5xxNotOn4xx(t *testing.T) {
	var refreshCalls, protectedCalls int32
	srv := newMockServer(t, &refreshCalls, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&protectedCalls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope{Success: true, Data: json.RawMessage(`{"foo":"bar"}`)})
	})
	defer srv.Close()

	c := NewClient(srv.URL, "refresh-secret", WithHTTPClient(srv.Client()), withRetryBaseDelay(time.Millisecond))
	var out protectedResult
	if err := c.Do(context.Background(), http.MethodGet, protectedPath, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Foo != "bar" {
		t.Fatalf("got %+v, want Foo=bar", out)
	}

	// A plain 400 must never be retried.
	protectedCalls = 0
	srv2 := newMockServer(t, &refreshCalls, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&protectedCalls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(envelope{Success: false, Error: &envelopeError{Code: "INVALID_REQUEST", Message: "bad field"}})
	})
	defer srv2.Close()
	c2 := NewClient(srv2.URL, "refresh-secret", WithHTTPClient(srv2.Client()), withRetryBaseDelay(time.Millisecond))
	err := c2.Do(context.Background(), http.MethodGet, protectedPath, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got %T, want *APIError", err)
	}
	if apiErr.Code != "INVALID_REQUEST" {
		t.Fatalf("got code %q, want INVALID_REQUEST", apiErr.Code)
	}
	if got := atomic.LoadInt32(&protectedCalls); got != 1 {
		t.Fatalf("expected exactly 1 call for a 4xx (no retry), got %d", got)
	}
}

// TestNoSecretLeaksInDebugLog guards the "never logged, including
// TF_LOG=DEBUG" requirement: it captures everything passed to DebugLog
// across a full refresh + API-call cycle and asserts neither the refresh
// token nor the issued access token ever appears in it.
func TestNoSecretLeaksInDebugLog(t *testing.T) {
	const refreshSecret = "super-secret-refresh-token-value"
	var refreshCalls int32
	srv := newMockServer(t, &refreshCalls, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope{Success: true, Data: json.RawMessage(`{"foo":"bar"}`), RequestID: "req-999"})
	})
	defer srv.Close()

	c := NewClient(srv.URL, refreshSecret, WithHTTPClient(srv.Client()), withRetryBaseDelay(time.Millisecond))

	var captured strings.Builder
	c.DebugLog = func(_ context.Context, msg string, fields map[string]any) {
		captured.WriteString(msg)
		for k, v := range fields {
			fmt.Fprintf(&captured, " %s=%v", k, v)
		}
		captured.WriteString("\n")
	}

	var out protectedResult
	if err := c.Do(context.Background(), http.MethodGet, protectedPath, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}

	logOutput := captured.String()
	if logOutput == "" {
		t.Fatal("expected DebugLog to be called at least once")
	}
	if strings.Contains(logOutput, refreshSecret) {
		t.Fatalf("refresh token leaked into debug log: %s", logOutput)
	}
	if !strings.Contains(logOutput, "requestId=req-999") {
		t.Fatalf("expected requestId to be logged for support escalation, got: %s", logOutput)
	}

	// The issued access token must not leak either, whatever its value was.
	c.tokens.mu.Lock()
	issuedAccessToken := c.tokens.accessToken
	c.tokens.mu.Unlock()
	if issuedAccessToken == "" {
		t.Fatal("expected an access token to have been issued")
	}
	if strings.Contains(logOutput, issuedAccessToken) {
		t.Fatalf("access token leaked into debug log: %s", logOutput)
	}
}
