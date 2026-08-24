package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveRefreshToken_DirectAttribute(t *testing.T) {
	got, err := resolveRefreshToken(context.Background(), "direct-token", "", "")
	if err != nil {
		t.Fatalf("resolveRefreshToken: %v", err)
	}
	if got != "direct-token" {
		t.Fatalf("got %q, want direct-token", got)
	}
}

func TestResolveRefreshToken_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveRefreshToken(context.Background(), "", path, "")
	if err != nil {
		t.Fatalf("resolveRefreshToken: %v", err)
	}
	if got != "file-token" {
		t.Fatalf("got %q, want file-token (trimmed)", got)
	}
}

func TestResolveRefreshToken_FileMissing(t *testing.T) {
	_, err := resolveRefreshToken(context.Background(), "", "/does/not/exist", "")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "/does/not/exist") {
		t.Fatalf("error should name the path, got: %v", err)
	}
}

func TestResolveRefreshToken_FileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := resolveRefreshToken(context.Background(), "", path, "")
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestResolveRefreshToken_Command(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	got, err := resolveRefreshToken(context.Background(), "", "", "printf 'command-token\\n'")
	if err != nil {
		t.Fatalf("resolveRefreshToken: %v", err)
	}
	if got != "command-token" {
		t.Fatalf("got %q, want command-token (trimmed)", got)
	}
}

func TestResolveRefreshToken_CommandNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	_, err := resolveRefreshToken(context.Background(), "", "", "echo 'nope' >&2; exit 1")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected stderr to surface in the error, got: %v", err)
	}
}

func TestResolveRefreshToken_CommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix shell command")
	}
	orig := execCommandTimeout
	execCommandTimeout = 20 * time.Millisecond
	defer func() { execCommandTimeout = orig }()

	_, err := resolveRefreshToken(context.Background(), "", "", "sleep 5")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestResolveRefreshToken_EnvVarFallback(t *testing.T) {
	t.Setenv(envRefreshToken, "env-token")
	got, err := resolveRefreshToken(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("resolveRefreshToken: %v", err)
	}
	if got != "env-token" {
		t.Fatalf("got %q, want env-token", got)
	}
}

func TestResolveRefreshToken_NothingSet(t *testing.T) {
	t.Setenv(envRefreshToken, "")
	_, err := resolveRefreshToken(context.Background(), "", "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"refresh_token", "refresh_token_file", "refresh_token_command", envRefreshToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}
