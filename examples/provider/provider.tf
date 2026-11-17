terraform {
  required_providers {
    lucidity = {
      source = "registry.terraform.io/Devaansh/lucidity"
    }
  }
}

# Authenticate as a user, with a refresh token generated in the dashboard.
provider "lucidity" {
  lucidity_dashboard_url          = "https://www.web.lucidity.dev/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"

  # The refresh token itself is best supplied via refresh_token_file,
  # refresh_token_command, or the LUCIDITY_REFRESH_TOKEN environment
  # variable rather than hardcoded here — see the provider index docs.
}

# Or authenticate as a service account (recommended for automation). The
# client secret is shown only once, when the credential is created.
provider "lucidity" {
  alias                           = "service_account"
  lucidity_dashboard_url          = "https://www.web.lucidity.dev/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"

  client_id             = "svc-abc123"
  client_secret_command = "aws secretsmanager get-secret-value --secret-id lucidity/client-secret --query SecretString --output text"
  # or LUCIDITY_CLIENT_ID + LUCIDITY_CLIENT_SECRET in the environment
}
