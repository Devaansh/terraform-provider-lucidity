package provider

import (
	"context"
	"fmt"
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

// testDashboardLoginURL is a valid dashboard_login_url value for tests that
// aren't specifically exercising deployment-selection behavior.
const testDashboardLoginURL = "https://www.web.lucidity.dev/dashboard"

func providerConfigType() tftypes.Object {
	return tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"refresh_token":                    tftypes.String,
			"refresh_token_file":               tftypes.String,
			"refresh_token_command":            tftypes.String,
			"dashboard_login_url":              tftypes.String,
			"max_parallel_requests":            tftypes.Number,
			"proactive_refresh_buffer_minutes": tftypes.Number,
			"account_name":                     tftypes.String,
		},
	}
}

func newTestProviderServer(t *testing.T) tfprotov6.ProviderServer {
	t.Helper()
	return providerserver.NewProtocol6(New("test")())()
}

func strVal(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

func numVal(n int64) tftypes.Value { return tftypes.NewValue(tftypes.Number, n) }

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
		"refresh_token":       strVal("direct-token"),
		"dashboard_login_url": strVal(testDashboardLoginURL),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_ValidateConfig_RejectsUnknownDashboardLoginURL(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token":       strVal("direct-token"),
		"dashboard_login_url": strVal("https://not-a-real-lucidity-deployment.example.com"),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for an unrecognized dashboard_login_url, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_ValidateConfig_RejectsMissingDashboardLoginURL(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token": strVal("direct-token"),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for a missing (required) dashboard_login_url, got: %+v", resp.Diagnostics)
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
		"refresh_token":       strVal("dummy-token"),
		"dashboard_login_url": strVal(testDashboardLoginURL),
	})

	resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_Configure_MapsEveryKnownDashboardLoginURL(t *testing.T) {
	for _, d := range knownDeployments {
		d := d
		t.Run(d.dashboardLoginURL, func(t *testing.T) {
			srv := newTestProviderServer(t)
			cfg := configValue(t, map[string]tftypes.Value{
				"refresh_token":       strVal("dummy-token"),
				"dashboard_login_url": strVal(d.dashboardLoginURL),
			})

			resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
			if err != nil {
				t.Fatalf("ConfigureProvider: %v", err)
			}
			if hasErrorDiagnostic(resp.Diagnostics) {
				t.Fatalf("expected no error diagnostic for a known deployment, got: %+v", resp.Diagnostics)
			}
		})
	}
}

func TestProviderRPC_ValidateConfig_AcceptsInRangeProactiveRefreshBuffer(t *testing.T) {
	for _, minutes := range []int64{1, 7, 14} {
		minutes := minutes
		t.Run(fmt.Sprintf("%d", minutes), func(t *testing.T) {
			srv := newTestProviderServer(t)
			cfg := configValue(t, map[string]tftypes.Value{
				"refresh_token":                    strVal("direct-token"),
				"dashboard_login_url":              strVal(testDashboardLoginURL),
				"proactive_refresh_buffer_minutes": numVal(minutes),
			})

			resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
			if err != nil {
				t.Fatalf("ValidateProviderConfig: %v", err)
			}
			if hasErrorDiagnostic(resp.Diagnostics) {
				t.Fatalf("expected no error diagnostic for %d minutes, got: %+v", minutes, resp.Diagnostics)
			}
		})
	}
}

func TestProviderRPC_ValidateConfig_RejectsOutOfRangeProactiveRefreshBuffer(t *testing.T) {
	for _, minutes := range []int64{0, 15, -1} {
		minutes := minutes
		t.Run(fmt.Sprintf("%d", minutes), func(t *testing.T) {
			srv := newTestProviderServer(t)
			cfg := configValue(t, map[string]tftypes.Value{
				"refresh_token":                    strVal("direct-token"),
				"dashboard_login_url":              strVal(testDashboardLoginURL),
				"proactive_refresh_buffer_minutes": numVal(minutes),
			})

			resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
			if err != nil {
				t.Fatalf("ValidateProviderConfig: %v", err)
			}
			if !hasErrorDiagnostic(resp.Diagnostics) {
				t.Fatalf("expected an error diagnostic for %d minutes (must be 1-14), got: %+v", minutes, resp.Diagnostics)
			}
		})
	}
}

func TestProviderRPC_Configure_SucceedsWithProactiveRefreshBufferUnset(t *testing.T) {
	// Regression guard: leaving the new attribute unset must still configure
	// cleanly, using client.DefaultProactiveRefreshAge.
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token":       strVal("dummy-token"),
		"dashboard_login_url": strVal(testDashboardLoginURL),
	})

	resp, err := srv.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ConfigureProvider: %v", err)
	}
	if hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected no error diagnostic, got: %+v", resp.Diagnostics)
	}
}

func TestProviderRPC_ValidateConfig_RejectsZeroMaxParallelRequests(t *testing.T) {
	srv := newTestProviderServer(t)
	cfg := configValue(t, map[string]tftypes.Value{
		"refresh_token":         strVal("direct-token"),
		"dashboard_login_url":   strVal(testDashboardLoginURL),
		"max_parallel_requests": numVal(0),
	})

	resp, err := srv.ValidateProviderConfig(context.Background(), &tfprotov6.ValidateProviderConfigRequest{Config: cfg})
	if err != nil {
		t.Fatalf("ValidateProviderConfig: %v", err)
	}
	if !hasErrorDiagnostic(resp.Diagnostics) {
		t.Fatalf("expected an error diagnostic for max_parallel_requests=0, got: %+v", resp.Diagnostics)
	}
}
