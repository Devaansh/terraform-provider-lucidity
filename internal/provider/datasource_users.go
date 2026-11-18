package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

var (
	_ datasource.DataSource              = &usersDataSource{}
	_ datasource.DataSourceWithConfigure = &usersDataSource{}
)

func newUsersDataSource() datasource.DataSource {
	return &usersDataSource{}
}

type usersDataSource struct {
	client *client.Client
}

type usersDataSourceModel struct {
	Email  types.String `tfsdk:"email"`
	Role   types.String `tfsdk:"role"`
	Status types.String `tfsdk:"status"`
	Users  []userModel  `tfsdk:"users"`
	// TenantID is sent as X-tenant (see tenant_id.go).
	TenantID types.String `tfsdk:"tenant_id"`
}

type userModel struct {
	Email           string                `tfsdk:"email"`
	Name            string                `tfsdk:"name"`
	Status          string                `tfsdk:"status"`
	IsAccountAdmin  bool                  `tfsdk:"is_account_admin"`
	RoleAssignments []roleAssignmentModel `tfsdk:"role_assignments"`
}

type roleAssignmentModel struct {
	CloudProviderAccountID string   `tfsdk:"cloud_provider_account_id"`
	DisplayName            string   `tfsdk:"display_name"`
	Roles                  []string `tfsdk:"roles"`
}

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the users on the caller's Lucidity account, optionally filtered. Lucidity staff accounts are not listed.",
		Attributes: map[string]schema.Attribute{
			"tenant_id": tenantIDDataSourceAttribute(),
			"email":     schema.StringAttribute{Optional: true, Description: "Only users whose email contains this text (case-insensitive)."},
			"role":      schema.StringAttribute{Optional: true, Description: "Only users holding this role on any cloud account."},
			"status":    schema.StringAttribute{Optional: true, Description: "Only users with this status, e.g. `ACTIVE` or `DISABLED`. If not set, removed users are included as `DISABLED`."},
			"users": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The matching users.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"email":            schema.StringAttribute{Computed: true, Description: "Email address; identifies the user."},
						"name":             schema.StringAttribute{Computed: true, Description: "Full name."},
						"status":           schema.StringAttribute{Computed: true, Description: "`ACTIVE` or `DISABLED`."},
						"is_account_admin": schema.BoolAttribute{Computed: true, Description: "Account admins hold every permission on every cloud account, so their `role_assignments` is empty."},
						"role_assignments": schema.ListNestedAttribute{
							Computed:    true,
							Description: "Roles held, per cloud account.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"cloud_provider_account_id": schema.StringAttribute{Computed: true, Description: "AWS account ID, Azure subscription ID or GCP project ID."},
									"display_name":              schema.StringAttribute{Computed: true, Description: "The cloud account's name in the Lucidity dashboard."},
									"roles":                     schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Role names, e.g. `Viewer`."},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state usersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	users, err := d.client.ListUsers(withTenant(ctx, state.TenantID), client.UserFilter{
		Email:  state.Email.ValueString(),
		Role:   state.Role.ValueString(),
		Status: state.Status.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Lucidity users", err.Error())
		return
	}
	state.Users = make([]userModel, 0, len(users))
	for _, u := range users {
		m := userModel{Email: u.Email, Name: u.Name, Status: u.Status, IsAccountAdmin: u.IsAccountAdmin, RoleAssignments: make([]roleAssignmentModel, 0, len(u.RoleAssignments))}
		for _, ra := range u.RoleAssignments {
			roles := ra.Roles
			if roles == nil {
				roles = []string{}
			}
			m.RoleAssignments = append(m.RoleAssignments, roleAssignmentModel{CloudProviderAccountID: ra.CloudProviderAccountID, DisplayName: ra.DisplayName, Roles: roles})
		}
		state.Users = append(state.Users, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
