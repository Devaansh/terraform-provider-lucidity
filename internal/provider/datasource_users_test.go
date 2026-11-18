package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

func newUsersTestClient(t *testing.T) *client.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		writeMockEnvelope(w, http.StatusOK, true, map[string]any{"accessToken": "mock-access-token"}, "", "")
	})
	mux.HandleFunc("/v1/users", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("role") != "Viewer" {
			t.Errorf("role filter not sent: %v", r.URL.Query())
		}
		writeMockEnvelope(w, http.StatusOK, true, map[string]any{
			"users": []map[string]any{
				{"email": "jane@acme.com", "name": "Jane Doe", "status": "ACTIVE", "isAccountAdmin": false,
					"roleAssignments": []map[string]any{{"cloudProviderAccountId": "123456789012", "displayName": "Acme Prod", "roles": []string{"Viewer"}}}},
				{"email": "admin@acme.com", "name": "Acme Admin", "status": "ACTIVE", "isAccountAdmin": true, "roleAssignments": []any{}},
			},
			"meta": map[string]any{"totalCount": 2, "pagination": map[string]any{"pageSize": 100, "totalPage": 1, "currentPage": 0}},
		}, "", "")
	})
	mux.HandleFunc("/v1/roles", func(w http.ResponseWriter, r *http.Request) {
		writeMockEnvelope(w, http.StatusOK, true, map[string]any{"roles": []map[string]any{
			{"name": "Viewer", "newUserDefault": true, "permissions": []map[string]any{{"resource": "volume", "action": "READ"}, {"resource": "savings", "action": "READ"}}},
		}}, "", "")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client.NewClient(srv.URL, client.Credentials{RefreshToken: "test-token"}, client.WithHTTPClient(srv.Client()))
}

// readDataSource runs Read with the given config attributes set (the rest
// null) and returns the resulting state.
func readDataSource(t *testing.T, d datasource.DataSource, set map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var sr datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &sr)
	objType := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, ty := range objType.AttributeTypes {
		if v, ok := set[name]; ok {
			vals[name] = v
		} else {
			vals[name] = tftypes.NewValue(ty, nil)
		}
	}
	cfg := tfsdk.Config{Raw: tftypes.NewValue(objType, vals), Schema: sr.Schema}
	resp := datasource.ReadResponse{State: tfsdk.State{Raw: tftypes.NewValue(objType, nil), Schema: sr.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: cfg}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %+v", resp.Diagnostics)
	}
	return resp.State
}

func TestUsersDataSource_Read(t *testing.T) {
	st := readDataSource(t, &usersDataSource{client: newUsersTestClient(t)}, map[string]tftypes.Value{"role": strVal("Viewer")})
	var got usersDataSourceModel
	if d := st.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("Get: %+v", d)
	}
	if got.Role.ValueString() != "Viewer" || !got.Email.IsNull() || len(got.Users) != 2 {
		t.Fatalf("got %+v", got)
	}
	jane, admin := got.Users[0], got.Users[1]
	if jane.Email != "jane@acme.com" || jane.RoleAssignments[0].DisplayName != "Acme Prod" || jane.RoleAssignments[0].Roles[0] != "Viewer" {
		t.Fatalf("jane: %+v", jane)
	}
	if !admin.IsAccountAdmin || admin.RoleAssignments == nil || len(admin.RoleAssignments) != 0 {
		t.Fatalf("admin: %+v", admin)
	}
}

func TestRolesDataSource_Read(t *testing.T) {
	st := readDataSource(t, &rolesDataSource{client: newUsersTestClient(t)}, nil)
	var got rolesDataSourceModel
	if d := st.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("Get: %+v", d)
	}
	if len(got.Roles) != 1 || got.Roles[0].Name != "Viewer" || !got.Roles[0].NewUserDefault || len(got.Roles[0].Permissions) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestUserManagement_RegistersDataSources(t *testing.T) {
	resp, err := newTestProviderServer(t).GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, n := range []string{"lucidity_users", "lucidity_roles"} {
		if _, ok := resp.DataSourceSchemas[n]; !ok {
			t.Errorf("data source %s not registered", n)
		}
	}
}
