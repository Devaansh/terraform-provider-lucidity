package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ datasource.DataSource              = &serviceAccountsDataSource{}
	_ datasource.DataSourceWithConfigure = &serviceAccountsDataSource{}
)

func newServiceAccountsDataSource() datasource.DataSource {
	return &serviceAccountsDataSource{}
}

type serviceAccountsDataSource struct {
	client *client.Client
}

type serviceAccountsDataSourceModel struct {
	TenantID        types.String             `tfsdk:"tenant_id"`
	ServiceAccounts []serviceAccountListItem `tfsdk:"service_accounts"`
}

type serviceAccountListItem struct {
	ID          string `tfsdk:"id"`
	Name        string `tfsdk:"name"`
	Description string `tfsdk:"description"`
	Enabled     bool   `tfsdk:"enabled"`
	CreatedAt   string `tfsdk:"created_at"`
}

func (d *serviceAccountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_accounts"
}

func (d *serviceAccountsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	clients, ok := req.ProviderData.(*LucidityClients)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *provider.LucidityClients, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	d.client = clients.Client
}

func (d *serviceAccountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Lists every service account in the caller's Lucidity account. Works with a service account's own credentials too (reading is allowed).",
		Attributes: map[string]schema.Attribute{
			"tenant_id": tenantIDDataSourceAttribute(),
			"service_accounts": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Every service account in the account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          str("Lucidity's ID for the service account."),
						"name":        str("Name."),
						"description": str("Description."),
						"enabled":     schema.BoolAttribute{Computed: true, Description: "Whether it can get new access tokens."},
						"created_at":  str("When it was created (RFC 3339)."),
					},
				},
			},
		},
	}
}

func (d *serviceAccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var tenantID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("tenant_id"), &tenantID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	accounts, err := d.client.ListServiceAccounts(withTenant(ctx, tenantID))
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Lucidity service accounts", err.Error())
		return
	}
	state := serviceAccountsDataSourceModel{TenantID: tenantID, ServiceAccounts: make([]serviceAccountListItem, 0, len(accounts))}
	for _, a := range accounts {
		state.ServiceAccounts = append(state.ServiceAccounts, serviceAccountListItem{ID: a.ID, Name: a.Name, Description: a.Description, Enabled: a.Enabled, CreatedAt: a.CreatedAt})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
