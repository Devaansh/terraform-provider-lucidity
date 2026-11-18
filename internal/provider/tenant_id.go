package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

// tenant_id: the Lucidity tenant a resource's or data source's API calls act
// on, sent as the X-tenant header. It overrides the provider's tenant_id;
// unset, the provider's applies (and with neither, no header is sent).
//
// It only selects the tenant context of the calls, so changing it never
// replaces anything: the next apply just stores the new value.
const tenantIDDescription = "Lucidity tenant ID (e.g. lucidity_tenant.<name>.tenant_id) to act on, sent as the X-tenant header. " +
	"Overrides the provider's tenant_id; leave unset to use it. Needed when authenticating as a service account with roles on several tenants."

func tenantIDResourceAttribute() rschema.StringAttribute {
	return rschema.StringAttribute{
		Optional:    true,
		Description: tenantIDDescription,
		Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
	}
}

func tenantIDDataSourceAttribute() dsschema.StringAttribute {
	return dsschema.StringAttribute{
		Optional:    true,
		Description: tenantIDDescription,
		Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
	}
}

// withTenant scopes ctx's API calls to tenantID when it's set.
func withTenant(ctx context.Context, tenantID types.String) context.Context {
	if tenantID.IsNull() || tenantID.IsUnknown() {
		return ctx
	}
	return client.WithTenant(ctx, tenantID.ValueString())
}
