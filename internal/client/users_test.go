package client

import (
	"context"
	"net/http"
	"testing"
)

// ListUsers sends the filters, asks for the largest page and follows every
// page until totalPage.
func TestListUsers_FiltersAndPages(t *testing.T) {
	var pages []string
	mux := http.NewServeMux()
	mux.HandleFunc(usersPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("email") != "acme.com" || q.Get("status") != "ACTIVE" || q.Get("pageSize") != "100" || q.Has("role") {
			t.Fatalf("got query %v", q)
		}
		pages = append(pages, q.Get("page"))
		email := map[string]string{"0": "jane@acme.com", "1": "admin@acme.com"}[q.Get("page")]
		writeEnvelope(w, http.StatusOK, true, map[string]any{
			"users": []map[string]any{{
				"email": email, "name": "n", "status": "ACTIVE", "isAccountAdmin": email == "admin@acme.com",
				"roleAssignments": []map[string]any{{"cloudProviderAccountId": "123456789012", "displayName": "Acme Prod", "roles": []string{"Viewer"}}},
			}},
			"meta": map[string]any{"totalCount": 2, "pagination": map[string]any{"pageSize": 100, "totalPage": 2, "currentPage": len(pages) - 1}},
		}, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)

	got, err := c.ListUsers(context.Background(), UserFilter{Email: "acme.com", Status: "ACTIVE"})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(pages) != 2 || len(got) != 2 || got[0].Email != "jane@acme.com" || !got[1].IsAccountAdmin ||
		got[0].RoleAssignments[0].CloudProviderAccountID != "123456789012" || got[0].RoleAssignments[0].Roles[0] != "Viewer" {
		t.Fatalf("got pages %v users %+v", pages, got)
	}
}

// An empty page ends the loop even if totalPage claims more.
func TestListUsers_StopsOnEmptyPage(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc(usersPath, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeEnvelope(w, http.StatusOK, true, map[string]any{"users": []any{}, "meta": map[string]any{"pagination": map[string]any{"totalPage": 5}}}, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	got, err := c.ListUsers(context.Background(), UserFilter{})
	if err != nil || len(got) != 0 || calls != 1 {
		t.Fatalf("got %v %v after %d calls", got, err, calls)
	}
}

func TestListRoles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(rolesPath, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, http.StatusOK, true, map[string]any{"roles": []map[string]any{
			{"name": "Viewer", "newUserDefault": true, "permissions": []map[string]any{{"resource": "volume", "action": "READ"}}},
			{"name": "Editor", "newUserDefault": false, "permissions": []map[string]any{{"resource": "volume", "action": "WRITE"}}},
		}}, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	got, err := c.ListRoles(context.Background())
	if err != nil || len(got) != 2 || got[0].Name != "Viewer" || !got[0].NewUserDefault || got[1].Permissions[0].Action != "WRITE" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
