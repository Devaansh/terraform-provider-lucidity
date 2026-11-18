package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ resource.Resource                   = &scalingPolicyAttachmentResource{}
	_ resource.ResourceWithConfigure      = &scalingPolicyAttachmentResource{}
	_ resource.ResourceWithImportState    = &scalingPolicyAttachmentResource{}
	_ resource.ResourceWithValidateConfig = &scalingPolicyAttachmentResource{}
)

func newScalingPolicyAttachmentResource() resource.Resource {
	return &scalingPolicyAttachmentResource{}
}

type scalingPolicyAttachmentResource struct {
	client *client.Client
}

type policyInstanceModel struct {
	CloudProvider          types.String `tfsdk:"cloud_provider"`
	CloudProviderAccountID types.String `tfsdk:"cloud_provider_account_id"`
	ResourceGroup          types.String `tfsdk:"resource_group"`
	InstanceID             types.String `tfsdk:"instance_id"`
}

type scalingPolicyAttachmentModel struct {
	ID                 types.String         `tfsdk:"id"`
	PolicyID           types.String         `tfsdk:"policy_id"`
	InstanceIdentifier *policyInstanceModel `tfsdk:"instance_identifier"`
	TenantID           types.String         `tfsdk:"tenant_id"`
}

func (r *scalingPolicyAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scaling_policy_attachment"
}

func (r *scalingPolicyAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	clients, ok := req.ProviderData.(*LucidityClients)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *provider.LucidityClients, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	r.client = clients.Client
}

func (r *scalingPolicyAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Attaches a lucidity_scaling_policy to one instance. Destroying it detaches the policy. One policy can be attached to many instances: use one attachment per instance. " +
			"Every refresh checks which policy the instance actually has; if it has none or a different one (changed outside Terraform), the next apply attaches this policy again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "\"<policy_id>/<cloud_provider>/<cloud_provider_account_id>/<instance_id>\" (with \"/<resource_group>\" before the instance ID for Azure). Also the import ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"policy_id": schema.StringAttribute{
				Required:      true,
				Description:   "ID of the scaling policy to attach (lucidity_scaling_policy.<name>.id).",
				PlanModifiers: replace,
			},
			"tenant_id": tenantIDResourceAttribute(),
			"instance_identifier": schema.SingleNestedAttribute{
				Required:    true,
				Description: "The instance to attach the policy to.",
				Attributes: map[string]schema.Attribute{
					"cloud_provider": schema.StringAttribute{
						Required:      true,
						Description:   "AWS, AZURE, or GCP.",
						Validators:    []validator.String{stringvalidator.OneOf("AWS", "AZURE", "GCP")},
						PlanModifiers: replace,
					},
					"cloud_provider_account_id": schema.StringAttribute{
						Required:      true,
						Description:   "The AWS account ID (12 digits), Azure subscription ID, or GCP project ID.",
						PlanModifiers: replace,
					},
					"resource_group": schema.StringAttribute{
						Optional:      true,
						Description:   "AZURE only, and required there: the VM's resource group.",
						PlanModifiers: replace,
					},
					"instance_id": schema.StringAttribute{
						Required:      true,
						Description:   "The instance ID (the VM name in Azure).",
						PlanModifiers: replace,
					},
				},
			},
		},
	}
}

func (r *scalingPolicyAttachmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var policyID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("policy_id"), &policyID)...)
	if !policyID.IsUnknown() && !policyID.IsNull() {
		if _, err := parsePolicyID(policyID.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("policy_id"), "Invalid policy_id", err.Error())
		}
	}

	// instance_identifier is an attribute, so the whole object can be
	// unknown at validate time (e.g. built from another resource's output).
	// Read it as an Object first and skip the checks until it's known.
	var obj types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("instance_identifier"), &obj)...)
	if resp.Diagnostics.HasError() || obj.IsNull() || obj.IsUnknown() {
		return
	}
	var inst policyInstanceModel
	resp.Diagnostics.Append(obj.As(ctx, &inst, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := &inst
	if id.CloudProvider.IsUnknown() || id.CloudProvider.IsNull() {
		return
	}
	rg := path.Root("instance_identifier").AtName("resource_group")
	switch id.CloudProvider.ValueString() {
	case "AZURE":
		if isMissingRequiredString(id.ResourceGroup) {
			resp.Diagnostics.AddAttributeError(rg, "Missing resource_group for Azure", "instance_identifier.resource_group is required when cloud_provider is \"AZURE\".")
		}
	default:
		if !id.ResourceGroup.IsNull() && !id.ResourceGroup.IsUnknown() {
			resp.Diagnostics.AddAttributeError(rg, "resource_group is Azure-only", fmt.Sprintf("instance_identifier.resource_group only applies when cloud_provider is \"AZURE\", got %q. Remove it.", id.CloudProvider.ValueString()))
		}
	}
	if id.CloudProvider.ValueString() == "AWS" {
		if acct := id.CloudProviderAccountID; !acct.IsUnknown() && !awsAccountIDPattern.MatchString(acct.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root("instance_identifier").AtName("cloud_provider_account_id"), "Invalid AWS account ID format",
				fmt.Sprintf("instance_identifier.cloud_provider_account_id must be exactly 12 digits for cloud_provider = \"AWS\", got: %q.", acct.ValueString()))
		}
	}
}

func attachmentID(m scalingPolicyAttachmentModel) string {
	i := m.InstanceIdentifier
	parts := []string{m.PolicyID.ValueString(), i.CloudProvider.ValueString(), i.CloudProviderAccountID.ValueString()}
	if i.CloudProvider.ValueString() == "AZURE" {
		parts = append(parts, i.ResourceGroup.ValueString())
	}
	return strings.Join(append(parts, i.InstanceID.ValueString()), "/")
}

