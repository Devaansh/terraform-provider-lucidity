package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ resource.Resource                = &tenantResource{}
	_ resource.ResourceWithConfigure   = &tenantResource{}
	_ resource.ResourceWithImportState = &tenantResource{}
)

func newTenantResource() resource.Resource {
	return &tenantResource{}
}

type tenantResource struct {
	client *client.Client
}

// cloudEntityInformationModel mirrors the cloud_entity_information block —
// deliberately a Terraform block, not an attribute, per the maintainer's
// explicit, twice-repeated call to write the reference example "as per
// terraform" with no JSON-like data-structure standing in for real HCL.
type cloudEntityInformationModel struct {
	CloudProvider          types.String `tfsdk:"cloud_provider"`
	CloudProviderAccountID types.String `tfsdk:"cloud_provider_account_id"`
	ExternalID             types.String `tfsdk:"aws_iam_external_id"`
	AWSIAMRoleName         types.String `tfsdk:"aws_iam_role_name"`
	AWSIAMPolicyName       types.String `tfsdk:"aws_iam_policy_name"`
}

type tenantResourceModel struct {
	CloudEntityInformation   cloudEntityInformationModel `tfsdk:"cloud_entity_information"`
	DisplayName              types.String                `tfsdk:"lucidity_dashboard_display_name"`
	ProductList              types.List                  `tfsdk:"lucidity_product_list"`
	AWSRootID                types.String                `tfsdk:"aws_root_account_id"`
	SkipCloudPermissionCheck types.Bool                  `tfsdk:"skip_cloud_permission_check"`
	AccountDeleteProtection  types.Bool                  `tfsdk:"lucidity_dashboard_account_delete_protection"`
	DestroyBehavior          types.String                `tfsdk:"lucidity_account_destroy_behavior"`
	TenantID                 types.String                `tfsdk:"tenant_id"`
	Status                   types.String                `tfsdk:"status"`
	CloudEntityName          types.String                `tfsdk:"cloud_entity_name"`
}

func (r *tenantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant"
}

