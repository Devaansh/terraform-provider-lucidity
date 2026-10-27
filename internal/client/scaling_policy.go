package client

import (
	"context"
	"net/http"
	"strconv"
)

// scalingPoliciesPath is the Autoscaler Scaling Policies API base path
// (revamped; replaces /public/v1/scaling-policies).
const scalingPoliciesPath = "/autoscaler/v1/scaling-policies"

// ScalingPolicySettings are a scaling policy's seven user-set fields. Create
// requires all seven; Update (PUT) replaces them and resets any omitted
// field to 0, so callers must always send every field.
type ScalingPolicySettings struct {
	Name                    string `json:"name"`
	MinBufferInGb           int64  `json:"minBufferInGb"`
	MaxBufferInGb           int64  `json:"maxBufferInGb"`
	IdealUtilizationPercent int64  `json:"idealUtilizationPercent"`
	MaxFsSizeInGb           int64  `json:"maxFsSizeInGb"`
	MinDiskSkuSize          int64  `json:"minDiskSkuSize"`
	MaxDiskSkuSize          int64  `json:"maxDiskSkuSize"`
}

// ScalingPolicy is a policy as the API returns it.
type ScalingPolicy struct {
	ID int64 `json:"id"`
	ScalingPolicySettings
	// AttachedInstanceCount is how many instances use the policy. The API
	// exposes only this count, not which instances.
	AttachedInstanceCount int64 `json:"attachedInstanceCount"`
}

// PolicyInstanceIdentifier identifies an instance to attach or detach.
// CloudProvider is AWS, AZURE or GCP (case-sensitive); ResourceGroup is
// required for AZURE only.
type PolicyInstanceIdentifier struct {
	CloudProvider          string `json:"cloudProvider"`
	CloudProviderAccountID string `json:"cloudProviderAccountId"`
	ResourceGroup          string `json:"resourceGroup,omitempty"`
	InstanceID             string `json:"instanceId"`
}

type policyInstancesRequest struct {
	InstanceIdentifiers []PolicyInstanceIdentifier `json:"instanceIdentifiers"`
}

func scalingPolicyPath(id int64) string {
	return scalingPoliciesPath + "/" + strconv.FormatInt(id, 10)
}

// ListScalingPolicies returns every scaling policy in the caller's account.
func (c *Client) ListScalingPolicies(ctx context.Context) ([]ScalingPolicy, error) {
	var out []ScalingPolicy
	if err := c.Do(ctx, http.MethodGet, scalingPoliciesPath, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateScalingPolicy creates a policy. The create response doesn't echo
// minDiskSkuSize, maxDiskSkuSize or attachedInstanceCount (they read 0), so
// call GetScalingPolicy for the stored values.
//
// DoNoRetry5xx: a 5xx doesn't prove the policy wasn't created, and a blind
// retry would then fail with a misleading name CONFLICT. The caller looks
// the policy up by name instead (names are unique per account).
func (c *Client) CreateScalingPolicy(ctx context.Context, settings ScalingPolicySettings) (*ScalingPolicy, error) {
	var out ScalingPolicy
	if err := c.DoNoRetry5xx(ctx, http.MethodPost, scalingPoliciesPath, settings, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetScalingPolicy fetches one policy. 404 means no such policy in the
// caller's account (another account's policy also returns 404).
func (c *Client) GetScalingPolicy(ctx context.Context, id int64) (*ScalingPolicy, error) {
	var out ScalingPolicy
	if err := c.Do(ctx, http.MethodGet, scalingPolicyPath(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateScalingPolicy replaces all of a policy's settings.
func (c *Client) UpdateScalingPolicy(ctx context.Context, id int64, settings ScalingPolicySettings) (*ScalingPolicy, error) {
	var out ScalingPolicy
	if err := c.Do(ctx, http.MethodPut, scalingPolicyPath(id), settings, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteScalingPolicy deletes a policy.
func (c *Client) DeleteScalingPolicy(ctx context.Context, id int64) error {
	return c.Do(ctx, http.MethodDelete, scalingPolicyPath(id), nil, nil)
}

// AttachScalingPolicy attaches a policy to instances.
func (c *Client) AttachScalingPolicy(ctx context.Context, id int64, instances []PolicyInstanceIdentifier) error {
	return c.Do(ctx, http.MethodPost, scalingPolicyPath(id)+"/attach", policyInstancesRequest{InstanceIdentifiers: instances}, nil)
}

// DetachScalingPolicy detaches a policy from instances.
func (c *Client) DetachScalingPolicy(ctx context.Context, id int64, instances []PolicyInstanceIdentifier) error {
	return c.Do(ctx, http.MethodPost, scalingPolicyPath(id)+"/detach", policyInstancesRequest{InstanceIdentifiers: instances}, nil)
}

// FindScalingPolicyByName returns the policy with this name, if any.
func FindScalingPolicyByName(policies []ScalingPolicy, name string) (ScalingPolicy, bool) {
	for _, p := range policies {
		if p.Name == name {
			return p, true
		}
	}
	return ScalingPolicy{}, false
}
