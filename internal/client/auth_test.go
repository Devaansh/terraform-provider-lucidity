package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func refreshServer(t *testing.T, accessToken func(call int) string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != refreshEndpoint {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(refreshResponseBody{AccessToken: accessToken(int(n))})
	}))
	return srv, &calls
}

func TestTokenManager_RefreshHappyPath(t *testing.T) {
	srv, _ := refreshServer(t, func(int) string { return "token-1" })
	defer srv.Close()

	tm := NewTokenManager(srv.Client(), srv.URL, "refresh-secret")
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

	tm := NewTokenManager(srv.Client(), srv.URL, "refresh-secret")

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

	// Backdate issuedAt past the 12-minute proactive-renewal mark.
	tm.mu.Lock()
	tm.issuedAt = time.Now().Add(-proactiveRefreshAge - time.Second)
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
		_ = json.NewEncoder(w).Encode(refreshResponseBody{AccessToken: "shared-token"})
	}))
	defer srv.Close()

	tm := NewTokenManager(srv.Client(), srv.URL, "refresh-secret")

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

	tm := NewTokenManager(srv.Client(), srv.URL, "expired-secret")
	_, err := tm.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != AuthFailedMessage {
		t.Fatalf("got %q, want the exact required auth-failed message", err.Error())
	}
}
