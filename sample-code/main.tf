# Sample usage of the lucidity provider, pulled from the Terraform Registry —
# not a local build. Anyone who clones this repo can run this as-is (given a
# valid refresh token); nothing here points at a machine-local binary path.

terraform {
  required_providers {
    lucidity = {
      source  = "registry.terraform.io/Devaansh/lucidity"
      version = "~> 0.1"
    }
  }
}

# Refresh token resolution: this config relies on the LUCIDITY_REFRESH_TOKEN
# environment variable (the CI-default path — see the provider's README /
# CLAUDE.md for the full token-source precedence). Uncomment exactly one of
# the alternatives below instead if you'd rather not use the env var — the
# provider hard-errors if more than one source is set.

provider "lucidity" {
  # refresh_token_file    = "/path/to/a/file/containing/only/the/token"
  # refresh_token_command = "vault kv get -field=token secret/lucidity"
}

# Phase 1 ships auth only — no resources or data sources yet, so there's
# nothing to declare here beyond the provider block. See this repo's README
# for why that also means `terraform plan` here won't exercise auth
# end-to-end until Phase 2 lands a resource (Terraform prunes an unreferenced
# provider from the plan graph).
