package client

import (
	"context"
	"net/http"
	"testing"
)

// X-tenant: the call's own tenant wins over the client default; with
// neither, the header isn't sent at all (so nothing changes for configs
// that don't set tenant_id).
func TestXTenantHeader(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		v, present := r.Header["X-Tenant"]
		if !present {
			got = append(got, "<absent>")
		} else {
			got = append(got, v[0])
		}
		writeEnvelope(w, http.StatusOK, true, []any{}, nil, "r")
	})
	c, srv := newTenantTestClient(t, mux)
	ctx := context.Background()

	if _, err := c.ListServiceAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListServiceAccounts(WithTenant(ctx, "lucidity-perf")); err != nil {
		t.Fatal(err)
	}
	withDefault := NewClient(srv.URL, Credentials{RefreshToken: "r"}, WithHTTPClient(srv.Client()), WithDefaultTenant("lucidity-e2e"))
	if _, err := withDefault.ListServiceAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := withDefault.ListServiceAccounts(WithTenant(ctx, "lucidity-perf")); err != nil {
		t.Fatal(err)
	}
	if _, err := withDefault.ListServiceAccounts(WithTenant(ctx, "")); err != nil {
		t.Fatal(err)
	}

	want := []string{"<absent>", "lucidity-perf", "lucidity-e2e", "lucidity-perf", "lucidity-e2e"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
