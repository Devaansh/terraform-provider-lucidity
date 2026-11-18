package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Service Account API (revamped): non-human identities with roles per
// tenant and client-ID/secret credentials. Only account admins can create
// or change anything; a service account's own token can read but gets 403
// on every write.
const serviceAccountsPath = "/v1/service-accounts"

func serviceAccountPath(id string) string {
	return serviceAccountsPath + "/" + url.PathEscape(id)
}

// ServiceAccount is a service account as the API returns it.
type ServiceAccount struct {
	ID          string `json:"id"`
	AccountID   int64  `json:"accountId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type createServiceAccountRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ServiceAccountUpdate is a partial update: nil fields aren't sent.
type ServiceAccountUpdate struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// CreateServiceAccount creates an enabled service account. Names are unique
// per account ignoring case, and a deleted account's name can't be reused.
//
// DoNoRetry5xx: a 5xx doesn't prove nothing was created, and a blind retry
// would then fail on the name. The caller looks the name up instead.
func (c *Client) CreateServiceAccount(ctx context.Context, name, description string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.DoNoRetry5xx(ctx, http.MethodPost, serviceAccountsPath, createServiceAccountRequest{Name: name, Description: description}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListServiceAccounts returns every service account in the caller's account.
func (c *Client) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	var out []ServiceAccount
	if err := c.Do(ctx, http.MethodGet, serviceAccountsPath, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetServiceAccount fetches one service account; 404 if it doesn't exist
// in the caller's account or was deleted.
func (c *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.Do(ctx, http.MethodGet, serviceAccountPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateServiceAccount changes name, description and/or enabled.
// Disabling stops new access tokens without deleting anything.
func (c *Client) UpdateServiceAccount(ctx context.Context, id string, u ServiceAccountUpdate) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.Do(ctx, http.MethodPatch, serviceAccountPath(id), u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteServiceAccount deletes a service account permanently: its
// credentials stop working and its name can't be reused.
func (c *Client) DeleteServiceAccount(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, serviceAccountPath(id), nil, nil)
}

// FindServiceAccountByName matches ignoring case, like the API's uniqueness
// rule.
func FindServiceAccountByName(accounts []ServiceAccount, name string) (ServiceAccount, bool) {
	for _, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return ServiceAccount{}, false
}

// ServiceAccountPermissions maps tenant IDs to role names. Enabled is the
// role mapping's own switch: false means the roles are kept but don't apply.
type ServiceAccountPermissions struct {
	ServiceAccountID string              `json:"serviceAccountId"`
	AccountID        int64               `json:"accountId"`
	TenantRoleMap    map[string][]string `json:"tenantRoleMap"`
	Enabled          bool                `json:"enabled"`
}

type replacePermissionsRequest struct {
	TenantRoleMap map[string][]string `json:"tenantRoleMap"`
	Enabled       bool                `json:"enabled"`
}

// GetServiceAccountPermissions returns the service account's tenant roles.
func (c *Client) GetServiceAccountPermissions(ctx context.Context, id string) (*ServiceAccountPermissions, error) {
	var out ServiceAccountPermissions
	if err := c.Do(ctx, http.MethodGet, serviceAccountPath(id)+"/permissions", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReplaceServiceAccountPermissions replaces the whole tenant → roles map: a
// tenant left out loses access. It sends enabled: true. That flag belongs to
// the role mapping itself ("enabled: false switches off this role mapping
// without deleting it"), not to the service account, which has its own
// enabled flag set via PATCH.
func (c *Client) ReplaceServiceAccountPermissions(ctx context.Context, id string, tenantRoles map[string][]string) (*ServiceAccountPermissions, error) {
	return c.putPermissions(ctx, id, tenantRoles, true)
}

// SwitchOffServiceAccountPermissions keeps the given roles but switches the
// role mapping off, so they grant nothing. This is how every role is
// removed: an empty tenantRoleMap may be rejected (each tenant needs at
// least one role), and switching off works either way.
func (c *Client) SwitchOffServiceAccountPermissions(ctx context.Context, id string, tenantRoles map[string][]string) (*ServiceAccountPermissions, error) {
	return c.putPermissions(ctx, id, tenantRoles, false)
}

func (c *Client) putPermissions(ctx context.Context, id string, tenantRoles map[string][]string, enabled bool) (*ServiceAccountPermissions, error) {
	if tenantRoles == nil {
		tenantRoles = map[string][]string{}
	}
	var out ServiceAccountPermissions
	if err := c.Do(ctx, http.MethodPut, serviceAccountPath(id)+"/permissions", replacePermissionsRequest{TenantRoleMap: tenantRoles, Enabled: enabled}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ServiceAccountCredential is a client ID/secret pair. ClientSecret is only
// returned by create; list always returns it as null.
type ServiceAccountCredential struct {
	TokenID      string  `json:"tokenId"`
	Name         string  `json:"name"`
	ClientID     string  `json:"clientId"`
	ClientSecret *string `json:"clientSecret"`
	Active       bool    `json:"active"`
	ExpiresAt    string  `json:"expiresAt"`
	CreatedAt    string  `json:"createdAt"`
	LastUsedAt   *string `json:"lastUsedAt"`
}

type createCredentialRequest struct {
	Name      string `json:"name"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// CreateServiceAccountCredential creates a credential. expiresAt is RFC 3339
// and optional (the API defaults to 90 days from now). The secret is in the
// response only, never again.
//
// DoNoRetry5xx: a retry after a lost response would create a second
// credential, and the first one's secret could never be recovered.
func (c *Client) CreateServiceAccountCredential(ctx context.Context, serviceAccountID, name, expiresAt string) (*ServiceAccountCredential, error) {
	var out ServiceAccountCredential
	if err := c.DoNoRetry5xx(ctx, http.MethodPost, serviceAccountPath(serviceAccountID)+"/tokens", createCredentialRequest{Name: name, ExpiresAt: expiresAt}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListServiceAccountCredentials returns the service account's credentials,
// without secrets. There is no get-one endpoint.
func (c *Client) ListServiceAccountCredentials(ctx context.Context, serviceAccountID string) ([]ServiceAccountCredential, error) {
	var out []ServiceAccountCredential
	if err := c.Do(ctx, http.MethodGet, serviceAccountPath(serviceAccountID)+"/tokens", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeServiceAccountCredential revokes one credential.
func (c *Client) RevokeServiceAccountCredential(ctx context.Context, serviceAccountID, tokenID string) error {
	return c.Do(ctx, http.MethodDelete, serviceAccountPath(serviceAccountID)+"/tokens/"+url.PathEscape(tokenID), nil, nil)
}
