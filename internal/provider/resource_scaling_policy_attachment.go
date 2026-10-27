package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/luciditycloud/terraform-provider-lucidity/internal/client"
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
			"Lucidity's API reports only how many instances a policy is attached to, not which, so Terraform can't detect an attach or detach made outside Terraform; the attachment is removed from state only if the policy itself is gone.",
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
		},
		Blocks: map[string]schema.Block{
			"instance_identifier": schema.SingleNestedBlock{
				Description: "The instance to attach the policy to. Required.",
				Validators:  []validator.Object{objectvalidator.IsRequired()},
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
	var cfg scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !cfg.PolicyID.IsUnknown() && !cfg.PolicyID.IsNull() {
		if _, err := parsePolicyID(cfg.PolicyID.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("policy_id"), "Invalid policy_id", err.Error())
		}
	}
	id := cfg.InstanceIdentifier
	if id == nil || id.CloudProvider.IsUnknown() || id.CloudProvider.IsNull() {
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
// The API can't confirm the attachment exists, so import only checks the
// policy does.
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
	resp.Diagnostics.AddWarning("Attachment assumed, not verified",
		"Lucidity's API can't list which instances a policy is attached to, so import records this attachment without confirming it exists. Only the policy's existence is checked on refresh.")
}

func (r *scalingPolicyAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
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

// Read can only check that the policy still exists: the API doesn't say
// which instances it's attached to.
func (r *scalingPolicyAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state scalingPolicyAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policyID, err := parsePolicyID(state.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy_id in state", err.Error())
		return
	}
	if _, err := r.client.GetScalingPolicy(ctx, policyID); err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Scaling policy no longer found",
				fmt.Sprintf("Scaling policy %d no longer exists, so this attachment is gone too. Removing it from Terraform state.", policyID))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read Lucidity scaling policy", err.Error())
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
				fmt.Sprintf("Couldn't %s scaling policy %d: Lucidity says it belongs to another account, or the X-Authtype header is unsupported (%s).", action, policyID, apiErr.Error()))
			return
		}
	}
	diags.AddError(fmt.Sprintf("Unable to %s Lucidity scaling policy", action), err.Error())
}
