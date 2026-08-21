// Package provider implements the Lucidity Terraform provider. Phase 1
// wires up authentication only; the lucidity_tenant resource and
// lucidity_tenants data source land in Phase 2 (see CLAUDE.md).
package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/providervalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

const (
	defaultBaseURL             = "https://dash-back.lucidity.dev"
	defaultMaxParallelRequests = 10
)

var (
	_ provider.Provider                     = &LucidityProvider{}
	_ provider.ProviderWithConfigValidators = &LucidityProvider{}
)

// LucidityProvider is the top-level Terraform provider implementation.
type LucidityProvider struct {
	// version is injected by main.go, set at build time by GoReleaser.
	version string
}

type lucidityProviderModel struct {
	RefreshToken        types.String `tfsdk:"refresh_token"`
	RefreshTokenFile    types.String `tfsdk:"refresh_token_file"`
	RefreshTokenCommand types.String `tfsdk:"refresh_token_command"`
	BaseURL             types.String `tfsdk:"base_url"`
	MaxParallelRequests types.Int64  `tfsdk:"max_parallel_requests"`
	AccountName         types.String `tfsdk:"account_name"`
}

// LucidityClients bundles the API client made available to resources and
// data sources via their Configure methods. Only Client exists in Phase 1.
type LucidityClients struct {
	Client *client.Client
}

// New returns a provider.Provider factory, suitable for
// providerserver.ServeOpts. version should be injected by GoReleaser at
// build time; main.go defaults it to "dev" for local builds.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &LucidityProvider{version: version}
	}
}

func (p *LucidityProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "lucidity"
	resp.Version = p.version
}

func (p *LucidityProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interact with the Lucidity cloud storage optimization platform.",
		Attributes: map[string]schema.Attribute{
			"refresh_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Long-lived Lucidity refresh token, supplied directly. Discouraged: the value ends up in your .tf config or, if fed from a data source, in Terraform state. Prefer refresh_token_file, refresh_token_command, or the LUCIDITY_REFRESH_TOKEN environment variable. Exactly one of refresh_token, refresh_token_file, or refresh_token_command may be set.",
			},
			"refresh_token_file": schema.StringAttribute{
				Optional:    true,
				Description: "Path to a local file containing only the Lucidity refresh token; its contents are trimmed of surrounding whitespace. Never sent anywhere but read into memory. Exactly one of refresh_token, refresh_token_file, or refresh_token_command may be set.",
			},
			"refresh_token_command": schema.StringAttribute{
				Optional:    true,
				Description: "Shell command whose trimmed stdout is used as the Lucidity refresh token, e.g. `vault kv get -field=token secret/lucidity`. Runs via the platform shell with a 30s timeout; stdout is never logged, including at TF_LOG=DEBUG. Exactly one of refresh_token, refresh_token_file, or refresh_token_command may be set.",
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: fmt.Sprintf("Lucidity API base URL for your deployment. Defaults to %s. See the provider's Getting Started documentation for the base URL matching your dashboard login URL.", defaultBaseURL),
			},
			"max_parallel_requests": schema.Int64Attribute{
				Optional:    true,
				Description: fmt.Sprintf("Maximum number of concurrent Lucidity API requests. Defaults to %d.", defaultMaxParallelRequests),
			},
			"account_name": schema.StringAttribute{
				Optional:    true,
				Description: "Lucidity dashboard account name. Reserved for Phase 2 tenant display-name updates; unused in Phase 1.",
			},
		},
	}
}

// ConfigValidators enforces the locked token-source precedence: zero or one
// of refresh_token / refresh_token_file / refresh_token_command may be set
// (zero is valid — it means "use LUCIDITY_REFRESH_TOKEN"); more than one is
// a hard validate-time error naming every attribute that was set.
func (p *LucidityProvider) ConfigValidators(_ context.Context) []provider.ConfigValidator {
	return []provider.ConfigValidator{
		providervalidator.Conflicting(
			path.MatchRoot("refresh_token"),
			path.MatchRoot("refresh_token_file"),
			path.MatchRoot("refresh_token_command"),
		),
	}
}

func (p *LucidityProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data lucidityProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	refreshToken, err := resolveRefreshToken(ctx, data.RefreshToken.ValueString(), data.RefreshTokenFile.ValueString(), data.RefreshTokenCommand.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to resolve Lucidity refresh token", err.Error())
		return
	}

	baseURL := defaultBaseURL
	if v := data.BaseURL.ValueString(); v != "" {
		baseURL = v
	}
	if parsed, err := url.ParseRequestURI(baseURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Invalid base_url",
			fmt.Sprintf("%q is not a valid absolute URL.", baseURL),
		)
		return
	}

	maxParallel := defaultMaxParallelRequests
	if !data.MaxParallelRequests.IsNull() {
		maxParallel = int(data.MaxParallelRequests.ValueInt64())
	}

	apiClient := client.NewClient(baseURL, refreshToken, client.WithMaxParallelRequests(maxParallel))
	apiClient.DebugLog = func(ctx context.Context, msg string, fields map[string]any) {
		tflog.Debug(ctx, msg, fields)
	}

	clients := &LucidityClients{Client: apiClient}
	resp.DataSourceData = clients
	resp.ResourceData = clients
}

func (p *LucidityProvider) Resources(_ context.Context) []func() resource.Resource {
	// Phase 2: lucidity_tenant.
	return nil
}

func (p *LucidityProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	// Phase 2: lucidity_tenants.
	return nil
}
