package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// User Management API (revamped). Only list/invite users and list roles
// exist today; GET/PATCH/DELETE /v1/users/{userId} are on hold until users
// get a stable ID (a user is identified by email for now).
const (
	usersPath = "/v1/users"
	rolesPath = "/v1/roles"

	// usersPageSize is the largest page the API allows (1-100).
	usersPageSize = 100
)

// RoleAssignment is the roles a user holds on one cloud account.
type RoleAssignment struct {
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	// DisplayName is the account's dashboard name; returned for
	// readability only.
	DisplayName string   `json:"displayName"`
	Roles       []string `json:"roles"`
}

// User is a user as GET /v1/users returns it. Email is the identifier;
// there is no separate user ID yet.
type User struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// IsAccountAdmin users hold every permission, so RoleAssignments comes
	// back empty for them.
	IsAccountAdmin  bool             `json:"isAccountAdmin"`
	RoleAssignments []RoleAssignment `json:"roleAssignments"`
}

// UserFilter narrows ListUsers. Empty fields aren't sent. Email is a
// case-insensitive substring match; Role matches a role held on any cloud
// account. Without Status, removed users are included as DISABLED.
type UserFilter struct {
	Email  string
	Role   string
	Status string
}

type usersPage struct {
	Users []User `json:"users"`
	Meta  struct {
		TotalCount int `json:"totalCount"`
		Pagination struct {
			TotalPage int `json:"totalPage"`
		} `json:"pagination"`
	} `json:"meta"`
}

// ListUsers returns every user matching the filter, following all pages.
func (c *Client) ListUsers(ctx context.Context, filter UserFilter) ([]User, error) {
	q := url.Values{}
	for k, v := range map[string]string{"email": filter.Email, "role": filter.Role, "status": filter.Status} {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("pageSize", strconv.Itoa(usersPageSize))

	var users []User
	for page := 0; ; page++ {
		q.Set("page", strconv.Itoa(page))
		var out usersPage
		if err := c.Do(ctx, http.MethodGet, usersPath+"?"+q.Encode(), nil, &out); err != nil {
			return nil, err
		}
		users = append(users, out.Users...)
		// Stop on the last page, or on an empty page so a wrong totalPage
		// can't loop forever.
		if len(out.Users) == 0 || page+1 >= out.Meta.Pagination.TotalPage {
			break
		}
	}
	return users, nil
}

// Permission is one action a role allows on one resource type.
type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// Role is a role users can be granted. Name is the identifier; there is no
// role ID.
type Role struct {
	Name           string       `json:"name"`
	Permissions    []Permission `json:"permissions"`
	NewUserDefault bool         `json:"newUserDefault"`
}

type rolesResponse struct {
	Roles []Role `json:"roles"`
}

// ListRoles returns every role on the account.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out rolesResponse
	if err := c.Do(ctx, http.MethodGet, rolesPath, nil, &out); err != nil {
		return nil, err
	}
	return out.Roles, nil
}
