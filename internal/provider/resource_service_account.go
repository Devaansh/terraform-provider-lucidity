package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ resource.Resource                = &serviceAccountResource{}
	_ resource.ResourceWithConfigure   = &serviceAccountResource{}
	_ resource.ResourceWithImportState = &serviceAccountResource{}
)

func newServiceAccountResource() resource.Resource {
	return &serviceAccountResource{}
}

type serviceAccountResource struct {
	client *client.Client
}

type serviceAccountModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Permissions types.Map    `tfsdk:"permissions"`
	CreatedAt   types.String `tfsdk:"created_at"`
	TenantID    types.String `tfsdk:"tenant_id"`
}

var rolesSetType = types.SetType{ElemType: types.StringType}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Lucidity service account: a non-human identity for scripts and pipelines, with roles per tenant. " +
			"Create its client ID and secret with lucidity_service_account_credential. " +
			"Managing service accounts needs an account admin's refresh token: a service account's own credentials can only read. " +
			"Deleting a service account is permanent: its credentials stop working and its name can't be used again. To pause one, set enabled = false.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Lucidity's ID for the service account. Also the import ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name, up to 255 characters, unique in the Lucidity account ignoring case. Updatable in place. A deleted service account's name can't be reused.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "What the service account is for, up to 1000 characters.",
				Validators:  []validator.String{stringvalidator.LengthAtMost(1000)},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Defaults to true. false stops new access tokens without deleting the service account or its credentials; tokens already issued keep working until they expire (up to 15 minutes). Credentials can't be created while it's false.",
			},
			"permissions": schema.MapAttribute{
				Optional:    true,
				ElementType: rolesSetType,
				Description: "Roles per tenant: keys are Lucidity tenant IDs (e.g. lucidity_tenant.<name>.tenant_id), values are role names (`Viewer`, `Editor`; see the lucidity_roles data source). " +
					"This is the complete set: a tenant left out has no access. Leave unset for a service account with no tenant roles; removing it from an existing service account switches its role mapping off (Lucidity keeps the roles stored, but they grant nothing).",
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.ValueSetsAre(setvalidator.SizeAtLeast(1)),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "When the service account was created (RFC 3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"tenant_id": tenantIDResourceAttribute(),
		},
	}
}

// permissionsFromModel converts the permissions attribute to the API's map.
// Null means no tenant roles.
func permissionsFromModel(ctx context.Context, m types.Map) (map[string][]string, diag.Diagnostics) {
	out := map[string][]string{}
	if m.IsNull() || m.IsUnknown() {
		return out, nil
	}
	var sets map[string]types.Set
	diags := m.ElementsAs(ctx, &sets, false)
	for tenant, set := range sets {
		var roles []string
		diags.Append(set.ElementsAs(ctx, &roles, false)...)
		sort.Strings(roles)
		out[tenant] = roles
	}
	return out, diags
}

// permissionsToModel converts the API's map to the attribute. An empty map
// becomes null, matching a configuration that leaves permissions unset.
//
// Role names match case-insensitively in the API. When a tenant's roles
// equal the prior value ignoring case, the prior spelling is kept, so
// writing "viewer" doesn't show a diff against a returned "Viewer".
func permissionsToModel(ctx context.Context, api map[string][]string, prior types.Map) (types.Map, diag.Diagnostics) {
	if len(api) == 0 {
		return types.MapNull(rolesSetType), nil
	}
	priorRoles, diags := permissionsFromModel(ctx, prior)
	elems := map[string][]string{}
	for tenant, roles := range api {
		if p, ok := priorRoles[tenant]; ok && sameRolesIgnoringCase(p, roles) {
			elems[tenant] = p
			continue
		}
		elems[tenant] = roles
	}
	m, d := types.MapValueFrom(ctx, rolesSetType, elems)
	diags.Append(d...)
	return m, diags
}

