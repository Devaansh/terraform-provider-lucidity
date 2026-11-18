package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestServiceAccountCalls_PathsMethodsAndBodies(t *testing.T) {
	type call struct{ method, path, body string }
	var calls []call
	sa := map[string]any{"id": "sa-1", "accountId": 42, "name": "ci-bot", "description": "d", "enabled": true, "createdAt": "2026-10-06T10:42:17Z"}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, string(b)})
	}
	mux.HandleFunc("/v1/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method == http.MethodPost {
			writeEnvelope(w, http.StatusCreated, true, sa, nil, "r")
			return
		}
		writeEnvelope(w, http.StatusOK, true, []any{sa}, nil, "r")
	})
	mux.HandleFunc("/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method == http.MethodDelete {
			writeEnvelope(w, http.StatusOK, true, true, nil, "r")
			return
		}
		writeEnvelope(w, http.StatusOK, true, sa, nil, "r")
	})
	mux.HandleFunc("/v1/service-accounts/sa-1/permissions", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeEnvelope(w, http.StatusOK, true, map[string]any{"serviceAccountId": "sa-1", "tenantRoleMap": map[string][]string{"t1": {"Viewer"}}, "enabled": true}, nil, "r")
	})
	mux.HandleFunc("/v1/service-accounts/sa-1/tokens", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		cred := map[string]any{"tokenId": "tok-1", "name": "key", "clientId": "tok-1", "clientSecret": "s3cret", "active": true, "expiresAt": "2027-01-04T00:00:00Z"}
		if r.Method == http.MethodPost {
			writeEnvelope(w, http.StatusCreated, true, cred, nil, "r")
			return
		}
		cred["clientSecret"] = nil
		writeEnvelope(w, http.StatusOK, true, []any{cred}, nil, "r")
	})
	mux.HandleFunc("/v1/service-accounts/sa-1/tokens/tok-1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeEnvelope(w, http.StatusOK, true, true, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	ctx := context.Background()

	created, err := c.CreateServiceAccount(ctx, "ci-bot", "")
	if err != nil || created.ID != "sa-1" || created.AccountID != 42 {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err := c.ListServiceAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetServiceAccount(ctx, "sa-1"); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := c.UpdateServiceAccount(ctx, "sa-1", ServiceAccountUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	perms, err := c.ReplaceServiceAccountPermissions(ctx, "sa-1", nil)
	if err != nil || perms.TenantRoleMap["t1"][0] != "Viewer" {
		t.Fatalf("permissions: %+v %v", perms, err)
	}
	cred, err := c.CreateServiceAccountCredential(ctx, "sa-1", "key", "")
	if err != nil || cred.ClientSecret == nil || *cred.ClientSecret != "s3cret" {
		t.Fatalf("credential: %+v %v", cred, err)
	}
	list, err := c.ListServiceAccountCredentials(ctx, "sa-1")
	if err != nil || list[0].ClientSecret != nil {
		t.Fatalf("list credentials must not carry a secret: %+v %v", list, err)
	}
	if err := c.RevokeServiceAccountCredential(ctx, "sa-1", "tok-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteServiceAccount(ctx, "sa-1"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{"POST", "/v1/service-accounts", `{"name":"ci-bot"}`},
		{"GET", "/v1/service-accounts", ""},
		{"GET", "/v1/service-accounts/sa-1", ""},
		{"PATCH", "/v1/service-accounts/sa-1", `{"enabled":false}`},
		{"PUT", "/v1/service-accounts/sa-1/permissions", `{"tenantRoleMap":{},"enabled":true}`},
		{"POST", "/v1/service-accounts/sa-1/tokens", `{"name":"key"}`},
		{"GET", "/v1/service-accounts/sa-1/tokens", ""},
		{"DELETE", "/v1/service-accounts/sa-1/tokens/tok-1", ""},
		{"DELETE", "/v1/service-accounts/sa-1", ""},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if calls[i].method != w.method || calls[i].path != w.path || !jsonEqual(calls[i].body, w.body) {
			t.Errorf("call %d: got %+v, want %+v", i, calls[i], w)
		}
	}
}

// The create calls must not be retried on a 5xx: after a create that did
// succeed, a retry would fail on the (never reusable) name, or make a second
// credential and lose the first one's secret.
func TestServiceAccountCreates_NoRetryOn5xx(t *testing.T) {
	posts := 0
	mux := http.NewServeMux()
	fail := func(w http.ResponseWriter, r *http.Request) {
		posts++
		writeEnvelope(w, http.StatusInternalServerError, false, nil, &envelopeError{Code: "INTERNAL_ERROR", Message: "boom"}, "r")
	}
	mux.HandleFunc("/v1/service-accounts", fail)
	mux.HandleFunc("/v1/service-accounts/sa-1/tokens", fail)
	c, _ := newTenantTestClient(t, mux)
	ctx := context.Background()

	if _, err := c.CreateServiceAccount(ctx, "x", ""); err == nil || !IsOutcomeUnknown(ctx, err) {
		t.Fatalf("want an outcome-unknown error, got %v", err)
	}
	if _, err := c.CreateServiceAccountCredential(ctx, "sa-1", "k", ""); err == nil || !IsOutcomeUnknown(ctx, err) {
		t.Fatalf("want an outcome-unknown error, got %v", err)
	}
	if posts != 2 {
		t.Fatalf("got %d POSTs, want exactly 2 (no retries)", posts)
	}
}

func TestFindServiceAccountByName_IgnoresCase(t *testing.T) {
	got, ok := FindServiceAccountByName([]ServiceAccount{{ID: "a", Name: "CI-Bot"}}, "ci-bot")
	if !ok || got.ID != "a" {
		t.Fatalf("got %+v %v", got, ok)
	}
}

func jsonEqual(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
