package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/luciditycloud/terraform-provider-lucidity/internal/client"
)

var (
	_ datasource.DataSource              = &scalingPoliciesDataSource{}
	_ datasource.DataSourceWithConfigure = &scalingPoliciesDataSource{}
)

func newScalingPoliciesDataSource() datasource.DataSource {
	return &scalingPoliciesDataSource{}
}

type scalingPoliciesDataSource struct {
	client *client.Client
}

type scalingPoliciesDataSourceModel struct {
	Policies []scalingPolicyResourceModel `tfsdk:"policies"`
}

func (d *scalingPoliciesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scaling_policies"
}

func (d *scalingPoliciesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *scalingPoliciesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attr := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Lists every scaling policy in the caller's Lucidity account.",
		Attributes: map[string]schema.Attribute{
			"policies": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Every scaling policy in the account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                        schema.StringAttribute{Computed: true, Description: "Lucidity's numeric ID for the policy."},
						"name":                      schema.StringAttribute{Computed: true, Description: "Policy name."},
						"min_buffer_in_gb":          attr("Minimum buffer, in GB."),
						"max_buffer_in_gb":          attr("Maximum buffer, in GB."),
						"ideal_utilization_percent": attr("Target disk utilization, percent."),
						"max_fs_size_in_gb":         attr("Maximum file system size, in GB."),
						"min_disk_sku_size":         attr("Minimum disk SKU size."),
						"max_disk_sku_size":         attr("Maximum disk SKU size."),
						"attached_instance_count":   attr("How many instances the policy is attached to."),
					},
				},
			},
		},
	}
}

func (d *scalingPoliciesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	policies, err := d.client.ListScalingPolicies(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list Lucidity scaling policies", err.Error())
		return
	}
	state := scalingPoliciesDataSourceModel{Policies: make([]scalingPolicyResourceModel, 0, len(policies))}
	for i := range policies {
		var m scalingPolicyResourceModel
		applyScalingPolicy(&m, &policies[i])
		state.Policies = append(state.Policies, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