func (r *tenantResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *tenantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Connects one cloud account to Lucidity as a managed tenant. One resource per cloud account — onboarding is AWS-only today (Azure/GCP are accepted by List/Deboard/Update but not by onboarding). Deboarding is IRREVERSIBLE via API: see lucidity_dashboard_account_delete_protection and lucidity_account_destroy_behavior below before running terraform destroy.",
		Blocks: map[string]schema.Block{
			"cloud_entity_information": schema.SingleNestedBlock{
				Description: "Cloud account identity and access details.",
				Attributes: map[string]schema.Attribute{
					"cloud_provider": schema.StringAttribute{
						Required:    true,
						Description: "Only \"AWS\" is accepted today — onboarding other providers via this API returns 400 INVALID_REQUEST even though List/Deboard/Update already accept AZURE and GCP.",
						Validators: []validator.String{
							stringvalidator.OneOf("AWS"),
						},
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"cloud_provider_account_id": schema.StringAttribute{
						Required:    true,
						Description: "The cloud's own identifier for the account — the AWS account ID. Not a display name.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"aws_iam_external_id": schema.StringAttribute{
						Required: true,
						Description: "A unique ID you generate and put in your IAM role's trust policy (a UUID works). Lucidity sends it on every AssumeRole so your role only trusts requests carrying it. " +
							"Immutable forever once onboarded — Lucidity never returns this value again (write-only) and never applies a changed one via update, so this provider cannot detect drift on it and treats any config change as requiring a full replace (destroy + re-onboard). " +
							"terraform import cannot recover this value; you must supply the real one that matches your IAM role's trust policy, or the very next apply will force a replace.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"aws_iam_role_name": schema.StringAttribute{
						Required:    true,
						Description: "The IAM role name only (e.g. \"LucidityRole\"), not the full ARN — Lucidity builds the ARN for you. Updatable in-place after onboarding via the tenant update API.",
					},
					"aws_iam_policy_name": schema.StringAttribute{
						Required:    true,
						Description: "e.g. \"LucidityPolicy\". Updatable in-place after onboarding via the tenant update API.",
					},
				},
			},
		},
		Attributes: map[string]schema.Attribute{
			"lucidity_dashboard_display_name": schema.StringAttribute{
				Required:    true,
				Description: "The name this tenant shows under in your Lucidity dashboard. Updatable in-place at any time.",
			},
			"aws_root_account_id": schema.StringAttribute{
				Optional: true,
				Description: "The AWS Organization root/management account ID for the account being onboarded. Sent to Lucidity on onboard when set, best-effort — it is NOT part of the documented onboard/update request schema (absent from both field tables in Lucidity's Public Tenant API doc), so Lucidity may silently ignore it, or reject the call outright if it validates request bodies strictly. " +
					"Not used for grouping or resolution: the tenant is always identified purely by cloud_provider + cloud_provider_account_id. Useful for audits and for cases where a shared IAM role/policy is assumed across multiple member accounts under the same org. " +
					"Cannot be modified once set: there is no update mechanism for it (documented or otherwise), so changing this after creation is a plan-time error rather than a silent no-op or a forced replace. A future release may add real update support.",
			},
			"lucidity_product_list": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Lucidity products to enable for this tenant. Only \"AUTOSCALER\" is supported today. Not updatable via any documented API — changing it forces a replace (re-onboard).",
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.ValueStringsAre(stringvalidator.OneOf("AUTOSCALER")),
				},
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"skip_cloud_permission_check": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Only disable permission validation if using a custom permission set or if your permission set is not yet up to date with the latest Lucidity permissions. " +
					"This ignores permission validation entirely — even if the account connects successfully, you may run into permission issues later on. " +
					"Does NOT skip Lucidity's baseline cloud-account-reachability validation: an unreachable/invalid cloud account still fails onboarding regardless of this flag. Onboard-time only; not applied on update.",
			},
			"lucidity_dashboard_account_delete_protection": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				Description: "DEFAULT true. When true, `terraform destroy` (or any change that would replace this resource) hard-errors before making any API call. " +
					"Deboarding on Lucidity is IRREVERSIBLE via API: an INACTIVE tenant can only be reactivated by Lucidity support, and deboarding an account with running services causes immediate disruption. " +
					"Set to false, and also set lucidity_account_destroy_behavior explicitly, before destroying this resource.",
			},
			"lucidity_account_destroy_behavior": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("forget"),
				Description: "Only consulted when lucidity_dashboard_account_delete_protection = false. \"forget\" (default): remove from Terraform state only — the tenant stays ACTIVE on Lucidity and continues to be managed/billed there. " +
					"\"deboard\": actually call the deboard API — the ONLY path to it. This is IRREVERSIBLE via API.",
				Validators: []validator.String{
					stringvalidator.OneOf("forget", "deboard"),
				},
			},
			"tenant_id": schema.StringAttribute{
				Computed:    true,
				Description: "Lucidity's identifier for the onboarded tenant.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "ACTIVE, INACTIVE, or DECOMMISSIONED (Lucidity treats the latter two as synonyms — a single code path). An INACTIVE/DECOMMISSIONED tenant cannot be reactivated via API.",
			},
			"cloud_entity_name": schema.StringAttribute{
				Computed:    true,
				Description: "The provider-side account name, e.g. the AWS account's own name. Only available from listing tenants, never from onboarding — so this is unknown until the first refresh after apply.",
			},
		},
	}
}

func (r *tenantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Expected form: "<cloud_provider>/<cloud_provider_account_id>", e.g.
	// "AWS/123456789012" — matches CLAUDE.md's locked Import design.
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Unexpected import ID format",
			fmt.Sprintf("Expected \"<cloud_provider>/<cloud_provider_account_id>\", e.g. \"AWS/123456789012\", got: %q", req.ID),
		)
		return
	}
	cloudProvider, accountID := parts[0], parts[1]

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cloud_entity_information").AtName("cloud_provider"), cloudProvider)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cloud_entity_information").AtName("cloud_provider_account_id"), accountID)...)
	// Imported resources get lucidity_dashboard_account_delete_protection = true regardless of
	// whatever the eventual config says, until the first apply — per
	// CLAUDE.md's locked Import design — so an import can never be
	// immediately followed by an accidental destroy.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("lucidity_dashboard_account_delete_protection"), true)...)

	resp.Diagnostics.AddWarning(
		"Some lucidity_tenant fields cannot be recovered by import",
		"aws_iam_external_id, aws_root_account_id, lucidity_product_list, and skip_cloud_permission_check are never returned by Lucidity's List API (aws_iam_external_id and aws_root_account_id are write-only; the others simply aren't exposed there), so this import cannot populate them. "+
			"Write a resource block with the real values that match this account's actual configuration. aws_iam_role_name, aws_iam_policy_name, and lucidity_dashboard_display_name will reconcile safely via a normal update on the next apply if they don't match. "+
			"aws_iam_external_id and lucidity_product_list are NOT updatable, though: if the value you write doesn't match reality, the next apply will force a destroy-and-recreate of this tenant (deboarding is IRREVERSIBLE) rather than silently drifting. Review carefully before applying.",
	)
}