func instanceFromModel(i *policyInstanceModel) client.PolicyInstanceIdentifier {
	return client.PolicyInstanceIdentifier{
		CloudProvider:          i.CloudProvider.ValueString(),
		CloudProviderAccountID: i.CloudProviderAccountID.ValueString(),
		ResourceGroup:          i.ResourceGroup.ValueString(),
		InstanceID:             i.InstanceID.ValueString(),
	}
}

// ImportState accepts "<policy_id>/<cloud>/<account>/<instance_id>", or
// "<policy_id>/AZURE/<subscription>/<resource_group>/<vm_name>" for Azure.
// The Read that follows checks the instance really has that policy; if not,
// Terraform reports that the object doesn't exist.
func (r *scalingPolicyAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	want := 4
	if len(parts) > 1 && parts[1] == "AZURE" {
		want = 5
	}
	if len(parts) != want {
		resp.Diagnostics.AddError("Unexpected import ID format",
			fmt.Sprintf("Expected \"<policy_id>/<cloud_provider>/<cloud_provider_account_id>/<instance_id>\" (Azure: \"<policy_id>/AZURE/<subscription_id>/<resource_group>/<vm_name>\"), got %q.", req.ID))
		return
	}
	m := scalingPolicyAttachmentModel{
		TenantID: types.StringNull(),
		PolicyID: types.StringValue(parts[0]),
		InstanceIdentifier: &policyInstanceModel{
			CloudProvider:          types.StringValue(parts[1]),
			CloudProviderAccountID: types.StringValue(parts[2]),
			ResourceGroup:          types.StringNull(),
			InstanceID:             types.StringValue(parts[want-1]),
		},
	}
	if want == 5 {
		m.InstanceIdentifier.ResourceGroup = types.StringValue(parts[3])
	}
	m.ID = types.StringValue(attachmentID(m))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *scalingPolicyAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	policyID, err := parsePolicyID(plan.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy_id", err.Error())
		return
	}
	instance := instanceFromModel(plan.InstanceIdentifier)
	if err := r.client.AttachScalingPolicy(ctx, policyID, []client.PolicyInstanceIdentifier{instance}); err != nil {
		handleAttachError(&resp.Diagnostics, err, "attach", policyID, instance)
		return
	}
	plan.ID = types.StringValue(attachmentID(plan))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read asks Lucidity which policy the instance actually has
// (POST /autoscaler/v1/instances). If it has none, or a different one, the
// attachment is gone: drop it from state so the next plan re-attaches.
func (r *scalingPolicyAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	policyID, err := parsePolicyID(state.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy_id in state", err.Error())
		return
	}
	instance := instanceFromModel(state.InstanceIdentifier)

	details, err := r.client.GetInstances(ctx, []client.PolicyInstanceIdentifier{instance})
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusForbidden {
			resp.Diagnostics.AddError("Instance's cloud account not accessible",
				fmt.Sprintf("Lucidity says %s account %s doesn't belong to this Lucidity account (%s).", instance.CloudProvider, instance.CloudProviderAccountID, apiErr.Error()))
			return
		}
		resp.Diagnostics.AddError("Unable to read Lucidity instance", err.Error())
		return
	}

	attached := details[0].ManagedPartition.ScalingPolicy
	switch {
	case attached == nil:
		resp.Diagnostics.AddWarning("Scaling policy no longer attached",
			fmt.Sprintf("Instance %s has no scaling policy in Lucidity (it was detached outside Terraform, the instance was removed, or its policy was deleted). Removing the attachment from state; the next apply attaches policy %d again if it's still in your configuration.", instance.InstanceID, policyID))
		resp.State.RemoveResource(ctx)
		return
	case attached.ScalingPolicyID != policyID:
		resp.Diagnostics.AddWarning("Instance has a different scaling policy",
			fmt.Sprintf("Instance %s now has scaling policy %d (%q), not %d. Removing this attachment from state; the next apply attaches policy %d again if it's still in your configuration.", instance.InstanceID, attached.ScalingPolicyID, attached.ScalingPolicyName, policyID, policyID))
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update never runs: every attribute forces replacement.
func (r *scalingPolicyAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scalingPolicyAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	policyID, err := parsePolicyID(state.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy_id in state", err.Error())
		return
	}
	instance := instanceFromModel(state.InstanceIdentifier)
	if err := r.client.DetachScalingPolicy(ctx, policyID, []client.PolicyInstanceIdentifier{instance}); err != nil {
		if isNotFound(err) {
			return // policy, instance or account already gone: nothing to detach
		}
		handleAttachError(&resp.Diagnostics, err, "detach", policyID, instance)
	}
}

func handleAttachError(diags *diag.Diagnostics, err error, action string, policyID int64, i client.PolicyInstanceIdentifier) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatus {
		case http.StatusNotFound:
			diags.AddError("Scaling policy, instance or account not found",
				fmt.Sprintf("Couldn't %s scaling policy %d for instance %s (%s account %s): the policy, the instance or the account wasn't found in Lucidity (%s). Check the instance is managed by Lucidity and the IDs are right.",
					action, policyID, i.InstanceID, i.CloudProvider, i.CloudProviderAccountID, apiErr.Error()))
			return
		case http.StatusForbidden:
			diags.AddError("Scaling policy belongs to another account",
				fmt.Sprintf("Couldn't %s scaling policy %d: Lucidity says it belongs to another account (%s).", action, policyID, apiErr.Error()))
			return
		}
	}
	diags.AddError(fmt.Sprintf("Unable to %s Lucidity scaling policy", action), err.Error())
}
