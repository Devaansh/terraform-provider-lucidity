package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ resource.Resource                   = &scalingPolicyResource{}
	_ resource.ResourceWithConfigure      = &scalingPolicyResource{}
	_ resource.ResourceWithImportState    = &scalingPolicyResource{}
	_ resource.ResourceWithValidateConfig = &scalingPolicyResource{}
)

func newScalingPolicyResource() resource.Resource {
	return &scalingPolicyResource{}
}

type scalingPolicyResource struct {
	client *client.Client
}

type scalingPolicyResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	MinBufferInGb           types.Int64  `tfsdk:"min_buffer_in_gb"`
	MaxBufferInGb           types.Int64  `tfsdk:"max_buffer_in_gb"`
	IdealUtilizationPercent types.Int64  `tfsdk:"ideal_utilization_percent"`
	MaxFsSizeInGb           types.Int64  `tfsdk:"max_fs_size_in_gb"`
	MinDiskSkuSize          types.Int64  `tfsdk:"min_disk_sku_size"`
	MaxDiskSkuSize          types.Int64  `tfsdk:"max_disk_sku_size"`
	AttachedInstanceCount   types.Int64  `tfsdk:"attached_instance_count"`
	TenantID                types.String `tfsdk:"tenant_id"`
}

func (r *scalingPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scaling_policy"
}

func (r *scalingPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *scalingPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	nonNegative := []validator.Int64{int64validator.AtLeast(0)}
	resp.Schema = schema.Schema{
		Description: "A Lucidity autoscaler scaling policy: the buffer and size limits Lucidity applies to the disks of the instances it's attached to. " +
			"Attach it to instances with lucidity_scaling_policy_attachment. All settings are updatable in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Lucidity's numeric ID for the policy. Also the import ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Policy name. Must be unique within the Lucidity account.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"min_buffer_in_gb": schema.Int64Attribute{
				Required:    true,
				Description: "Minimum free space (buffer) to keep, in GB.",
				Validators:  nonNegative,
			},
			"max_buffer_in_gb": schema.Int64Attribute{
				Required:    true,
				Description: "Maximum free space (buffer) to keep, in GB.",
				Validators:  nonNegative,
			},
			"ideal_utilization_percent": schema.Int64Attribute{
				Required:    true,
				Description: "Target disk utilization, 1 to 100 percent.",
				Validators:  []validator.Int64{int64validator.Between(1, 100)},
			},
			"max_fs_size_in_gb": schema.Int64Attribute{
				Required:    true,
				Description: "Maximum file system size, in GB.",
				Validators:  nonNegative,
			},
			"min_disk_sku_size": schema.Int64Attribute{
				Required:    true,
				Description: "Minimum disk SKU size.",
				Validators:  nonNegative,
			},
			"max_disk_sku_size": schema.Int64Attribute{
				Required:    true,
				Description: "Maximum disk SKU size.",
				Validators:  nonNegative,
			},
			"attached_instance_count": schema.Int64Attribute{
				Computed:    true,
				Description: "How many instances the policy is attached to. Lucidity reports only the count, not which instances.",
			},
			"tenant_id": tenantIDResourceAttribute(),
		},
	}
}

// ValidateConfig fails at plan time when a minimum is above its maximum,
// instead of leaving it to the API. The API docs don't state these ranges,
// so this is only the obvious check: equal values are allowed.
func (r *scalingPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg scalingPolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	checkMinNotAboveMax(&resp.Diagnostics, "min_buffer_in_gb", cfg.MinBufferInGb, "max_buffer_in_gb", cfg.MaxBufferInGb)
	checkMinNotAboveMax(&resp.Diagnostics, "min_disk_sku_size", cfg.MinDiskSkuSize, "max_disk_sku_size", cfg.MaxDiskSkuSize)
}

// checkMinNotAboveMax skips values not known yet (e.g. from a variable
// computed at apply time); Terraform validates again once they are.
func checkMinNotAboveMax(diags *diag.Diagnostics, minName string, minVal types.Int64, maxName string, maxVal types.Int64) {
	if minVal.IsNull() || minVal.IsUnknown() || maxVal.IsNull() || maxVal.IsUnknown() {
		return
	}
	if minVal.ValueInt64() > maxVal.ValueInt64() {
		diags.AddAttributeError(path.Root(minName), fmt.Sprintf("%s is greater than %s", minName, maxName),
			fmt.Sprintf("%s (%d) must not be greater than %s (%d).", minName, minVal.ValueInt64(), maxName, maxVal.ValueInt64()))
	}
}

func settingsFromModel(m scalingPolicyResourceModel) client.ScalingPolicySettings {
	return client.ScalingPolicySettings{
		Name:                    m.Name.ValueString(),
		MinBufferInGb:           m.MinBufferInGb.ValueInt64(),
		MaxBufferInGb:           m.MaxBufferInGb.ValueInt64(),
		IdealUtilizationPercent: m.IdealUtilizationPercent.ValueInt64(),
		MaxFsSizeInGb:           m.MaxFsSizeInGb.ValueInt64(),
		MinDiskSkuSize:          m.MinDiskSkuSize.ValueInt64(),
		MaxDiskSkuSize:          m.MaxDiskSkuSize.ValueInt64(),
	}
}

