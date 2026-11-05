package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Tenant API endpoints. The dashboard account is always resolved from the
// caller's access token, never sent explicitly.
//
// Revamped API (Tenant Linking API doc): list is GET /v1/tenants for every
// cloud; onboard, update and deboard put the cloud in the path. Only AWS is
// supported, by these and by the legacy endpoints they replace; Azure and
// GCP will come as /v1/tenants/azure and /v1/tenants/gcp.
const (
	tenantsPath           = "/v1/tenants"
	tenantsAWSOnboardPath = "/v1/tenants/aws/onboard"
	tenantsAWSUpdatePath  = "/v1/tenants/aws"
	tenantsAWSDeboardPath = "/v1/tenants/aws/deboard"
)

const cloudProviderAWS = "AWS"

// errUnsupportedCloud guards the onboard/update/deboard calls: the provider
// already rejects non-AWS tenants at plan time, so reaching this is a bug.
func errUnsupportedCloud(cloudProvider string) error {
	return fmt.Errorf("lucidity tenant API supports AWS only for now, got cloud provider %q", cloudProvider)
}

// awsCloudEntityInformation is cloudEntityInformation for the revamped
// /v1/tenants/aws endpoints: no cloudProvider (it's in the path). Optional fields are omitempty so an update only sends what
// changes.
type awsCloudEntityInformation struct {
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	ExternalID             string `json:"externalId,omitempty"`
	AWSIAMRoleName         string `json:"awsIAMRoleName,omitempty"`
	AWSIAMPolicyName       string `json:"awsIAMPolicyName,omitempty"`
	// AWSRootID isn't in the revamped doc either; the API ignores unknown
	// fields, so it's still sent when set (see CloudEntityInformation).
	AWSRootID string `json:"awsRootId,omitempty"`
}

func toAWSCloudEntityInformation(c CloudEntityInformation) awsCloudEntityInformation {
	return awsCloudEntityInformation{
		CloudProviderAccountID: c.CloudProviderAccountID,
		ExternalID:             c.ExternalID,
		AWSIAMRoleName:         c.AWSIAMRoleName,
		AWSIAMPolicyName:       c.AWSIAMPolicyName,
		AWSRootID:              c.AWSRootID,
	}
}

type awsOnboardRequest struct {
	CloudEntityInformation   awsCloudEntityInformation `json:"cloudEntityInformation"`
	DisplayName              string                    `json:"displayName"`
	SkipCloudPermissionCheck bool                      `json:"skipCloudPermissionCheck,omitempty"`
	ProductList              []string                  `json:"productList"`
}

type awsUpdateRequest struct {
	CloudEntityInformation awsCloudEntityInformation `json:"cloudEntityInformation"`
	DisplayName            string                    `json:"displayName,omitempty"`
}

// awsDeboardRequest sends the account ID at the top level: the revamped
// deboard ignores a cloudEntityInformation wrapper and answers
// VALIDATION_ERROR.
type awsDeboardRequest struct {
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
}

// Tenant status values, per the List/Update response "status" field.
const (
	TenantStatusActive         = "ACTIVE"
	TenantStatusInactive       = "INACTIVE"
	TenantStatusDecommissioned = "DECOMMISSIONED"
)

// Deboard result status values, per the Deboard response "status" field.
const (
	DeboardStatusDeBoarded        = "DE_BOARDED"
	DeboardStatusAlreadyDeBoarded = "ALREADY_DE_BOARDED"
)

// CloudEntityInformation identifies a tenant's cloud account and, on
// onboard/update, carries the AWS auth fields. Field applicability
// (required/optional/ignored) differs per call — see the doc comment on
// each method below. CloudProvider picks the endpoint; it isn't sent.
type CloudEntityInformation struct {
	CloudProvider          string `json:"cloudProvider"`
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	ExternalID             string `json:"externalId,omitempty"`
	AWSIAMRoleName         string `json:"awsIAMRoleName,omitempty"`
	AWSIAMPolicyName       string `json:"awsIAMPolicyName,omitempty"`

	// AWSRootID is NOT part of the documented Public Tenant API request
	// schema (confirmed absent from both the onboard and update field
	// tables, live-tested 2026-09-06). Sent anyway on the maintainer's
	// explicit instruction so it lands in Lucidity's records for accounts
	// where a shared IAM role/policy is assumed across an AWS Organization
	// — but since it's undocumented, Lucidity may silently ignore it or,
	// if it validates request bodies strictly, reject the call outright.
	// omitempty so a config that never sets aws_root_id sends exactly the
	// documented payload shape.
	AWSRootID string `json:"awsRootId,omitempty"`
}

