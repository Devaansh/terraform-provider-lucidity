package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                   = &serviceAccountCredentialResource{}
	_ resource.ResourceWithConfigure      = &serviceAccountCredentialResource{}
	_ resource.ResourceWithImportState    = &serviceAccountCredentialResource{}
	_ resource.ResourceWithValidateConfig = &serviceAccountCredentialResource{}
)

func newServiceAccountCredentialResource() resource.Resource {
	return &serviceAccountCredentialResource{}
}

type serviceAccountCredentialResource struct {
	client *client.Client
	now    func() time.Time // replaced in tests
}

type serviceAccountCredentialModel struct {
	ID               types.String `tfsdk:"id"`
	ServiceAccountID types.String `tfsdk:"service_account_id"`
	Name             types.String `tfsdk:"name"`
	ExpiresAt        types.String `tfsdk:"expires_at"`
	TokenID          types.String `tfsdk:"token_id"`
	ClientID         types.String `tfsdk:"client_id"`
	ClientSecret     types.String `tfsdk:"client_secret"`
	CreatedAt        types.String `tfsdk:"created_at"`
	TenantID         types.String `tfsdk:"tenant_id"`
}

func (r *serviceAccountCredentialResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account_credential"
}

func (r *serviceAccountCredentialResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceAccountCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			Computed:      true,
			Description:   desc,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A client ID and secret for a lucidity_service_account, which a pipeline exchanges for access tokens. " +
			"The secret is stored in Terraform state (marked sensitive): encrypt your state, and pass the secret straight to your secrets manager rather than outputting it. " +
			"Credentials can't be changed, so any change creates a new one and revokes the old. " +
			"When a credential expires or is revoked outside Terraform, the next refresh removes it and the next apply creates a new one (with a new secret).",
		Attributes: map[string]schema.Attribute{
			"id": computed("\"<service_account_id>/<token_id>\". Also the import ID."),
			"service_account_id": schema.StringAttribute{
				Required:      true,
				Description:   "ID of the service account (lucidity_service_account.<name>.id).",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Name for the credential, e.g. where it's used.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"expires_at": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "When the credential stops working, as an RFC 3339 timestamp (e.g. `2027-01-04T00:00:00Z`); must be in the future. Defaults to 90 days after creation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tenant_id":  tenantIDResourceAttribute(),
			"token_id":   computed("Lucidity's ID for the credential."),
			"client_id":  computed("The client ID, for the provider's client_id setting or LUCIDITY_CLIENT_ID."),
			"created_at": computed("When the credential was created (RFC 3339)."),
			"client_secret": schema.StringAttribute{
				Computed:      true,
				Sensitive:     true,
				Description:   "The client secret. Lucidity shows it only when the credential is created, so it's empty after `terraform import`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serviceAccountCredentialResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var expires types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("expires_at"), &expires)...)
	if expires.IsNull() || expires.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, expires.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Invalid expires_at",
			fmt.Sprintf("expires_at must be an RFC 3339 timestamp such as 2027-01-04T00:00:00Z, got %q.", expires.ValueString()))
	}
}

func (r *serviceAccountCredentialResource) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *serviceAccountCredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	saID, tokenID, ok := strings.Cut(req.ID, "/")
	if !ok || saID == "" || tokenID == "" || strings.Contains(tokenID, "/") {
		resp.Diagnostics.AddError("Unexpected import ID format",
			fmt.Sprintf("Expected \"<service_account_id>/<token_id>\", got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_account_id"), saID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("token_id"), tokenID)...)
	resp.Diagnostics.AddWarning("Client secret not imported",
		"Lucidity shows a credential's secret only when it's created, so client_secret is empty after import. "+
			"If something in your configuration needs the secret, replace the credential instead (terraform apply -replace=...).")
}