// applyScalingPolicy copies the API's view into the model. The API returns
// every setting, so Read detects drift on all of them.
func applyScalingPolicy(m *scalingPolicyResourceModel, p *client.ScalingPolicy) {
	m.ID = types.StringValue(strconv.FormatInt(p.ID, 10))
	m.Name = types.StringValue(p.Name)
	m.MinBufferInGb = types.Int64Value(p.MinBufferInGb)
	m.MaxBufferInGb = types.Int64Value(p.MaxBufferInGb)
	m.IdealUtilizationPercent = types.Int64Value(p.IdealUtilizationPercent)
	m.MaxFsSizeInGb = types.Int64Value(p.MaxFsSizeInGb)
	m.MinDiskSkuSize = types.Int64Value(p.MinDiskSkuSize)
	m.MaxDiskSkuSize = types.Int64Value(p.MaxDiskSkuSize)
	m.AttachedInstanceCount = types.Int64Value(p.AttachedInstanceCount)
}

func parsePolicyID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("expected a numeric scaling policy ID, got %q", s)
	}
	return id, nil
}

func (r *scalingPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parsePolicyID(req.ID); err != nil {
		resp.Diagnostics.AddError("Unexpected import ID format", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *scalingPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan scalingPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	settings := settingsFromModel(plan)

	created, err := r.client.CreateScalingPolicy(ctx, settings)
	if err != nil {
		if !client.IsOutcomeUnknown(ctx, err) {
			r.handleCreateError(ctx, &resp.Diagnostics, err, settings.Name)
			return
		}
		// A 5xx or lost response may still have created it. Names are unique
		// per account, so look it up by name rather than re-POSTing.
		policies, listErr := r.client.ListScalingPolicies(ctx)
		if listErr != nil {
			resp.Diagnostics.AddError("Unable to create Lucidity scaling policy", fmt.Sprintf("%s; checking whether it was created anyway also failed: %s", err, listErr))
			return
		}
		found, ok := client.FindScalingPolicyByName(policies, settings.Name)
		if !ok {
			resp.Diagnostics.AddError("Unable to create Lucidity scaling policy", fmt.Sprintf("%s. No policy named %q exists, so nothing was created; re-apply to try again.", err, settings.Name))
			return
		}
		resp.Diagnostics.AddWarning("Lucidity scaling policy created despite an error",
			fmt.Sprintf("Creating %q returned %s, but the policy exists (id %d); it has been adopted into state.", settings.Name, err, found.ID))
		created = &found
	}

	// The create response reports the disk SKU sizes and attached count as
	// 0; read the stored values back.
	stored, err := r.client.GetScalingPolicy(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Lucidity scaling policy after create", fmt.Sprintf("Policy %d was created, but reading it back failed: %s. Run terraform import lucidity_scaling_policy.<name> %d.", created.ID, err, created.ID))
		return
	}
	applyScalingPolicy(&plan, stored)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scalingPolicyResource) handleCreateError(ctx context.Context, diags *diag.Diagnostics, err error, name string) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusConflict {
		detail := fmt.Sprintf("A scaling policy named %q already exists in this Lucidity account (%s). Names must be unique: choose another name, or manage the existing policy with `terraform import`.", name, apiErr.Error())
		if policies, listErr := r.client.ListScalingPolicies(ctx); listErr == nil {
			if existing, ok := client.FindScalingPolicyByName(policies, name); ok {
				detail = fmt.Sprintf("A scaling policy named %q already exists in this Lucidity account (id %d). Choose another name, or manage it here with `terraform import lucidity_scaling_policy.<name> %d`.", name, existing.ID, existing.ID)
			}
		}
		diags.AddError("Scaling policy name already in use", detail)
		return
	}
	diags.AddError("Unable to create Lucidity scaling policy", err.Error())
}

func (r *scalingPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state scalingPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	id, err := parsePolicyID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid scaling policy ID in state", err.Error())
		return
	}

	policy, err := r.client.GetScalingPolicy(ctx, id)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Lucidity scaling policy no longer found",
				fmt.Sprintf("Scaling policy %d no longer exists in this Lucidity account. Removing it from Terraform state; the next apply creates it again if it's still in your configuration.", id))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read Lucidity scaling policy", err.Error())
		return
	}
	applyScalingPolicy(&state, policy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *scalingPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state scalingPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	id, err := parsePolicyID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid scaling policy ID in state", err.Error())
		return
	}

	// PUT replaces every setting and resets omitted ones to 0: always send
	// all seven.
	updated, err := r.client.UpdateScalingPolicy(ctx, id, settingsFromModel(plan))
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusConflict {
			resp.Diagnostics.AddError("Scaling policy name already in use", fmt.Sprintf("Another scaling policy in this Lucidity account is already named %q (%s).", plan.Name.ValueString(), apiErr.Error()))
			return
		}
		resp.Diagnostics.AddError("Unable to update Lucidity scaling policy", err.Error())
		return
	}
	applyScalingPolicy(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scalingPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state scalingPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	id, err := parsePolicyID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid scaling policy ID in state", err.Error())
		return
	}
	if err := r.client.DeleteScalingPolicy(ctx, id); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete Lucidity scaling policy", err.Error())
	}
}

func isNotFound(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusNotFound
}
