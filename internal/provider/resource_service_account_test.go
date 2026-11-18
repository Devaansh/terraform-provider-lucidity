package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

// mockSAServer is a stateful stand-in for the Service Account API. Like a
// case-insensitive backend might, it returns role names capitalised.
type mockSAServer struct {
	mu           sync.Mutex
	accounts     map[string]*client.ServiceAccount
	perms        map[string]map[string][]string
	creds        map[string][]*client.ServiceAccountCredential
	next         int
	calls        []string        // "METHOD path body"
	failCreate   int             // status to return after a successful create (0 = none)
	rejectCreate bool            // POST fails with a 500 and creates nothing (e.g. a reserved name)
	forbidden    bool            // every write gets 403, as for a service account token
	mappingOff   map[string]bool // role mapping switched off (PUT with enabled: false)
	tenants      []string        // X-tenant of every call ("" when not sent)
}

func canonicalRole(r string) string {
	if r == "" {
		return r
	}
	return strings.ToUpper(r[:1]) + strings.ToLower(r[1:])
}

func newMockSAServer(t *testing.T) (*client.Client, *mockSAServer) {
	t.Helper()
	m := &mockSAServer{accounts: map[string]*client.ServiceAccount{}, perms: map[string]map[string][]string{}, creds: map[string][]*client.ServiceAccountCredential{}, mappingOff: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "mock-access-token"})
	})
	mux.HandleFunc("/v1/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		body := m.record(r)
		switch r.Method {
		case http.MethodGet:
			list := []client.ServiceAccount{}
			for _, a := range m.accounts {
				list = append(list, *a)
			}
			writeMockEnvelope(w, http.StatusOK, true, list, "", "")
		case http.MethodPost:
			if m.forbidden {
				writeMockEnvelope(w, http.StatusForbidden, false, nil, "FORBIDDEN", "Service accounts cannot manage service accounts")
				return
			}
			var req struct{ Name, Description string }
			_ = json.Unmarshal(body, &req)
			// As documented: a taken name is a 500, not a 4xx.
			for _, a := range m.accounts {
				if strings.EqualFold(a.Name, req.Name) {
					writeMockEnvelope(w, http.StatusInternalServerError, false, nil, "INTERNAL_ERROR", "Internal Server Error")
					return
				}
			}
			if m.rejectCreate {
				writeMockEnvelope(w, http.StatusInternalServerError, false, nil, "INTERNAL_ERROR", "Internal Server Error")
				return
			}
			m.next++
			a := &client.ServiceAccount{ID: fmt.Sprintf("sa-%d", m.next), AccountID: 42, Name: req.Name, Description: req.Description, Enabled: true, CreatedAt: "2026-11-18T10:00:00Z"}
			m.accounts[a.ID] = a
			if m.failCreate != 0 {
				writeMockEnvelope(w, m.failCreate, false, nil, "INTERNAL_ERROR", "boom")
				return
			}
			writeMockEnvelope(w, http.StatusCreated, true, a, "", "")
		}
	})
	mux.HandleFunc("/v1/service-accounts/", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		body := m.record(r)
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/service-accounts/"), "/")
		a, ok := m.accounts[parts[0]]
		if !ok {
			writeMockEnvelope(w, http.StatusNotFound, false, nil, "NOT_FOUND", "Service account not found")
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			writeMockEnvelope(w, http.StatusOK, true, a, "", "")
		case len(parts) == 1 && r.Method == http.MethodPatch:
			var u struct {
				Name        *string
				Description *string
				Enabled     *bool
			}
			_ = json.Unmarshal(body, &u)
			if u.Name != nil {
				for _, o := range m.accounts {
					if o.ID != a.ID && strings.EqualFold(o.Name, *u.Name) {
						writeMockEnvelope(w, http.StatusInternalServerError, false, nil, "INTERNAL_ERROR", "Internal Server Error")
						return
					}
				}
				a.Name = *u.Name
			}
			if u.Description != nil {
				a.Description = *u.Description
			}
			if u.Enabled != nil {
				a.Enabled = *u.Enabled
			}
			writeMockEnvelope(w, http.StatusOK, true, a, "", "")
		case len(parts) == 1 && r.Method == http.MethodDelete:
			delete(m.accounts, a.ID)
			writeMockEnvelope(w, http.StatusOK, true, true, "", "")
		case parts[1] == "permissions" && r.Method == http.MethodGet:
			writeMockEnvelope(w, http.StatusOK, true, map[string]any{"serviceAccountId": a.ID, "tenantRoleMap": m.permsOf(a.ID), "enabled": !m.mappingOff[a.ID]}, "", "")
		case parts[1] == "permissions" && r.Method == http.MethodPut:
			var req struct {
				TenantRoleMap map[string][]string
				Enabled       bool
			}
			_ = json.Unmarshal(body, &req)
			stored := map[string][]string{}
			for t, roles := range req.TenantRoleMap {
				for _, r := range roles {
					stored[t] = append(stored[t], canonicalRole(r))
				}
			}
			m.perms[a.ID] = stored
			m.mappingOff[a.ID] = !req.Enabled
			writeMockEnvelope(w, http.StatusOK, true, map[string]any{"serviceAccountId": a.ID, "tenantRoleMap": stored, "enabled": req.Enabled}, "", "")
		case parts[1] == "tokens" && len(parts) == 2 && r.Method == http.MethodPost:
			if !a.Enabled {
				writeMockEnvelope(w, http.StatusInternalServerError, false, nil, "INTERNAL_ERROR", "Internal Server Error")
				return
			}
			var req struct{ Name, ExpiresAt string }
			_ = json.Unmarshal(body, &req)
			if req.ExpiresAt == "" {
				req.ExpiresAt = "2027-02-16T10:00:00Z"
			}
			m.next++
			id := fmt.Sprintf("tok-%d", m.next)
			secret := "secret-" + id
			// As documented, the client ID differs from the token ID.
			c := &client.ServiceAccountCredential{TokenID: id, Name: req.Name, ClientID: "AKSA-" + id, Active: true, ExpiresAt: req.ExpiresAt, CreatedAt: "2026-11-18T10:00:00Z"}
			m.creds[a.ID] = append(m.creds[a.ID], c)
			echo := *c
			echo.ClientSecret = &secret
			writeMockEnvelope(w, http.StatusCreated, true, echo, "", "")
		case parts[1] == "tokens" && len(parts) == 2 && r.Method == http.MethodGet:
			list := []client.ServiceAccountCredential{}
			for _, c := range m.creds[a.ID] {
				list = append(list, *c)
			}
			writeMockEnvelope(w, http.StatusOK, true, list, "", "")
		case parts[1] == "tokens" && len(parts) == 3 && r.Method == http.MethodDelete:
			for _, c := range m.creds[a.ID] {
				if c.TokenID == parts[2] {
					c.Active = false
					writeMockEnvelope(w, http.StatusOK, true, true, "", "")
					return
				}
			}
			writeMockEnvelope(w, http.StatusNotFound, false, nil, "NOT_FOUND", "credential not found")
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client.NewClient(srv.URL, client.Credentials{RefreshToken: "test-token"}, client.WithHTTPClient(srv.Client())), m
}

func (m *mockSAServer) record(r *http.Request) []byte {
	m.tenants = append(m.tenants, r.Header.Get("X-Tenant"))
	var raw json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&raw)
	m.calls = append(m.calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(raw)))
	return raw
}

