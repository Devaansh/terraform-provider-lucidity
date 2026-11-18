package client

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// TestAcceptance_ListTenants is the Phase 1 gated smoke test: one
// authenticated GET /v1/tenants against a real
// Lucidity account. Skipped unless TF_ACC=1, per the project's testing
// conventions (sandbox/demo accounts are treated as live — this hits real
// infrastructure, not a mock).
//
// Required env vars:
//   - LUCIDITY_REFRESH_TOKEN: a live refresh token, OR
//     LUCIDITY_CLIENT_ID + LUCIDITY_CLIENT_SECRET: a service account credential
//   - LUCIDITY_BASE_URL: the API base URL matching the account's deployment
//     (see the Getting Started guide's dashboard-URL -> base-URL table)
func TestAcceptance_ListTenants(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run acceptance tests against a live Lucidity account")
	}

	creds := Credentials{
		RefreshToken: os.Getenv("LUCIDITY_REFRESH_TOKEN"),
		ClientID:     os.Getenv("LUCIDITY_CLIENT_ID"),
		ClientSecret: os.Getenv("LUCIDITY_CLIENT_SECRET"),
	}
	if creds.IsServiceAccount() {
		creds.RefreshToken = ""
	}
	if creds.RefreshToken == "" && (creds.ClientID == "" || creds.ClientSecret == "") {
		t.Fatal("set LUCIDITY_REFRESH_TOKEN, or LUCIDITY_CLIENT_ID and LUCIDITY_CLIENT_SECRET, for acceptance tests")
	}
	baseURL := os.Getenv("LUCIDITY_BASE_URL")
	if baseURL == "" {
		baseURL = "https://dash-back.lucidity.dev"
	}

	c := NewClient(baseURL, creds)

	var tenants json.RawMessage
	if err := c.Do(context.Background(), http.MethodGet, "/v1/tenants", nil, &tenants); err != nil {
		t.Fatalf("GET /v1/tenants: %v", err)
	}
	if len(tenants) == 0 {
		t.Fatal("expected a non-empty tenants response")
	}
	t.Logf("tenants response: %s", tenants)
}
