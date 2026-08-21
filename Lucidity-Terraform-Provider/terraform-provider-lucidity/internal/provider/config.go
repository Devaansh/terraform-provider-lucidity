package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const envRefreshToken = "LUCIDITY_REFRESH_TOKEN"

// execCommandTimeout bounds refresh_token_command. A var (not const) so
// tests can shrink it instead of waiting out the real timeout.
var execCommandTimeout = 30 * time.Second

// resolveRefreshToken implements the locked token-source precedence from
// CLAUDE.md ("Token-source precedence"): exactly one of refreshToken,
// refreshTokenFile, or refreshTokenCommand may be set — that mutual
// exclusivity is enforced separately by a provider ConfigValidator before
// Configure ever calls this. If none of the three are set, fall back to the
// LUCIDITY_REFRESH_TOKEN env var.
func resolveRefreshToken(ctx context.Context, refreshToken, refreshTokenFile, refreshTokenCommand string) (string, error) {
	switch {
	case refreshToken != "":
		return refreshToken, nil
	case refreshTokenFile != "":
		return readTokenFile(refreshTokenFile)
	case refreshTokenCommand != "":
		return runTokenCommand(ctx, refreshTokenCommand)
	default:
		if v := os.Getenv(envRefreshToken); v != "" {
			return v, nil
		}
		return "", fmt.Errorf(
			"no refresh token found: set exactly one of the refresh_token, refresh_token_file, "+
				"or refresh_token_command provider attributes, or the %s environment variable",
			envRefreshToken)
	}
}

func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading refresh_token_file %q: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("refresh_token_file %q is empty", path)
	}
	return token, nil
}

func runTokenCommand(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execCommandTimeout)
	defer cancel()

	cmd := shellCommand(ctx, command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("refresh_token_command timed out after %s", execCommandTimeout)
		}
		return "", fmt.Errorf("refresh_token_command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("refresh_token_command produced no output")
	}
	return token, nil
}

// shellCommand mirrors the AWS CLI credential_process / kubectl exec-auth
// pattern: run via the platform shell so pipelines and env expansion work as
// users expect.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