func (m *mockSAServer) permsOf(id string) map[string][]string {
	if p := m.perms[id]; p != nil {
		return p
	}
	return map[string][]string{}
}

func (m *mockSAServer) sawCall(prefix string) bool {
	for _, c := range m.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

var saRolesType = tftypes.Set{ElementType: tftypes.String}
var saPermsType = tftypes.Map{ElementType: saRolesType}

func serviceAccountType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "name": tftypes.String, "description": tftypes.String,
		"enabled": tftypes.Bool, "permissions": saPermsType, "created_at": tftypes.String, "tenant_id": tftypes.String,
	}}
}

func permsVal(p map[string][]string) tftypes.Value {
	if p == nil {
		return tftypes.NewValue(saPermsType, nil)
	}
	m := map[string]tftypes.Value{}
	for t, roles := range p {
		vals := []tftypes.Value{}
		for _, r := range roles {
			vals = append(vals, strVal(r))
		}
		m[t] = tftypes.NewValue(saRolesType, vals)
	}
	return tftypes.NewValue(saPermsType, m)
}

func serviceAccountValue(overrides map[string]tftypes.Value) tftypes.Value {
	v := map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), "name": strVal("ci-bot"),
		"description": tftypes.NewValue(tftypes.String, nil), "enabled": tftypes.NewValue(tftypes.Bool, true),
		"permissions": permsVal(nil), "created_at": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"tenant_id": tftypes.NewValue(tftypes.String, nil),
	}
	for k, o := range overrides {
		v[k] = o
	}
	return tftypes.NewValue(serviceAccountType(), v)
}

