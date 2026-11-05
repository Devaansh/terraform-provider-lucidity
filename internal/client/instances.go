package client

import (
	"context"
	"fmt"
	"net/http"
)

// instancesPath is the Autoscaler Instances API (revamped; replaces
// POST /public/v1/instances/details).
const instancesPath = "/autoscaler/v1/instances"

// MaxInstancesPerRequest is the API's batch limit.
const MaxInstancesPerRequest = 100

// AttachedScalingPolicy is the scaling policy attached to an instance's
// managed partitions.
type AttachedScalingPolicy struct {
	ScalingPolicyID         int64  `json:"scalingPolicyId"`
	ScalingPolicyName       string `json:"scalingPolicyName"`
	MinBufferInGb           int64  `json:"minBufferInGb"`
	IdealUtilizationPercent int64  `json:"idealUtilizationPercent"`
	MaxBufferInGb           int64  `json:"maxBufferInGb"`
}

// PartitionEntry is one mount point on an instance.
type PartitionEntry struct {
	MountPoint               string `json:"mountPoint"`
	Status                   string `json:"status"`
	CustomRebootBasedOnboard bool   `json:"customRebootBasedOnboard"`
}

// InstanceDetails is one entry of the instances response. Entries come back
// in request order; an instance Lucidity has no record of still gets an
// entry, with empty partitions and null agent fields.
type InstanceDetails struct {
	InstanceIdentifier struct {
		PolicyInstanceIdentifier
		TenantID string `json:"tenantId"`
	} `json:"instanceIdentifier"`
	ManagedPartition struct {
		InstanceState    *string          `json:"instanceState"`
		PartitionEntries []PartitionEntry `json:"partitionEntries"`
		// ScalingPolicy is nil when no policy is attached, or when the
		// policy couldn't be read (the API doesn't distinguish the two).
		ScalingPolicy *AttachedScalingPolicy `json:"scalingPolicyResponse"`
	} `json:"managedPartition"`
	UnmanagedPartition struct {
		InstanceState    *string          `json:"instanceState"`
		InstanceStatus   *string          `json:"instanceStatus"`
		PartitionEntries []PartitionEntry `json:"partitionEntries"`
	} `json:"unmanagedPartition"`
	AgentVersion    *string `json:"agentVersion"`
	AgentHealth     *string `json:"agentHealth"`
	LastAgentUpTime *string `json:"lastAgentUpTime"`
}

type instancesRequest struct {
	Instances []PolicyInstanceIdentifier `json:"instances"`
}

type instancesResponse struct {
	Instances []InstanceDetails `json:"instances"`
}

// GetInstances returns details, including the attached scaling policy, for
// 1 to 100 instances, in request order. All or nothing: one bad entry (or
// one from another account, 403) rejects the whole request.
func (c *Client) GetInstances(ctx context.Context, instances []PolicyInstanceIdentifier) ([]InstanceDetails, error) {
	if len(instances) == 0 || len(instances) > MaxInstancesPerRequest {
		return nil, fmt.Errorf("lucidity: GetInstances takes 1 to %d instances, got %d", MaxInstancesPerRequest, len(instances))
	}
	var out instancesResponse
	if err := c.Do(ctx, http.MethodPost, instancesPath, instancesRequest{Instances: instances}, &out); err != nil {
		return nil, err
	}
	if len(out.Instances) != len(instances) {
		return nil, fmt.Errorf("lucidity: asked for %d instances, got %d entries back", len(instances), len(out.Instances))
	}
	return out.Instances, nil
}
