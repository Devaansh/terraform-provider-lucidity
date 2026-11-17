package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ datasource.DataSource              = &rolesDataSource{}
	_ datasource.DataSourceWithConfigure = &rolesDataSource{}
)

func newRolesDataSource() datasource.DataSource {
	return &rolesDataSource{}
}

type rolesDataSource struct {
	client *client.Client
}

type rolesDataSourceModel struct {
	Roles []roleModel `tfsdk:"roles"`
}

type roleModel struct {
	Name           string            `tfsdk:"name"`
	NewUserDefault bool              `tfsdk:"new_user_default"`
	Permissions    []permissionModel `tfsdk:"permissions"`
}

type permissionModel struct {
	Resource string `tfsdk:"resource"`
	Action   string `tfsdk:"action"`
}

func (d *rolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_roles"
}

func (d *rolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *rolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the roles users can be granted on the caller's Lucidity account. Account admin is not a role: it bypasses role checks entirely.",
		Attributes: map[string]schema.Attribute{
			"roles": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Every role on the account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":             schema.StringAttribute{Computed: true, Description: "Role name; identifies the role (there is no role ID)."},
						"new_user_default": schema.BoolAttribute{Computed: true, Description: "Whether new users get this role by default."},
						"permissions": schema.ListNestedAttribute{
							Computed:    true,
							Description: "What the role allows.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"resource": schema.StringAttribute{Computed: true, Description: "Resource type, e.g. `volume`."},
									"action":   schema.StringAttribute{Computed: true, Description: "Action, e.g. `READ` or `WRITE`."},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *rolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roles, err := d.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Lucidity roles", err.Error())
		return
	}
	state := rolesDataSourceModel{Roles: make([]roleModel, 0, len(roles))}
	for _, r := range roles {
		m := roleModel{Name: r.Name, NewUserDefault: r.NewUserDefault, Permissions: make([]permissionModel, 0, len(r.Permissions))}
		for _, p := range r.Permissions {
			m.Permissions = append(m.Permissions, permissionModel{Resource: p.Resource, Action: p.Action})
		}
		state.Roles = append(state.Roles, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