func readSA(t *testing.T, r *serviceAccountResource, st tfsdk.State) resource.ReadResponse {
	t.Helper()
	var resp resource.ReadResponse
	resp.State = st
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %+v", resp.Diagnostics)
	}
	return resp
}

func TestServiceAccountLifecycle(t *testing.T) {
	c, m := newMockSAServer(t)
	r := &serviceAccountResource{client: c}
	sch := schemaOf(t, r)
	ctx := context.Background()

	// Create disabled, with a description and lower-case role names.
	plan := serviceAccountValue(map[string]tftypes.Value{
		"description": strVal("GitHub Actions"), "enabled": tftypes.NewValue(tftypes.Bool, false),
		"permissions": permsVal(map[string][]string{"tenant-prod": {"viewer", "editor"}}),
	})
	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("Create: %+v", created.Diagnostics)
	}
	var st serviceAccountModel
	created.State.Get(ctx, &st)
	if st.ID.ValueString() != "sa-1" || m.accounts["sa-1"].Enabled || m.accounts["sa-1"].Description != "GitHub Actions" ||
		len(m.perms["sa-1"]["tenant-prod"]) != 2 || !m.sawCall(`PUT /v1/service-accounts/sa-1/permissions {"tenantRoleMap":{"tenant-prod":["editor","viewer"]},"enabled":true}`) {
		t.Fatalf("Create state %+v / server %+v %+v / calls %v", st, m.accounts["sa-1"], m.perms, m.calls)
	}

	// Read: the API answers "Viewer"/"Editor"; the configured spelling stays.
	read := readSA(t, r, created.State)
	var rs serviceAccountModel
	read.State.Get(ctx, &rs)
	if !rs.Permissions.Equal(st.Permissions) {
		t.Fatalf("case-only difference must not show as drift: %v vs %v", rs.Permissions, st.Permissions)
	}

	// Update: rename, re-enable, drop editor. A name check, one PATCH, one PUT.
	m.calls = nil
	newPlan := serviceAccountValue(map[string]tftypes.Value{
		"id": strVal("sa-1"), "name": strVal("ci-deploy-bot"), "description": strVal("GitHub Actions"),
		"created_at": strVal("2026-11-18T10:00:00Z"), "permissions": permsVal(map[string][]string{"tenant-prod": {"viewer"}}),
	})
	var updated resource.UpdateResponse
	updated.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Raw: newPlan, Schema: sch}, State: read.State}, &updated)
	if updated.Diagnostics.HasError() || m.accounts["sa-1"].Name != "ci-deploy-bot" || !m.accounts["sa-1"].Enabled ||
		len(m.perms["sa-1"]["tenant-prod"]) != 1 || len(m.calls) != 3 || m.calls[0] != "GET /v1/service-accounts" ||
		!m.sawCall(`PATCH /v1/service-accounts/sa-1 {"name":"ci-deploy-bot","enabled":true}`) {
		t.Fatalf("Update: %+v / %+v / calls %v", updated.Diagnostics, m.accounts["sa-1"], m.calls)
	}

	// Drift: a role added in the dashboard shows up on refresh.
	m.perms["sa-1"]["tenant-dev"] = []string{"Viewer"}
	drift := readSA(t, r, updated.State)
	var ds serviceAccountModel
	drift.State.Get(ctx, &ds)
	if len(ds.Permissions.Elements()) != 2 {
		t.Fatalf("expected the dashboard-added tenant in state, got %v", ds.Permissions)
	}

	// Removing permissions from config switches the role mapping off (the
	// current roles with enabled: false), not an empty map.
	m.calls = nil
	noPerms := serviceAccountValue(map[string]tftypes.Value{"id": strVal("sa-1"), "name": strVal("ci-deploy-bot"), "description": strVal("GitHub Actions"), "created_at": strVal("2026-11-18T10:00:00Z")})
	var cleared resource.UpdateResponse
	cleared.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Raw: noPerms, Schema: sch}, State: drift.State}, &cleared)
	if cleared.Diagnostics.HasError() || !m.mappingOff["sa-1"] || m.sawCall(`PUT /v1/service-accounts/sa-1/permissions {"tenantRoleMap":{}`) ||
		!m.sawCall(`PUT /v1/service-accounts/sa-1/permissions {"tenantRoleMap":{"tenant-dev":["Viewer"],"tenant-prod":["viewer"]},"enabled":false}`) {
		t.Fatalf("clear permissions: %+v / %v / calls %v", cleared.Diagnostics, m.perms, m.calls)
	}
	again := readSA(t, r, cleared.State)
	var as serviceAccountModel
	again.State.Get(ctx, &as)
	if !as.Permissions.IsNull() || len(again.Diagnostics.Warnings()) != 0 {
		t.Fatalf("a mapping the provider switched off must read back as null, without a warning; got %v / %+v", as.Permissions, again.Diagnostics)
	}

	// Delete, then Read removes it from state.
	var del resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: cleared.State}, &del)
	if del.Diagnostics.HasError() || len(m.accounts) != 0 {
		t.Fatalf("Delete: %+v", del.Diagnostics)
	}
	gone := readSA(t, r, cleared.State)
	if !gone.State.Raw.IsNull() || len(gone.Diagnostics.Warnings()) != 1 || !strings.Contains(gone.Diagnostics.Warnings()[0].Detail(), "can't be reused") {
		t.Fatalf("expected removal with a name-reuse warning, got %+v", gone.Diagnostics)
	}
}

