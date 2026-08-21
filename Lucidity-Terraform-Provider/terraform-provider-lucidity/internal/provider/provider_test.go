package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// These drive the provider's tfprotov6.ProviderServer RPCs directly, because
// `terraform plan` against an empty config (no resources/data sources) never
// calls ValidateProviderConfig or ConfigureProvider at all — Terraform prunes
// a provider that nothing in the graph references. That makes "terraform
// plan succeeds" alone a weak signal for Phase 1: it would pass even with
// broken auth wiring. Exercising the RPCs directly is what actually proves
// ConfigValidators and Configure behave correctly.

func providerConfigType() tftypes.Object {
	return tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"refresh_token":         tftypes.String,
			"refresh_token_file":    tftypes.String,
			"refresh_token_command": tftypes.String,
			"base_url":              tftypes.String,
			"max_parallel_requests": tftypes.Number,
			"account_name":          tftypes.String,
		},
	}
}

func newTestProviderServer(t *testing.T) tfprotov6.ProviderServer {
	t.Helper()
	return providerserver.NewProtocol6(New("test")())()
}

func strVal(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

func configValue(t *testing.T, set map[string]tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	objType := providerConfigType()
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

func hasErrorDiagnostic(diags []*tfprotov6.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

func TestProviderRPC_ValidateConfig_RejectsConflictingTokenSources(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token":      strVal("direct-token"),
		"refresh_token_file": strVal("token.txt"),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for conflicting refresh-token sources, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_ValidateConfig_AllowsExactlyOneTokenSource(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token": strVal("direct-token"),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_Configure_ErrorsWithNoTokenSource(t *testing.T) {
	t.Setenv(envRefreshToken, "")
	srv := newTestProviderServer(t)
	cfg := configValue(t, nil)

	resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for a missing refresh token, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_Configure_SucceedsWithDirectToken(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token": strVal("dummy-token"),
	})

	resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_Configure_ErrorsOnMalformedBaseURL(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token": strVal("dummy-token"),
		"base_url":      strVal("not a url"),
	})

	resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for a malformed base_url, got: %+v", resp.Diagnostics)
	}
}