func (r *tenantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan tenantResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudProvider := plan.CloudEntityInformation.CloudProvider.ValueString()
	accountID := plan.CloudEntityInformation.CloudProviderAccountID.ValueString()

	// Pre-check kept per CLAUDE.md even though onboard's own 409 CONFLICT
	// now covers both cases natively: this list-and-match stays the primary
	// mechanism, the API's native 409 below is a defense-in-depth backstop
	// for the race window between this check and the actual onboard call.
	tenants, err := r.client.ListTenants(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list existing Lucidity tenants", err.Error())
		return
	}
	if existing, found := client.FindTenant(tenants, cloudProvider, accountID); found {
		addExistingTenantError(&resp.Diagnostics, existing, accountID)
		return
	}

	var productList []string
	resp.Diagnostics.Append(plan.ProductList.ElementsAs(ctx, &productList, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	onboardReq := client.OnboardTenantRequest{
		CloudEntityInformation: client.CloudEntityInformation{
			CloudProvider:          cloudProvider,
			CloudProviderAccountID: accountID,
			ExternalID:             plan.CloudEntityInformation.ExternalID.ValueString(),
			AWSIAMRoleName:         plan.CloudEntityInformation.AWSIAMRoleName.ValueString(),
			AWSIAMPolicyName:       plan.CloudEntityInformation.AWSIAMPolicyName.ValueString(),
			AWSRootID:              plan.AWSRootID.ValueString(),
		},
		DisplayName:              plan.DisplayName.ValueString(),
		SkipCloudPermissionCheck: plan.SkipCloudPermissionCheck.ValueBool(),
		ProductList:              productList,
	}

	if _, err := r.client.OnboardTenant(ctx, onboardReq); err != nil {
		handleOnboardError(&resp.Diagnostics, err, accountID)
		return
	}

	if diags := r.refreshFromList(ctx, cloudProvider, accountID, &plan); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tenantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state tenantResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudProvider := state.CloudEntityInformation.CloudProvider.ValueString()
	accountID := state.CloudEntityInformation.CloudProviderAccountID.ValueString()

	// No GET-by-ID exists — list and match, per CLAUDE.md.
	tenants, err := r.client.ListTenants(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Lucidity tenants", err.Error())
		return
	}

	found, ok := client.FindTenant(tenants, cloudProvider, accountID)
	if !ok {
		resp.Diagnostics.AddWarning(
			"Lucidity tenant no longer found",
			fmt.Sprintf("Cloud account %s is no longer a Lucidity tenant. Removing it from Terraform state; re-add this resource to re-onboard it if that wasn't intentional.", accountID),
		)
		resp.State.RemoveResource(ctx)
		return
	}

	applyListItem(&state, found)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	if found.Status != client.TenantStatusActive {
		resp.Diagnostics.AddError(
			fmt.Sprintf("Lucidity tenant is %s", found.Status),
			fmt.Sprintf(
				"Cloud account %s (tenantId=%s) is %s on Lucidity and cannot be reactivated via API — only Lucidity support can restore it. "+
					"The resource is kept in Terraform state with its current status; run `terraform state rm` on it once you've decided how to proceed (contact Lucidity support, or stop tracking it).",
				accountID, found.TenantID, found.Status,
			),
		)
	}
}

func (r *tenantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state tenantResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudProvider := plan.CloudEntityInformation.CloudProvider.ValueString()
	accountID := plan.CloudEntityInformation.CloudProviderAccountID.ValueString()

	// aws_root_account_id has no update path — documented or otherwise — and no
	// server-side value to reconcile against (it's not part of the real API
	// payload's response either). RequiresReplace would force a full
	// deboard/re-onboard cycle just to change a local metadata note, which
	// is disproportionate given deboarding is irreversible — reject
	// explicitly instead of silently dropping the change or forcing replace.
	if !plan.AWSRootID.Equal(state.AWSRootID) {
		resp.Diagnostics.AddAttributeError(
			path.Root("aws_root_account_id"),
			"aws_root_account_id cannot be modified",
			"Changing aws_root_account_id after onboarding is not supported in this release — Lucidity has no update mechanism for it. "+
				"Revert it to its current value. If it truly must change, that requires destroying and re-creating this resource "+
				"(mind lucidity_dashboard_account_delete_protection and lucidity_account_destroy_behavior — deboarding is irreversible). A future release may add real update support.",
		)
		return
	}

	updateReq := client.UpdateTenantRequest{
		CloudEntityInformation: client.CloudEntityInformation{
			CloudProvider:          cloudProvider,
			CloudProviderAccountID: accountID,
		},
	}
	changed := false
	if !plan.DisplayName.Equal(state.DisplayName) {
		updateReq.DisplayName = plan.DisplayName.ValueString()
		changed = true
	}
	if !plan.CloudEntityInformation.AWSIAMRoleName.Equal(state.CloudEntityInformation.AWSIAMRoleName) {
		updateReq.CloudEntityInformation.AWSIAMRoleName = plan.CloudEntityInformation.AWSIAMRoleName.ValueString()
		changed = true
	}
	if !plan.CloudEntityInformation.AWSIAMPolicyName.Equal(state.CloudEntityInformation.AWSIAMPolicyName) {
		updateReq.CloudEntityInformation.AWSIAMPolicyName = plan.CloudEntityInformation.AWSIAMPolicyName.ValueString()
		changed = true
	}

	if changed {
		if _, err := r.client.UpdateTenant(ctx, updateReq); err != nil {
			handleUpdateError(&resp.Diagnostics, err, accountID)
			return
		}
	}
	// skip_cloud_permission_check, lucidity_dashboard_account_delete_protection, and
	// lucidity_account_destroy_behavior are local-only / onboard-only — no API call needed;
	// the planned value is simply carried into state below.

	if diags := r.refreshFromList(ctx, cloudProvider, accountID, &plan); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tenantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state tenantResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cloudProvider := state.CloudEntityInformation.CloudProvider.ValueString()
	accountID := state.CloudEntityInformation.CloudProviderAccountID.ValueString()
	protected := state.AccountDeleteProtection.IsNull() || state.AccountDeleteProtection.ValueBool()

	if protected {
		resp.Diagnostics.AddError(
			"Destroy blocked by lucidity_dashboard_account_delete_protection",
			fmt.Sprintf(
				"Refusing to destroy lucidity_tenant for cloud account %s: lucidity_dashboard_account_delete_protection is true (the default). "+
					"Deboarding on Lucidity is IRREVERSIBLE via API — an INACTIVE tenant can only be reactivated by Lucidity support, and deboarding "+
					"an account with running services causes immediate disruption. Set lucidity_dashboard_account_delete_protection = false and choose lucidity_account_destroy_behavior "+
					"explicitly to proceed.",
				accountID,
			),
		)
		return
	}

	if state.DestroyBehavior.ValueString() != "deboard" {
		resp.Diagnostics.AddWarning(
			"Lucidity tenant left ACTIVE on Lucidity",
			fmt.Sprintf(
				"Removed cloud account %s from Terraform state without deboarding it — it remains ACTIVE on Lucidity and continues to be managed/billed there. "+
					"Set lucidity_account_destroy_behavior = \"deboard\" (with lucidity_dashboard_account_delete_protection = false) if you actually want it deboarded.",
				accountID,
			),
		)
		return
	}

	result, err := r.client.DeboardTenant(ctx, cloudProvider, accountID)
	if err != nil {
		handleDeboardError(&resp.Diagnostics, err, accountID)
		return
	}

	if result.Status == client.DeboardStatusAlreadyDeBoarded {
		resp.Diagnostics.AddWarning(
			"Lucidity tenant was already deboarded",
			fmt.Sprintf("Cloud account %s was already INACTIVE on Lucidity; removing it from Terraform state.", accountID),
		)
		return
	}
	resp.Diagnostics.AddWarning(
		"Lucidity tenant deboarded — IRREVERSIBLE",
		fmt.Sprintf("Cloud account %s has been deboarded (marked INACTIVE) on Lucidity. This cannot be undone via API — only Lucidity support can restore it.", accountID),
	)
}

// refreshFromList re-lists and populates model with the matching tenant's
// server-side fields. Shared by Create and Update because cloud_entity_name
// is only ever available from List, never from onboard/update responses,
// and this keeps one source of truth for "what does the server say now."
func (r *tenantResource) refreshFromList(ctx context.Context, cloudProvider, accountID string, model *tenantResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	tenants, err := r.client.ListTenants(ctx)
	if err != nil {
		diags.AddError("Unable to list Lucidity tenants", err.Error())
		return diags
	}
	found, ok := client.FindTenant(tenants, cloudProvider, accountID)
	if !ok {
		diags.AddError(
			"Lucidity tenant not found immediately after apply",
			fmt.Sprintf("Cloud account %s was not found in a follow-up list call right after the API reported success. This may be a brief propagation delay — retry the apply, or check the Lucidity dashboard directly.", accountID),
		)
		return diags
	}
	applyListItem(model, found)
	return diags
}

func applyListItem(model *tenantResourceModel, item client.TenantListItem) {
	model.TenantID = types.StringValue(item.TenantID)
	model.Status = types.StringValue(item.Status)
	model.CloudEntityName = types.StringValue(item.CloudEntityName)
	model.DisplayName = types.StringValue(item.DisplayName)
	model.CloudEntityInformation.CloudProvider = types.StringValue(item.CloudProvider)
	model.CloudEntityInformation.CloudProviderAccountID = types.StringValue(item.CloudProviderAccountID)
}

// addExistingTenantError distinguishes the two pre-check outcomes per
// CLAUDE.md: an ACTIVE duplicate is a config error (most likely a for_each/
// key collision), while an INACTIVE/DECOMMISSIONED one needs the
// support-contact framing — re-onboarding is not possible via API.
func addExistingTenantError(diags *diag.Diagnostics, existing client.TenantListItem, accountID string) {
	if existing.Status == client.TenantStatusActive {
		diags.AddError(
			"Tenant already exists",
			fmt.Sprintf(
				"A tenant already exists for cloud account %s (tenantId=%s) and is ACTIVE. This is a configuration error — most likely a for_each/key collision — not a Lucidity-support situation.",
				accountID, existing.TenantID,
			),
		)
		return
	}
	diags.AddError(
		"Cloud account was previously deboarded",
		fmt.Sprintf(
			"cloud account %s was previously deboarded and is INACTIVE on Lucidity. Once a tenant is made inactive it cannot be made active again — "+
				"re-onboarding via API is not possible. Contact Lucidity support to restore this account.",
			accountID,
		),
	)
}

func handleOnboardError(diags *diag.Diagnostics, err error, accountID string) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "CONFLICT":
			// Defense-in-depth backstop for the race window between the
			// pre-check above and this call — same two variants, now coming
			// straight from the API instead of our own list-and-match.
			if strings.Contains(strings.ToLower(apiErr.Message), "inactive") {
				diags.AddError(
					"Cloud account was previously deboarded",
					fmt.Sprintf(
						"cloud account %s was previously deboarded and is INACTIVE on Lucidity (requestId=%s). Once a tenant is made inactive it cannot be made active again — "+
							"re-onboarding via API is not possible. Contact Lucidity support to restore this account.",
						accountID, apiErr.RequestID,
					),
				)
				return
			}
			diags.AddError(
				"Tenant already exists",
				fmt.Sprintf("A tenant already exists for cloud account %s (requestId=%s). This is a configuration error — most likely a for_each/key collision.", accountID, apiErr.RequestID),
			)
			return
		case "UNAUTHORIZED":
			if strings.Contains(strings.ToLower(apiErr.Message), "cloud account could not be validated") {
				diags.AddError(
					"Cloud account could not be validated",
					fmt.Sprintf(
						"Lucidity could not validate cloud account %s (requestId=%s): %s. Confirm the IAM role/policy and aws_iam_external_id are set up correctly in the target account's trust policy. "+
							"skip_cloud_permission_check only bypasses Lucidity's permission check — it does NOT bypass this baseline reachability validation.",
						accountID, apiErr.RequestID, apiErr.Message,
					),
				)
				return
			}
		}
		diags.AddError("Lucidity API error onboarding tenant", apiErr.Error())
		return
	}
	diags.AddError("Unable to onboard Lucidity tenant", err.Error())
}