func createSA(t *testing.T, r *serviceAccountResource, plan tftypes.Value) resource.CreateResponse {
	t.Helper()
	sch := schemaOf(t, r)
	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: sch}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}}, &resp)
	return resp
}

func TestServiceAccountCreate_NameTakenPointsAtImport(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-9"] = &client.ServiceAccount{ID: "sa-9", Name: "CI-Bot", Enabled: true}
	resp := createSA(t, &serviceAccountResource{client: c}, serviceAccountValue(nil))
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "import lucidity_service_account.<name> sa-9") || m.sawCall("POST") {
		t.Fatalf("got %+v, calls %v", resp.Diagnostics, m.calls)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("an existing service account must never be adopted on a name clash")
	}
}

// A 500 that created nothing (e.g. the name belongs to a deleted service
// account) explains the documented causes instead of adopting anything.
func TestServiceAccountCreate_500NothingCreated(t *testing.T) {
	c, m := newMockSAServer(t)
	m.rejectCreate = true
	resp := createSA(t, &serviceAccountResource{client: c}, serviceAccountValue(nil))
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "names stay reserved") {
		t.Fatalf("got %+v", resp.Diagnostics)
	}
}

func TestServiceAccountUpdate_RenameToTakenNameFailsBeforePatch(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: true, CreatedAt: "2026-11-18T10:00:00Z"}
	m.accounts["sa-2"] = &client.ServiceAccount{ID: "sa-2", Name: "Deploy-Bot", Enabled: true}
	r := &serviceAccountResource{client: c}
	sch := schemaOf(t, r)
	state := serviceAccountValue(map[string]tftypes.Value{"id": strVal("sa-1"), "created_at": strVal("2026-11-18T10:00:00Z")})
	plan := serviceAccountValue(map[string]tftypes.Value{"id": strVal("sa-1"), "name": strVal("deploy-bot"), "created_at": strVal("2026-11-18T10:00:00Z")})
	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: sch}
	r.Update(context.Background(), resource.UpdateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}, State: tfsdk.State{Raw: state, Schema: sch}}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "sa-2 already has that name") || m.sawCall("PATCH") {
		t.Fatalf("got %+v, calls %v", resp.Diagnostics, m.calls)
	}
}