func (r *serviceAccountCredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, plan.TenantID)
	saID, name := plan.ServiceAccountID.ValueString(), plan.Name.ValueString()
	expiresAt := ""
	if !plan.ExpiresAt.IsUnknown() && !plan.ExpiresAt.IsNull() {
		expiresAt = plan.ExpiresAt.ValueString()
	}

	// The API answers both of these with a 500, which would otherwise read
	// as "the credential may have been created". Check them first.
	if expiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, expiresAt); err == nil && !exp.After(r.clock()) {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "expires_at is in the past",
				fmt.Sprintf("expires_at (%s) must be in the future.", expiresAt))
			return
		}
	}
	sa, err := r.client.GetServiceAccount(ctx, saID)
	if err != nil {
		serviceAccountError(&resp.Diagnostics, "Unable to read the Lucidity service account before creating a credential", err)
		return
	}
	if !sa.Enabled {
		resp.Diagnostics.AddError("Service account is disabled",
			fmt.Sprintf("Service account %s (%s) is disabled, and Lucidity doesn't create credentials for a disabled service account. Set enabled = true on it first.", saID, sa.Name))
		return
	}

	cred, err := r.client.CreateServiceAccountCredential(ctx, saID, name, expiresAt)
	if err != nil {
		if client.IsOutcomeUnknown(ctx, err) {
			// The credential may exist, but its secret is gone for good, so
			// adopting it would be useless. Name the candidates so they can
			// be revoked; nothing is revoked automatically, because names
			// aren't unique and a real credential could match.
			detail := fmt.Sprintf("%s. The credential may have been created anyway; its secret can't be recovered.", err)
			if creds, listErr := r.client.ListServiceAccountCredentials(ctx, saID); listErr == nil {
				var ids []string
				for _, c := range creds {
					if c.Name == name && c.Active {
						ids = append(ids, c.TokenID)
					}
				}
				if len(ids) > 0 {
					detail += fmt.Sprintf(" Active credentials named %q on this service account: %s. Revoke any you didn't create on purpose in the Lucidity dashboard, then re-apply.", name, strings.Join(ids, ", "))
				}
			}
			resp.Diagnostics.AddError("Unable to create Lucidity service account credential", detail)
			return
		}
		serviceAccountError(&resp.Diagnostics, "Unable to create Lucidity service account credential", err)
		return
	}

	plan.ID = types.StringValue(saID + "/" + cred.TokenID)
	plan.TokenID = types.StringValue(cred.TokenID)
	plan.ClientID = types.StringValue(cred.ClientID)
	plan.CreatedAt = types.StringValue(cred.CreatedAt)
	plan.ClientSecret = types.StringNull()
	if cred.ClientSecret != nil {
		plan.ClientSecret = types.StringValue(*cred.ClientSecret)
	}
	if expiresAt == "" {
		plan.ExpiresAt = types.StringValue(cred.ExpiresAt)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if cred.ClientSecret == nil || *cred.ClientSecret == "" {
		resp.Diagnostics.AddWarning("Lucidity returned no client secret",
			fmt.Sprintf("Credential %s was created but the response had no client secret. Revoke it in the Lucidity dashboard and re-apply.", cred.TokenID))
	}
}

// Read finds the credential in the service account's list (there's no
// get-one endpoint). Gone, revoked or expired: it's dropped from state, so
// the next apply creates a replacement. That makes rotation "run apply",
// and anything fed client_secret in the same configuration follows along.
func (r *serviceAccountCredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withTenant(ctx, state.TenantID)
	saID, tokenID := state.ServiceAccountID.ValueString(), state.TokenID.ValueString()

	creds, err := r.client.ListServiceAccountCredentials(ctx, saID)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Service account no longer found",
				fmt.Sprintf("Service account %s no longer exists, so credential %s is gone too. Removing it from Terraform state.", saID, tokenID))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read Lucidity service account credentials", err.Error())
		return
	}
	var found *client.ServiceAccountCredential
	for i := range creds {
		if creds[i].TokenID == tokenID {
			found = &creds[i]
			break
		}
	}

	var gone string
	switch {
	case found == nil:
		gone = "no longer exists"
	case !found.Active:
		gone = "is no longer active (revoked or expired)"
	default:
		if exp, err := time.Parse(time.RFC3339, found.ExpiresAt); err == nil && !exp.After(r.clock()) {
			gone = "expired at " + found.ExpiresAt
		}
	}
	if gone != "" {
		resp.Diagnostics.AddWarning("Service account credential no longer usable",
			fmt.Sprintf("Credential %s (%s) on service account %s %s. Removing it from Terraform state; the next apply creates a new credential with a new secret if it's still in your configuration.", tokenID, state.Name.ValueString(), saID, gone))
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(saID + "/" + tokenID)
	state.Name = types.StringValue(found.Name)
	state.ClientID = types.StringValue(found.ClientID)
	state.CreatedAt = types.StringValue(found.CreatedAt)
	// Keep the configured spelling when it's the same instant (the API may
	// format it differently), so it doesn't force a replacement.
	if !sameInstant(state.ExpiresAt.ValueString(), found.ExpiresAt) {
		state.ExpiresAt = types.StringValue(found.ExpiresAt)
	}
	if state.ClientSecret.IsUnknown() {
		state.ClientSecret = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func sameInstant(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ta.Equal(tb)
}

// Update never runs: every configurable attribute forces replacement.
func (r *serviceAccountCredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serviceAccountCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete revokes the credential: anything still using it stops being able
// to get access tokens.
func (r *serviceAccountCredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RevokeServiceAccountCredential(withTenant(ctx, state.TenantID), state.ServiceAccountID.ValueString(), state.TokenID.ValueString())
	if err != nil && !isNotFound(err) {
		serviceAccountError(&resp.Diagnostics, "Unable to revoke Lucidity service account credential", err)
	}
}
