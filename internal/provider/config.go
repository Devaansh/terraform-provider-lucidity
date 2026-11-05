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

	"github.com/luciditycloud/terraform-provider-lucidity/internal/client"
)

const (
	envRefreshToken = "LUCIDITY_REFRESH_TOKEN"
	envClientID     = "LUCIDITY_CLIENT_ID"
	envClientSecret = "LUCIDITY_CLIENT_SECRET"
)

// execCommandTimeout bounds refresh_token_command and client_secret_command.
// A var (not const) so tests can shrink it instead of waiting out the real
// timeout.
var execCommandTimeout = 30 * time.Second

// credentialSources holds the credential attributes from the provider
// config, each "" when unset.
type credentialSources struct {
	RefreshToken        string
	RefreshTokenFile    string
	RefreshTokenCommand string

	ClientID            string
	ClientSecret        string
	ClientSecretFile    string
	ClientSecretCommand string
}

func (s credentialSources) hasRefreshToken() bool {
	return s.RefreshToken != "" || s.RefreshTokenFile != "" || s.RefreshTokenCommand != ""
}

func (s credentialSources) hasClientCredentials() bool {
	return s.ClientID != "" || s.ClientSecret != "" || s.ClientSecretFile != "" || s.ClientSecretCommand != ""
}

// resolveCredentials picks exactly one credential type: a user's refresh
// token, or a service account's client ID and secret.
//
//   - Credential attributes in the provider block decide the type. Mixing the
//     two types, or setting more than one source for the same secret, is
//     rejected earlier by the provider's ConfigValidators.
//   - With client attributes, a missing client ID or secret is taken from
//     LUCIDITY_CLIENT_ID / LUCIDITY_CLIENT_SECRET.
//   - With no credential attributes at all, the environment decides:
//     LUCIDITY_REFRESH_TOKEN, or LUCIDITY_CLIENT_ID + LUCIDITY_CLIENT_SECRET.
//     Both types set in the environment is an error rather than a guess.
func resolveCredentials(ctx context.Context, s credentialSources) (client.Credentials, error) {
	switch {
	case s.hasRefreshToken():
		token, err := resolveSecret(ctx, "refresh_token", s.RefreshToken, s.RefreshTokenFile, s.RefreshTokenCommand)
		if err != nil {
			return client.Credentials{}, err
		}
		return client.Credentials{RefreshToken: token}, nil

	case s.hasClientCredentials():
		return resolveClientCredentials(ctx, s)

	default:
		refreshToken := os.Getenv(envRefreshToken)
		clientID, clientSecret := os.Getenv(envClientID), os.Getenv(envClientSecret)
		switch {
		case refreshToken != "" && (clientID != "" || clientSecret != ""):
			return client.Credentials{}, fmt.Errorf(
				"both %s and %s/%s are set: use either a refresh token or service account credentials, not both",
				envRefreshToken, envClientID, envClientSecret)
		case refreshToken != "":
			return client.Credentials{RefreshToken: refreshToken}, nil
		case clientID != "" || clientSecret != "":
			return resolveClientCredentials(ctx, s)
		}
		return client.Credentials{}, fmt.Errorf(
			"no credentials found: set either a refresh token (one of refresh_token, refresh_token_file, refresh_token_command, or the %s environment variable) "+
				"or service account credentials (client_id or %s, plus one of client_secret, client_secret_file, client_secret_command, or %s)",
			envRefreshToken, envClientID, envClientSecret)
	}
}

func resolveClientCredentials(ctx context.Context, s credentialSources) (client.Credentials, error) {
	clientID := s.ClientID
	if clientID == "" {
		clientID = os.Getenv(envClientID)
	}
	secret, err := resolveSecret(ctx, "client_secret", s.ClientSecret, s.ClientSecretFile, s.ClientSecretCommand)
	if err != nil {
		return client.Credentials{}, err
	}
	if secret == "" {
		secret = os.Getenv(envClientSecret)
	}

	switch {
	case clientID == "" && secret == "":
		return client.Credentials{}, fmt.Errorf("service account credentials are incomplete: no client ID or secret found")
	case clientID == "":
		return client.Credentials{}, fmt.Errorf("service account credentials are incomplete: a client secret is set but no client ID (set client_id or %s)", envClientID)
	case secret == "":
		return client.Credentials{}, fmt.Errorf(
			"service account credentials are incomplete: client ID %q is set but no client secret (set one of client_secret, client_secret_file, client_secret_command, or %s)",
			clientID, envClientSecret)
	}
	return client.Credentials{ClientID: clientID, ClientSecret: secret}, nil
}

// resolveSecret reads a secret from whichever one of its three sources is
// set — the attribute itself, <name>_file, or <name>_command — or returns ""
// if none is. name is the attribute name, used in error messages.
func resolveSecret(ctx context.Context, name, direct, file, command string) (string, error) {
	switch {
	case direct != "":
		return direct, nil
	case file != "":
		return readSecretFile(name+"_file", file)
	case command != "":
		return runSecretCommand(ctx, name+"_command", command)
	}
	return "", nil
}

func readSecretFile(attr, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s %q: %w", attr, path, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("%s %q is empty", attr, path)
	}
	return secret, nil
}

func runSecretCommand(ctx context.Context, attr, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execCommandTimeout)
	defer cancel()

	cmd := shellCommand(ctx, command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s timed out after %s", attr, execCommandTimeout)
		}
		return "", fmt.Errorf("%s failed: %w: %s", attr, err, strings.TrimSpace(stderr.String()))
	}

	secret := strings.TrimSpace(stdout.String())
	if secret == "" {
		return "", fmt.Errorf("%s produced no output", attr)
	}
	return secret, nil
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