// A role mapping switched off in Lucidity grants nothing: Read shows no
// roles (with a warning), so a configuration with roles plans a PUT that
// switches it back on.
func TestServiceAccountRead_MappingSwitchedOff(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: true}
	m.perms["sa-1"] = map[string][]string{"t1": {"Viewer"}}
	m.mappingOff["sa-1"] = true
	r := &serviceAccountResource{client: c}
	sch := schemaOf(t, r)
	st := tfsdk.State{Schema: sch, Raw: serviceAccountValue(map[string]tftypes.Value{"id": strVal("sa-1"), "created_at": strVal("x"), "permissions": permsVal(map[string][]string{"t1": {"Viewer"}})})}
	read := readSA(t, r, st)
	var rs serviceAccountModel
	read.State.Get(context.Background(), &rs)
	if !rs.Permissions.IsNull() || len(read.Diagnostics.Warnings()) != 1 {
		t.Fatalf("got permissions %v, diags %+v", rs.Permissions, read.Diagnostics)
	}
}

// A 5xx after the account was actually created: adopted, not left orphaned
// (its name could never be reused).
func TestServiceAccountCreate_5xxButCreatedIsAdopted(t *testing.T) {
	c, m := newMockSAServer(t)
	m.failCreate = http.StatusInternalServerError
	resp := createSA(t, &serviceAccountResource{client: c}, serviceAccountValue(map[string]tftypes.Value{"permissions": permsVal(map[string][]string{"t1": {"Viewer"}})}))
	var st serviceAccountModel
	resp.State.Get(context.Background(), &st)
	if resp.Diagnostics.HasError() || len(resp.Diagnostics.Warnings()) != 1 || st.ID.ValueString() != "sa-1" || len(m.perms["sa-1"]) != 1 {
		t.Fatalf("got %+v, state %+v, perms %v", resp.Diagnostics, st, m.perms)
	}
}

func TestServiceAccountCreate_403ExplainsAdminToken(t *testing.T) {
	c, m := newMockSAServer(t)
	m.forbidden = true
	resp := createSA(t, &serviceAccountResource{client: c}, serviceAccountValue(nil))
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "admin user's refresh token") {
		t.Fatalf("got %+v", resp.Diagnostics)
	}
}

func credentialType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "service_account_id": tftypes.String, "name": tftypes.String, "expires_at": tftypes.String,
		"token_id": tftypes.String, "client_id": tftypes.String, "client_secret": tftypes.String, "created_at": tftypes.String, "tenant_id": tftypes.String,
	}}
}

func credentialValue(overrides map[string]tftypes.Value) tftypes.Value {
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	v := map[string]tftypes.Value{
		"id": unknown, "service_account_id": strVal("sa-1"), "name": strVal("ci-key"), "expires_at": unknown,
		"token_id": unknown, "client_id": unknown, "client_secret": unknown, "created_at": unknown,
		"tenant_id": tftypes.NewValue(tftypes.String, nil),
	}
	for k, o := range overrides {
		v[k] = o
	}
	return tftypes.NewValue(credentialType(), v)
}

