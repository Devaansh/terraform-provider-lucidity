package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const tenantResourceTypeName = "lucidity_tenant"

func cloudEntityInformationType() tftypes.Object {
	return tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"cloud_provider":            tftypes.String,
			"cloud_provider_account_id": tftypes.String,
			"external_id":               tftypes.String,
			"aws_iam_role_name":         tftypes.String,
			"aws_iam_policy_name":       tftypes.String,
		},
	}
}

func tenantConfigType() tftypes.Object {
	return tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"cloud_entity_information":    cloudEntityInformationType(),
			"display_name":                tftypes.String,
			"product_list":                tftypes.List{ElementType: tftypes.String},
			"aws_root_id":                 tftypes.String,
			"skip_cloud_permission_check": tftypes.Bool,
			"account_delete_protection":   tftypes.Bool,
			"destroy_behavior":            tftypes.String,
			"tenant_id":                   tftypes.String,
			"status":                      tftypes.String,
			"cloud_entity_name":           tftypes.String,
		},
	}
}

// validTenantConfig returns a fully-populated, schema-valid config value map
// (minus computed attributes, left null as Terraform would leave them in a
// real plan). Individual tests override one field to exercise a validator.
func validTenantConfig() map[string]tftypes.Value {
	cei := tftypes.NewValue(cloudEntityInformationType(), map[string]tftypes.Value{
		"cloud_provider":            strVal("AWS"),
		"cloud_provider_account_id": strVal("123456789012"),
		"external_id":               strVal("8f14e45f-ceea-4331-9f5e-111111111111"),
		"aws_iam_role_name":         strVal("LucidityRole"),
		"aws_iam_policy_name":       strVal("LucidityPolicy"),
	})
	productList := tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{strVal("AUTOSCALER")})
	return map[string]tftypes.Value{
		"cloud_entity_information": cei,
		"display_name":             strVal("non-prod"),
		"product_list":             productList,
	}
}

func tenantConfigValue(t *testing.T, overrides map[string]tftypes.Value, ceiOverrides map[string]tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	objType := tenantConfigType()
	set := validTenantConfig()

	if len(ceiOverrides) > 0 {
		ceiType := cloudEntityInformationType()
		ceiSet := map[string]tftypes.Value{}
		// Start from the valid CEI and apply overrides.
		validCEI := map[string]tftypes.Value{
			"cloud_provider":            strVal("AWS"),
			"cloud_provider_account_id": strVal("123456789012"),
			"external_id":               strVal("8f14e45f-ceea-4331-9f5e-111111111111"),
			"aws_iam_role_name":         strVal("LucidityRole"),
			"aws_iam_policy_name":       strVal("LucidityPolicy"),
		}
		for k, v := range validCEI {
			ceiSet[k] = v
		}
		for k, v := range ceiOverrides {
			ceiSet[k] = v
		}
		set["cloud_entity_information"] = tftypes.NewValue(ceiType, ceiSet)
	}
	for k, v := range overrides {
		set[k] = v
	}

	full := map[string]tftypes.Value{}
	for name, ty := range objType.AttributeTypes {
		if v, ok := set[name]; ok {
			full[name] = v
		} else {
			full[name] = tftypes.NewValue(ty, nil)
		}
	}
	dv, err := tfprotov6.NewDynamicValue(objType, tftypes.NewValue(objType, full))
	if err != nil {
		t.Fatalf("NewDynamicValue: %v", err)
	}
	return &dv
}

func TestTenantResourceRPC_GetProviderSchema_RegistersResourceAndDataSource(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
	if _, ok := resp.ResourceSchemas[tenantResourceTypeName]; !ok {
		t.Fatalf("expected %q to be a registered resource, got: %+v", tenantResourceTypeName, resp.ResourceSchemas)
	}
	if _, ok := resp.DataSourceSchemas["lucidity_tenants"]; !ok {
		t.Fatalf("expected lucidity_tenants to be a registered data source, got: %+v", resp.DataSourceSchemas)
	}
}

func TestTenantResourceRPC_ValidateConfig_AcceptsValidConfig(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: tenantResourceTypeName,
		Config:   tenantConfigValue(t, nil, nil),
	})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestTenantResourceRPC_ValidateConfig_RejectsNonAWSCloudProvider(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: tenantResourceTypeName,
		Config:   tenantConfigValue(t, nil, map[string]tftypes.Value{"cloud_provider": strVal("AZURE")}),
	})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for cloud_provider=AZURE (onboarding is AWS-only), got: %+v", resp.Diagnostics)
	}
}

func TestTenantResourceRPC_ValidateConfig_RejectsEmptyProductList(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: tenantResourceTypeName,
		Config: tenantConfigValue(t, map[string]tftypes.Value{
			"product_list": tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
		}, nil),
	})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for an empty product_list, got: %+v", resp.Diagnostics)
	}
}

func TestTenantResourceRPC_ValidateConfig_RejectsUnsupportedProduct(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: tenantResourceTypeName,
		Config: tenantConfigValue(t, map[string]tftypes.Value{
			"product_list": tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{strVal("NOT_A_PRODUCT")}),
		}, nil),
	})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for an unsupported product, got: %+v", resp.Diagnostics)
	}
}

func TestTenantResourceRPC_ValidateConfig_RejectsInvalidDestroyBehavior(t *testing.T) {
	srv := newTestProviderServer(t)
	resp, err := srv.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: tenantResourceTypeName,
		Config: tenantConfigValue(t, map[string]tftypes.Value{
			"destroy_behavior": strVal("delete"),
		}, nil),
	})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for destroy_behavior=delete (must be forget|deboard), got: %+v", resp.Diagnostics)
	}
}