func handleUpdateError(diags *diag.Diagnostics, err error, accountID string) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "NOT_FOUND":
			diags.AddError(
				"Lucidity tenant not found",
				fmt.Sprintf("No tenant exists for cloud account %s (requestId=%s). It may have been deboarded or removed outside Terraform — run `terraform plan` to refresh state.", accountID, apiErr.RequestID),
			)
			return
		case "CONFLICT":
			diags.AddError(
				"Lucidity tenant is not ACTIVE",
				fmt.Sprintf("The tenant for cloud account %s is not ACTIVE and cannot be updated (requestId=%s): %s", accountID, apiErr.RequestID, apiErr.Message),
			)
			return
		}
		diags.AddError("Lucidity API error updating tenant", apiErr.Error())
		return
	}
	diags.AddError("Unable to update Lucidity tenant", err.Error())
}

func handleDeboardError(diags *diag.Diagnostics, err error, accountID string) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code == "NOT_FOUND" {
			diags.AddWarning(
				"Lucidity tenant already gone",
				fmt.Sprintf("No tenant exists for cloud account %s (requestId=%s) — nothing to deboard; treating it as already removed.", accountID, apiErr.RequestID),
			)
			return
		}
		diags.AddError("Lucidity API error deboarding tenant", apiErr.Error())
		return
	}
	diags.AddError("Unable to deboard Lucidity tenant", err.Error())
}