func TestServiceAccountCredentialLifecycle(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: true}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	r := &serviceAccountCredentialResource{client: c, now: func() time.Time { return now }}
	sch := schemaOf(t, r)
	ctx := context.Background()

	// Create without expires_at: the API's default comes back into state,
	// along with the one-time secret.
	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: credentialValue(nil), Schema: sch}}, &created)
	var st serviceAccountCredentialModel
	created.State.Get(ctx, &st)
	if created.Diagnostics.HasError() || st.ID.ValueString() != "sa-1/tok-1" || st.ClientSecret.ValueString() != "secret-tok-1" ||
		st.ClientID.ValueString() != "AKSA-tok-1" || st.TokenID.ValueString() != "tok-1" || st.ExpiresAt.ValueString() != "2027-02-16T10:00:00Z" {
		t.Fatalf("Create: %+v / %+v", created.Diagnostics, st)
	}

	// Read keeps it, including the secret the API no longer returns.
	var read resource.ReadResponse
	read.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	var rs serviceAccountCredentialModel
	read.State.Get(ctx, &rs)
	if read.Diagnostics.HasError() || rs.ClientSecret.ValueString() != "secret-tok-1" {
		t.Fatalf("Read: %+v / %+v", read.Diagnostics, rs)
	}

	// Past its expiry: dropped from state so the next apply makes a new one.
	now = time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	var expired resource.ReadResponse
	expired.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &expired)
	if !expired.State.Raw.IsNull() || !strings.Contains(expired.Diagnostics.Warnings()[0].Detail(), "expired at") {
		t.Fatalf("expected removal on expiry, got %+v", expired.Diagnostics)
	}

	// Revoked outside Terraform: also dropped.
	now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	m.creds["sa-1"][0].Active = false
	var revoked resource.ReadResponse
	revoked.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &revoked)
	if !revoked.State.Raw.IsNull() || !strings.Contains(revoked.Diagnostics.Warnings()[0].Detail(), "no longer active") {
		t.Fatalf("expected removal when revoked, got %+v", revoked.Diagnostics)
	}

	// Delete revokes.
	m.creds["sa-1"][0].Active = true
	var del resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &del)
	if del.Diagnostics.HasError() || m.creds["sa-1"][0].Active {
		t.Fatalf("Delete: %+v", del.Diagnostics)
	}
}

// The API answers a disabled service account and a past expiry with a 500,
// which would read as "may have been created": both are checked first.
func TestServiceAccountCredentialCreate_PreChecks(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: false}
	r := &serviceAccountCredentialResource{client: c, now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }}
	sch := schemaOf(t, r)
	ctx := context.Background()

	var disabled resource.CreateResponse
	disabled.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: credentialValue(nil), Schema: sch}}, &disabled)
	if !disabled.Diagnostics.HasError() || !strings.Contains(disabled.Diagnostics.Errors()[0].Detail(), "is disabled") || m.sawCall("POST") {
		t.Fatalf("disabled: %+v, calls %v", disabled.Diagnostics, m.calls)
	}

	m.accounts["sa-1"].Enabled = true
	var past resource.CreateResponse
	past.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: credentialValue(map[string]tftypes.Value{"expires_at": strVal("2026-01-01T00:00:00Z")}), Schema: sch}}, &past)
	if !past.Diagnostics.HasError() || !strings.Contains(past.Diagnostics.Errors()[0].Summary(), "in the past") || m.sawCall("POST") {
		t.Fatalf("past expiry: %+v, calls %v", past.Diagnostics, m.calls)
	}
}