// OnboardTenantRequest is the POST .../tenants/onboard request body.
type OnboardTenantRequest struct {
	CloudEntityInformation   CloudEntityInformation `json:"cloudEntityInformation"`
	DisplayName              string                 `json:"displayName"`
	SkipCloudPermissionCheck bool                   `json:"skipCloudPermissionCheck,omitempty"`
	ProductList              []string               `json:"productList"`
}

// OnboardedTenant is the OnboardTenant response's data field.
type OnboardedTenant struct {
	TenantID               string   `json:"tenantId"`
	CloudProvider          string   `json:"cloudProvider"`
	CloudProviderAccountID string   `json:"cloudProviderAccountId"`
	DisplayName            string   `json:"displayName"`
	Status                 string   `json:"status"`
	Products               []string `json:"products"`
}

// OnboardTenant connects an AWS account to Lucidity as a managed tenant via
// POST /v1/tenants/aws/onboard.
func (c *Client) OnboardTenant(ctx context.Context, req OnboardTenantRequest) (*OnboardedTenant, error) {
	if cp := req.CloudEntityInformation.CloudProvider; cp != cloudProviderAWS {
		return nil, errUnsupportedCloud(cp)
	}
	body := awsOnboardRequest{
		CloudEntityInformation:   toAWSCloudEntityInformation(req.CloudEntityInformation),
		DisplayName:              req.DisplayName,
		SkipCloudPermissionCheck: req.SkipCloudPermissionCheck,
		ProductList:              req.ProductList,
	}
	var out OnboardedTenant
	// DoNoRetry5xx, not Do: a 5xx on the onboard POST doesn't prove the
	// tenant wasn't created, and blindly re-sending it turns a
	// successful-but-5xx create into a masking 409. The caller re-lists to
	// find out what actually happened instead (see IsOutcomeUnknown).
	if err := c.DoNoRetry5xx(ctx, http.MethodPost, tenantsAWSOnboardPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TenantListItem is one entry in the ListTenants response.
type TenantListItem struct {
	TenantID               string `json:"tenantId"`
	CloudProvider          string `json:"cloudProvider"`
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	CloudEntityName        string `json:"cloudEntityName"`
	DisplayName            string `json:"displayName"`
	Status                 string `json:"status"`

	// ExternalID is the AWS IAM external ID Lucidity holds for the tenant
	// (nil when the API reports null). ExternalIDReported distinguishes a
	// backend that returns the key at all (true, even if null) from an older
	// one that omits it entirely (false) — only the former means "remote has
	// no external id"; the latter means "can't tell", and callers must not
	// treat it as a mismatch.
	ExternalID         *string `json:"externalId"`
	ExternalIDReported bool    `json:"-"`
}

// UnmarshalJSON records whether the externalId key was present at all.
func (t *TenantListItem) UnmarshalJSON(b []byte) error {
	type plain TenantListItem
	aux := struct {
		*plain
		ExternalID json.RawMessage `json:"externalId"`
	}{plain: (*plain)(t)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	t.ExternalID, t.ExternalIDReported = nil, len(aux.ExternalID) > 0
	if t.ExternalIDReported && string(aux.ExternalID) != "null" {
		var v string
		if err := json.Unmarshal(aux.ExternalID, &v); err != nil {
			return err
		}
		t.ExternalID = &v
	}
	return nil
}

type tenantListMeta struct {
	TotalCount int `json:"totalCount"`
}

type tenantListResponse struct {
	Tenants []TenantListItem `json:"tenants"`
	Meta    tenantListMeta   `json:"meta"`
}

// ListTenants returns every tenant under the caller's account (resolved from
// the access token), ACTIVE first then INACTIVE, per the doc.
func (c *Client) ListTenants(ctx context.Context) ([]TenantListItem, error) {
	var out tenantListResponse
	if err := c.Do(ctx, http.MethodGet, tenantsPath, nil, &out); err != nil {
		return nil, err
	}
	// totalCount counts every tenant under the account (INACTIVE included),
	// so it is only meaningful against this same response, never against
	// anything local. A larger count than returned means the list was cut
	// short (pagination), and any "not found" derived from it — Read removing
	// state, Create's duplicate pre-check, the 5xx recovery — would be
	// unreliable, so fail loudly instead. A missing/zero or smaller count
	// (older backend, stale count) is not evidence of truncation.
	if out.Meta.TotalCount > len(out.Tenants) {
		return nil, &TruncatedListError{Returned: len(out.Tenants), Total: out.Meta.TotalCount}
	}
	return out.Tenants, nil
}

// TruncatedListError means ListTenants returned fewer tenants than the API
// says exist. No page parameter is documented yet, so the provider cannot
// fetch the rest.
type TruncatedListError struct {
	Returned, Total int
}

func (e *TruncatedListError) Error() string {
	return fmt.Sprintf(
		"lucidity returned %d of %d tenants (the list is truncated, likely paginated); the provider cannot safely tell whether a tenant exists, so it stops rather than guess. Report this to the provider maintainers or Lucidity support — pagination support is needed",
		e.Returned, e.Total,
	)
}

// GetTenant reads one tenant by its Lucidity tenantId via
// GET /v1/tenants/{tenantId}. The response has the same fields as a list
// item. A 404 (APIError, HTTPStatus 404) means no such tenant under the
// caller's account.
func (c *Client) GetTenant(ctx context.Context, tenantID string) (*TenantListItem, error) {
	var out TenantListItem
	if err := c.Do(ctx, http.MethodGet, tenantsPath+"/"+url.PathEscape(tenantID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FindTenant returns the list entry matching provider+accountID, and whether
// one was found. Used by Read/Create for the list-and-match pattern the API
// requires (there is no GET-by-ID).
func FindTenant(tenants []TenantListItem, cloudProvider, cloudProviderAccountID string) (TenantListItem, bool) {
	for _, t := range tenants {
		if t.CloudProvider == cloudProvider && t.CloudProviderAccountID == cloudProviderAccountID {
			return t, true
		}
	}
	return TenantListItem{}, false
}

// UpdateTenantRequest is the PATCH .../tenants request body. Only
// CloudProvider and CloudProviderAccountID (to identify the tenant) plus
// whichever fields are changing should be set; the rest must stay zero so
// they're omitted and existing values are preserved (authInfo is merged
// server-side). DisplayName and/or at least one AWS auth field must be
// set, or the request is rejected. ExternalID (AWS) can be rotated via
// update; rotate the AWS trust policy first, since Lucidity re-validates by
// AssumeRole.
type UpdateTenantRequest struct {
	CloudEntityInformation CloudEntityInformation `json:"cloudEntityInformation"`
	DisplayName            string                 `json:"displayName,omitempty"`
}

// UpdatedTenant is the UpdateTenant response's data field.
type UpdatedTenant struct {
	TenantID               string `json:"tenantId"`
	CloudProvider          string `json:"cloudProvider"`
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	DisplayName            string `json:"displayName"`
	Status                 string `json:"status"`
}

// UpdateTenant applies a partial update to an existing, ACTIVE AWS tenant
// via PATCH /v1/tenants/aws.
func (c *Client) UpdateTenant(ctx context.Context, req UpdateTenantRequest) (*UpdatedTenant, error) {
	if cp := req.CloudEntityInformation.CloudProvider; cp != cloudProviderAWS {
		return nil, errUnsupportedCloud(cp)
	}
	body := awsUpdateRequest{
		CloudEntityInformation: toAWSCloudEntityInformation(req.CloudEntityInformation),
		DisplayName:            req.DisplayName,
	}
	var out UpdatedTenant
	if err := c.Do(ctx, http.MethodPatch, tenantsAWSUpdatePath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeboardResult is the DeboardTenant response's data field.
type DeboardResult struct {
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	Status                 string `json:"status"` // DE_BOARDED or ALREADY_DE_BOARDED
	Message                string `json:"message"`
}

// DeboardTenant marks an AWS tenant INACTIVE via PUT
// /v1/tenants/aws/deboard. Idempotent: deboarding an
// already-inactive tenant succeeds (Status == DeboardStatusAlreadyDeBoarded)
// rather than erroring. IRREVERSIBLE via API — an INACTIVE tenant can only
// be reactivated by Lucidity support.
func (c *Client) DeboardTenant(ctx context.Context, cloudProvider, cloudProviderAccountID string) (*DeboardResult, error) {
	if cloudProvider != cloudProviderAWS {
		return nil, errUnsupportedCloud(cloudProvider)
	}
	var out DeboardResult
	body := awsDeboardRequest{CloudProviderAccountID: cloudProviderAccountID}
	if err := c.Do(ctx, http.MethodPut, tenantsAWSDeboardPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