func sameRolesIgnoringCase(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(s []string) []string {
		out := make([]string, len(s))
		for i, v := range s {
			out[i] = strings.ToLower(v)
		}
		sort.Strings(out)
		return out
	}
	na, nb := norm(a), norm(b)
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// applyServiceAccount copies the API's view into the model. Description ""
// stays null when the configuration leaves it unset.
func applyServiceAccount(m *serviceAccountModel, sa *client.ServiceAccount) {
	m.ID = types.StringValue(sa.ID)
	m.Name = types.StringValue(sa.Name)
	if sa.Description != "" || !m.Description.IsNull() {
		m.Description = types.StringValue(sa.Description)
	}
	m.Enabled = types.BoolValue(sa.Enabled)
	m.CreatedAt = types.StringValue(sa.CreatedAt)
}

// serviceAccountError adds the admin-token hint to a 403: the most likely
// cause is running the provider with a service account's own credentials.
func serviceAccountError(diags *diag.Diagnostics, summary string, err error) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusForbidden {
		diags.AddError(summary, fmt.Sprintf("%s. Only account admins can create or change service accounts and their credentials: run the provider with an admin user's refresh token, not a service account's client credentials.", apiErr.Error()))
		return
	}
	diags.AddError(summary, err.Error())
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	name := plan.Name.ValueString()
	tenantRoles, diags := permissionsFromModel(ctx, plan.Permissions)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check the name first. The API answers a duplicate name with a 500,
	// the same status as a lost response, so after the POST the two can't
	// be told apart: without this check an error could "adopt" someone
	// else's existing service account and overwrite its roles.
	accounts, err := r.client.ListServiceAccounts(ctx)
	if err != nil {
		serviceAccountError(&resp.Diagnostics, "Unable to check existing Lucidity service accounts", err)
		return
	}
	if existing, found := client.FindServiceAccountByName(accounts, name); found {
		resp.Diagnostics.AddError("Service account name already in use",
			fmt.Sprintf("A service account named %q already exists in this Lucidity account (id %s). Names are unique ignoring case. Choose another name, or manage it here with `terraform import lucidity_service_account.<name> %s`.", existing.Name, existing.ID, existing.ID))
		return
	}

	sa, err := r.client.CreateServiceAccount(ctx, name, plan.Description.ValueString())
	if err != nil {
		if !client.IsOutcomeUnknown(ctx, err) {
			serviceAccountError(&resp.Diagnostics, "Unable to create Lucidity service account", err)
			return
		}
		existing, found := r.findByName(ctx, name)
		if !found {
			resp.Diagnostics.AddError("Unable to create Lucidity service account",
				fmt.Sprintf("%s. No service account named %q exists, so nothing was created. Known causes of a 500 here: the name belonged to a deleted service account (names stay reserved after deletion, so choose another), or the token's user isn't a member of this Lucidity account.", err, name))
			return
		}
		// The name wasn't there before the POST and is now: this request
		// created it and the response was lost. Adopt it rather than fail
		// (a retry would fail on the name, which can never be reused).
		resp.Diagnostics.AddWarning("Lucidity service account created despite an error",
			fmt.Sprintf("Creating %q returned %s, but the service account exists (id %s); it has been adopted into state.", name, err, existing.ID))
		sa = &existing
	}

	// From here on the service account exists: save it to state first, so a
	// failure below leaves it tracked (a re-apply fixes the rest) instead
	// of orphaning an account whose name can never be reused.
	created := plan
	applyServiceAccount(&created, sa)
	created.Permissions = types.MapNull(rolesSetType)
	created.Enabled = types.BoolValue(sa.Enabled)
	resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wantEnabled := plan.Enabled.ValueBool()
	if sa.Enabled != wantEnabled || (sa.Description != plan.Description.ValueString() && !plan.Description.IsNull()) {
		desc := plan.Description.ValueString()
		updated, err := r.client.UpdateServiceAccount(ctx, sa.ID, client.ServiceAccountUpdate{Description: &desc, Enabled: &wantEnabled})
		if err != nil {
			serviceAccountError(&resp.Diagnostics, "Service account created, but setting enabled/description failed", err)
			return
		}
		applyServiceAccount(&created, updated)
	}

	if len(tenantRoles) > 0 {
		_, err := r.client.ReplaceServiceAccountPermissions(ctx, sa.ID, tenantRoles)
		if err != nil {
			resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
			serviceAccountError(&resp.Diagnostics, "Service account created, but setting its tenant roles failed", err)
			return
		}
	}
	keepPlanned(&created, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
}

// keepPlanned stores the configured values after a successful Create or
// Update, as the rest of this provider does: Terraform requires the applied
// state to match the plan, and the API may echo e.g. role names in another
// case. Read adopts the remote values on the next refresh, where
// permissionsToModel ignores case-only differences.
func keepPlanned(m *serviceAccountModel, plan serviceAccountModel) {
	m.Name = plan.Name
	m.Description = plan.Description
	m.Enabled = plan.Enabled
	m.Permissions = plan.Permissions
	m.TenantID = plan.TenantID
}

func (r *serviceAccountResource) findByName(ctx context.Context, name string) (client.ServiceAccount, bool) {
	accounts, err := r.client.ListServiceAccounts(ctx)
	if err != nil {
		return client.ServiceAccount{}, false
	}
	return client.FindServiceAccountByName(accounts, name)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	id := state.ID.ValueString()

	sa, err := r.client.GetServiceAccount(ctx, id)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Lucidity service account no longer found",
				fmt.Sprintf("Service account %s (%s) no longer exists in this Lucidity account. Removing it from Terraform state. Its name can't be reused, so if it's still in your configuration, give it a new name before the next apply.", id, state.Name.ValueString()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read Lucidity service account", err.Error())
		return
	}
	perms, err := r.client.GetServiceAccountPermissions(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Lucidity service account permissions", err.Error())
		return
	}

	applyServiceAccount(&state, sa)
	roles := perms.TenantRoleMap
	if !perms.Enabled && len(roles) > 0 {
		// Switched off means the roles grant nothing: show them as none.
		// That's also how this provider removes every role, so only warn
		// when state still expected roles (switched off outside Terraform).
		if !state.Permissions.IsNull() {
			resp.Diagnostics.AddWarning("Service account roles switched off",
				fmt.Sprintf("Service account %s has tenant roles, but its role mapping is switched off in Lucidity, so they grant nothing. Treating it as having no roles; if your configuration sets permissions, the next apply switches the mapping back on.", id))
		}
		roles = nil
	}
	var diags diag.Diagnostics
	state.Permissions, diags = permissionsToModel(ctx, roles, state.Permissions)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	id := state.ID.ValueString()

	var u client.ServiceAccountUpdate
	if !plan.Name.Equal(state.Name) {
		n := plan.Name.ValueString()
		// The API answers a taken name with a 500; check first so the
		// error says what's wrong.
		if other, found := r.findByName(ctx, n); found && other.ID != id {
			resp.Diagnostics.AddError("Service account name already in use",
				fmt.Sprintf("Can't rename to %q: service account %s already has that name (names are unique ignoring case).", n, other.ID))
			return
		}
		u.Name = &n
	}
	if !plan.Description.Equal(state.Description) {
		d := plan.Description.ValueString() // unset sends "", clearing it
		u.Description = &d
	}
	if !plan.Enabled.Equal(state.Enabled) {
		e := plan.Enabled.ValueBool()
		u.Enabled = &e
	}

	updated := state
	if u.Name != nil || u.Description != nil || u.Enabled != nil {
		sa, err := r.client.UpdateServiceAccount(ctx, id, u)
		if err != nil {
			serviceAccountError(&resp.Diagnostics, "Unable to update Lucidity service account", err)
			return
		}
		applyServiceAccount(&updated, sa)
		updated.Description = plan.Description
	}

	if !plan.Permissions.Equal(state.Permissions) {
		var err error
		if plan.Permissions.IsNull() {
			// Removing every role: switch the role mapping off rather than
			// send an empty map, which Lucidity may reject. The roles stay
			// stored but grant nothing; Read shows them as none.
			current, diags := permissionsFromModel(ctx, state.Permissions)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			_, err = r.client.SwitchOffServiceAccountPermissions(ctx, id, current)
		} else {
			tenantRoles, diags := permissionsFromModel(ctx, plan.Permissions)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			_, err = r.client.ReplaceServiceAccountPermissions(ctx, id, tenantRoles)
		}
		if err != nil {
			resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
			serviceAccountError(&resp.Diagnostics, "Unable to update Lucidity service account permissions", err)
			return
		}
	}
	keepPlanned(&updated, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

// Delete permanently deletes the service account. This can't be undone:
// every credential it has stops working at once, and its name can never be
// used again. Pipelines that use it break immediately. To pause a service
// account instead, set enabled = false. Terraform's lifecycle
// { prevent_destroy = true } guards against deleting it by accident.
func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteServiceAccount(withTenant(ctx, state.TenantID), state.ID.ValueString()); err != nil && !isNotFound(err) {
		serviceAccountError(&resp.Diagnostics, "Unable to delete Lucidity service account", err)
	}
}