func TestServiceAccountCredential_ExplicitExpiryKeepsConfiguredSpelling(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: true}
	r := &serviceAccountCredentialResource{client: c, now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }}
	sch := schemaOf(t, r)
	ctx := context.Background()

	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: credentialValue(map[string]tftypes.Value{"expires_at": strVal("2027-01-04T05:30:00+05:30")}), Schema: sch}}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("Create: %+v", created.Diagnostics)
	}
	m.creds["sa-1"][0].ExpiresAt = "2027-01-04T00:00:00Z" // same instant, API's format
	var read resource.ReadResponse
	read.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	var rs serviceAccountCredentialModel
	read.State.Get(ctx, &rs)
	if rs.ExpiresAt.ValueString() != "2027-01-04T05:30:00+05:30" {
		t.Fatalf("same instant must keep the configured spelling (else it forces a replace), got %q", rs.ExpiresAt.ValueString())
	}
}

func TestServiceAccountCredential_Import(t *testing.T) {
	c, m := newMockSAServer(t)
	m.accounts["sa-1"] = &client.ServiceAccount{ID: "sa-1", Name: "ci-bot", Enabled: true}
	m.creds["sa-1"] = []*client.ServiceAccountCredential{{TokenID: "tok-7", Name: "legacy-key", ClientID: "AKSA7Q2M9X4T1LBC", Active: true, ExpiresAt: "2027-01-04T00:00:00Z"}}
	r := &serviceAccountCredentialResource{client: c, now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }}
	sch := schemaOf(t, r)
	ctx := context.Background()

	var bad resource.ImportStateResponse
	bad.State = tfsdk.State{Schema: sch, Raw: tftypes.NewValue(credentialType(), nil)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "tok-7"}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Fatal("an import ID without the service account must be rejected")
	}

	var imp resource.ImportStateResponse
	imp.State = tfsdk.State{Schema: sch, Raw: tftypes.NewValue(credentialType(), nil)}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "sa-1/tok-7"}, &imp)
	if imp.Diagnostics.HasError() || len(imp.Diagnostics.Warnings()) != 1 {
		t.Fatalf("ImportState: %+v", imp.Diagnostics)
	}
	var read resource.ReadResponse
	read.State = imp.State
	r.Read(ctx, resource.ReadRequest{State: imp.State}, &read)
	var rs serviceAccountCredentialModel
	read.State.Get(ctx, &rs)
	if read.Diagnostics.HasError() || rs.Name.ValueString() != "legacy-key" || !rs.ClientSecret.IsNull() || rs.ExpiresAt.ValueString() != "2027-01-04T00:00:00Z" {
		t.Fatalf("Read after import: %+v / %+v", read.Diagnostics, rs)
	}
}

// saConfig and credentialConfig are configurations as Terraform sends them
// to validation: computed-only attributes are null, not unknown.
func saConfig(overrides map[string]tftypes.Value) tftypes.Value {
	null := tftypes.NewValue(tftypes.String, nil)
	o := map[string]tftypes.Value{"id": null, "created_at": null, "enabled": tftypes.NewValue(tftypes.Bool, nil)}
	for k, v := range overrides {
		o[k] = v
	}
	return serviceAccountValue(o)
}

func credentialConfig(overrides map[string]tftypes.Value) tftypes.Value {
	null := tftypes.NewValue(tftypes.String, nil)
	o := map[string]tftypes.Value{"id": null, "expires_at": null, "token_id": null, "client_id": null, "client_secret": null, "created_at": null}
	for k, v := range overrides {
		o[k] = v
	}
	return credentialValue(o)
}

