package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Devaansh/terraform-provider-lucidity/internal/client"
)

// clearCredentialEnv makes sure no credential env var leaks in from the
// machine running the tests.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envRefreshToken, "")
	t.Setenv(envClientID, "")
	t.Setenv(envClientSecret, "")
}

func mustResolve(t *testing.T, s credentialSources) client.Credentials {
	t.Helper()
	got, err := resolveCredentials(context.Background(), s)
	if err != nil {
		t.Fatalf("resolveCredentials: %v", err)
	}
	return got
}

func TestResolveCredentials_RefreshTokenAttribute(t *testing.T) {
	clearCredentialEnv(t)
	got := mustResolve(t, credentialSources{RefreshToken: "direct-token"})
	if got != (client.Credentials{RefreshToken: "direct-token"}) {
		t.Fatalf("got %+v, want refresh token direct-token", got)
	}
}

func TestResolveCredentials_RefreshTokenFile(t *testing.T) {
	clearCredentialEnv(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := mustResolve(t, credentialSources{RefreshTokenFile: path})
	if got.RefreshToken != "file-token" {
		t.Fatalf("got %q, want file-token (trimmed)", got.RefreshToken)
	}
}

func TestResolveCredentials_RefreshTokenFileMissing(t *testing.T) {
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{RefreshTokenFile: "/does/not/exist"})
	if err == nil || !strings.Contains(err.Error(), "/does/not/exist") || !strings.Contains(err.Error(), "refresh_token_file") {
		t.Fatalf("expected an error naming refresh_token_file and the path, got: %v", err)
	}
}

func TestResolveCredentials_RefreshTokenFileEmpty(t *testing.T) {
	clearCredentialEnv(t)
	path := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := resolveCredentials(context.Background(), credentialSources{RefreshTokenFile: path}); err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestResolveCredentials_RefreshTokenCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	clearCredentialEnv(t)
	got := mustResolve(t, credentialSources{RefreshTokenCommand: "printf 'command-token\\n'"})
	if got.RefreshToken != "command-token" {
		t.Fatalf("got %q, want command-token (trimmed)", got.RefreshToken)
	}
}

func TestResolveCredentials_CommandNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{RefreshTokenCommand: "echo 'nope' >&2; exit 1"})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected stderr to surface in the error, got: %v", err)
	}
}

func TestResolveCredentials_CommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	clearCredentialEnv(t)
	orig := execCommandTimeout
	execCommandTimeout = 20 * time.Millisecond
	defer func() { execCommandTimeout = orig }()

	_, err := resolveCredentials(context.Background(), credentialSources{RefreshTokenCommand: "sleep 5"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestResolveCredentials_RefreshTokenEnv(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envRefreshToken, "env-token")
	got := mustResolve(t, credentialSources{})
	if got != (client.Credentials{RefreshToken: "env-token"}) {
		t.Fatalf("got %+v, want refresh token env-token", got)
	}
}

func TestResolveCredentials_NothingSet(t *testing.T) {
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"refresh_token", "refresh_token_file", "refresh_token_command", envRefreshToken, "client_id", "client_secret", envClientID, envClientSecret} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestResolveCredentials_ClientCredentialsAttributes(t *testing.T) {
	clearCredentialEnv(t)
	got := mustResolve(t, credentialSources{ClientID: "svc-1", ClientSecret: "s3cret"})
	if got != (client.Credentials{ClientID: "svc-1", ClientSecret: "s3cret"}) || !got.IsServiceAccount() {
		t.Fatalf("got %+v, want service account svc-1", got)
	}
}

func TestResolveCredentials_ClientSecretFile(t *testing.T) {
	clearCredentialEnv(t)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := mustResolve(t, credentialSources{ClientID: "svc-1", ClientSecretFile: path})
	if got.ClientSecret != "file-secret" {
		t.Fatalf("got %q, want file-secret", got.ClientSecret)
	}
}

func TestResolveCredentials_ClientSecretCommandErrorNamesAttribute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{ClientID: "svc-1", ClientSecretCommand: "exit 3"})
	if err == nil || !strings.Contains(err.Error(), "client_secret_command") {
		t.Fatalf("expected an error naming client_secret_command, got: %v", err)
	}
}

func TestResolveCredentials_ClientCredentialsFromEnv(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envClientID, "svc-env")
	t.Setenv(envClientSecret, "env-secret")
	got := mustResolve(t, credentialSources{})
	if got != (client.Credentials{ClientID: "svc-env", ClientSecret: "env-secret"}) {
		t.Fatalf("got %+v, want service account svc-env", got)
	}
}

// The ID in the config and the secret in the environment (or the other way
// round) is a valid split.
func TestResolveCredentials_ClientIDAttributeSecretFromEnv(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envClientSecret, "env-secret")
	got := mustResolve(t, credentialSources{ClientID: "svc-1"})
	if got != (client.Credentials{ClientID: "svc-1", ClientSecret: "env-secret"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveCredentials_ClientIDWithoutSecret(t *testing.T) {
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{ClientID: "svc-1"})
	if err == nil || !strings.Contains(err.Error(), "no client secret") {
		t.Fatalf("expected a missing-secret error, got: %v", err)
	}
}

func TestResolveCredentials_SecretWithoutClientID(t *testing.T) {
	clearCredentialEnv(t)
	_, err := resolveCredentials(context.Background(), credentialSources{ClientSecret: "s3cret"})
	if err == nil || !strings.Contains(err.Error(), "no client ID") {
		t.Fatalf("expected a missing-client-ID error, got: %v", err)
	}
}

func TestResolveCredentials_BothTypesInEnvIsAnError(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envRefreshToken, "env-token")
	t.Setenv(envClientID, "svc-env")
	t.Setenv(envClientSecret, "env-secret")
	_, err := resolveCredentials(context.Background(), credentialSources{})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected an ambiguous-credentials error, got: %v", err)
	}
}

// Attributes in the provider block decide the type; the other type's env
// vars are ignored rather than treated as a conflict.
func TestResolveCredentials_AttributeTypeWinsOverOtherTypeEnv(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envRefreshToken, "env-token")
	got := mustResolve(t, credentialSources{ClientID: "svc-1", ClientSecret: "s3cret"})
	if !got.IsServiceAccount() || got.RefreshToken != "" {
		t.Fatalf("got %+v, want only the configured service account", got)
	}

	clearCredentialEnv(t)
	t.Setenv(envClientID, "svc-env")
	t.Setenv(envClientSecret, "env-secret")
	got = mustResolve(t, credentialSources{RefreshToken: "direct-token"})
	if got != (client.Credentials{RefreshToken: "direct-token"}) {
		t.Fatalf("got %+v, want only the configured refresh token", got)
	}
}
