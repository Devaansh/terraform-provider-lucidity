package client

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// TestAcceptance_ListTenants is the Phase 1 gated smoke test from CLAUDE.md:
// one authenticated GET /external/client/api/v1/tenants against a real
// Lucidity account. Skipped unless TF_ACC=1, per the project's testing
// conventions (sandbox/demo accounts are treated as live — this hits real
// infrastructure, not a mock).
//
// Required env vars:
//   - LUCIDITY_REFRESH_TOKEN: a live refresh token
//   - LUCIDITY_BASE_URL: the API base URL matching the account's deployment
//     (see the Getting Started guide's dashboard-URL -> base-URL table)
func TestAcceptance_ListTenants(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run acceptance tests against a live Lucidity account")
	}

	refreshToken := os.Getenv("LUCIDITY_REFRESH_TOKEN")
	if refreshToken == "" {
		t.Fatal("LUCIDITY_REFRESH_TOKEN must be set for acceptance tests")
	}
	baseURL := os.Getenv("LUCIDITY_BASE_URL")
	if baseURL == "" {
		baseURL = "https://dash-back.lucidity.dev"
	}

	c := NewClient(baseURL, refreshToken)

	var tenants json.RawMessage
	if err := c.Do(context.Background(), http.MethodGet, "/external/client/api/v1/tenants", nil, &tenants); err != nil {
		t.Fatalf("GET /external/client/api/v1/tenants: %v", err)
	}
	if len(tenants) == 0 {
		t.Fatal("expected a non-empty tenants response")
	}
	t.Logf("tenants response: %s", tenants)
}