func TestServiceAccountRPC_ValidationAndRegistration(t *testing.T) {
	if d := validateResource(t, "lucidity_service_account", serviceAccountType(), saConfig(map[string]tftypes.Value{"permissions": permsVal(map[string][]string{"t1": {"Viewer"}})})); hasErrorDiagnostic(d) {
		t.Fatalf("valid service account rejected: %+v", d)
	}
	if d := validateResource(t, "lucidity_service_account", serviceAccountType(), saConfig(map[string]tftypes.Value{"permissions": permsVal(map[string][]string{"t1": {}})})); !hasErrorDiagnostic(d) {
		t.Fatal("a tenant with no roles must be rejected (the API needs at least one)")
	}
	if d := validateResource(t, "lucidity_service_account", serviceAccountType(), saConfig(map[string]tftypes.Value{"permissions": permsVal(map[string][]string{})})); !hasErrorDiagnostic(d) {
		t.Fatal("an empty permissions map must be rejected (leave it unset instead)")
	}
	if d := validateResource(t, "lucidity_service_account_credential", credentialType(), credentialConfig(map[string]tftypes.Value{"expires_at": strVal("2027-01-04")})); !hasErrorDiagnostic(d) {
		t.Fatal("a non-RFC 3339 expires_at must be rejected")
	}
	if d := validateResource(t, "lucidity_service_account_credential", credentialType(), credentialConfig(map[string]tftypes.Value{"expires_at": strVal("2027-01-04T00:00:00Z")})); hasErrorDiagnostic(d) {
		t.Fatalf("valid expires_at rejected: %+v", d)
	}

	resp, err := newTestProviderServer(t).GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"lucidity_service_account", "lucidity_service_account_credential"} {
		if _, ok := resp.ResourceSchemas[n]; !ok {
			t.Errorf("resource %s not registered", n)
		}
	}
	if _, ok := resp.DataSourceSchemas["lucidity_service_accounts"]; !ok {
		t.Error("data source lucidity_service_accounts not registered")
	}
	if !hasSensitiveSecret(resp.ResourceSchemas["lucidity_service_account_credential"].Block.Attributes) {
		t.Error("client_secret must be sensitive")
	}
}

func hasSensitiveSecret(attrs []*tfprotov6.SchemaAttribute) bool {
	for _, a := range attrs {
		if a.Name == "client_secret" {
			return a.Sensitive
		}
	}
	return false
}

// A resource's tenant_id reaches every call as X-tenant; changing only
// tenant_id is a no-op apply (no API write, no replacement).
func TestServiceAccount_TenantIDSentAsHeader(t *testing.T) {
	c, m := newMockSAServer(t)
	r := &serviceAccountResource{client: c}
	sch := schemaOf(t, r)
	ctx := context.Background()

	resp := createSA(t, r, serviceAccountValue(map[string]tftypes.Value{
		"tenant_id": strVal("lucidity-perf"), "permissions": permsVal(map[string][]string{"lucidity-perf": {"Viewer"}}),
	}))
	if resp.Diagnostics.HasError() || len(m.tenants) == 0 {
		t.Fatalf("Create: %+v", resp.Diagnostics)
	}
	for i, tnt := range m.tenants {
		if tnt != "lucidity-perf" {
			t.Fatalf("call %d (%s) sent X-tenant %q, want lucidity-perf", i, m.calls[i], tnt)
		}
	}

	m.calls, m.tenants = nil, nil
	var st serviceAccountModel
	resp.State.Get(ctx, &st)
	plan := serviceAccountValue(map[string]tftypes.Value{
		"id": strVal(st.ID.ValueString()), "created_at": strVal(st.CreatedAt.ValueString()),
		"tenant_id": strVal("lucidity-e2e"), "permissions": permsVal(map[string][]string{"lucidity-perf": {"Viewer"}}),
	})
	var upd resource.UpdateResponse
	upd.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}, State: resp.State}, &upd)
	var us serviceAccountModel
	upd.State.Get(ctx, &us)
	if upd.Diagnostics.HasError() || len(m.calls) != 0 || us.TenantID.ValueString() != "lucidity-e2e" {
		t.Fatalf("tenant_id-only change must not call the API: %+v, calls %v, state tenant %q", upd.Diagnostics, m.calls, us.TenantID.ValueString())
	}

	read := readSA(t, r, upd.State)
	if read.Diagnostics.HasError() || m.tenants[len(m.tenants)-1] != "lucidity-e2e" {
		t.Fatalf("Read must use the new tenant_id, got %v", m.tenants)
	}
}
