# terraform-provider-lucidity

Terraform provider for Lucidity. Currently in Phase 1 (auth layer).
See [CLAUDE.md](CLAUDE.md) for the full design record and phase plan.

- `docs/api/` — Lucidity API reference PDFs (source of truth)
- `docs/lucidity-oidc-proposal.md` — OIDC proposal for the Lucidity team
- `docs/examples/lucidity-tenants.tf` — target end-user configuration pattern (Phase 2)

This repo holds provider source only — no sample/testing Terraform configs.
Published at `registry.terraform.io/Devaansh/lucidity`; pull it from there to
try it out.

## Refresh token from AWS Secrets Manager

The provider takes the refresh token via `refresh_token` / `refresh_token_file`
/ `refresh_token_command` (exactly one) or the `LUCIDITY_REFRESH_TOKEN` env
var — see `internal/provider/provider.go` for the full schema. Two ways to
back that with AWS Secrets Manager:

**Option 1 (recommended): `refresh_token_command`, via the AWS CLI**

```hcl
provider "lucidity" {
  dashboard_login_url   = "https://www.web.lucidity.dev/dashboard"
  refresh_token_command = "aws secretsmanager get-secret-value --secret-id lucidity/refresh-token --query SecretString --output text"
}
```

Runs at configure time; the token never touches `.tf` files or Terraform
state. Requires the AWS CLI installed wherever `terraform` runs, AWS
credentials available in that environment (instance profile, IRSA, SSO
profile, etc.), and an IAM policy granting `secretsmanager:GetSecretValue` on
that secret's ARN. If the secret is stored as JSON rather than a plain
string, pipe through `jq`: `... --output text | jq -r .token`.

**Option 2: `refresh_token`, fed from a data source**

```hcl
provider "aws" {
  region = "us-east-1"
}

data "aws_secretsmanager_secret_version" "lucidity_refresh_token" {
  secret_id = "lucidity/refresh-token"
}

provider "lucidity" {
  dashboard_login_url = "https://www.web.lucidity.dev/dashboard"
  refresh_token        = data.aws_secretsmanager_secret_version.lucidity_refresh_token.secret_string
}
```

Works, but the fetched value gets written into Terraform state as that data
source's attribute (every data source result is persisted to state, not just
resources) — only as safe as your state's encryption/access controls. Also
requires configuring the `aws` provider just for this lookup.

Option 1 is preferred for exactly that reason: no bespoke per-backend client
code in the provider, and the secret never lands in state.

## Local development setup

Tested on a Mac laptop.

```
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

```
brew install go
```

```
brew tap hashicorp/tap
brew install hashicorp/tap/terraform
```

```
brew install git
```

```
brew install node
```

```
npm install -g @anthropic-ai/claude-code
```

### VS Code extensions

- Go — ID: `golang.go` (publisher: Go Team at Google)
- HashiCorp Terraform — ID: `hashicorp.terraform` (publisher: HashiCorp)
- HashiCorp HCL — ID: `hashicorp.hcl`
- GitLens — ID: `eamodio.gitlens`
- GitDoc — ID: `vsls-contrib.gitdoc` (publisher: Jonathan Carter / vsls-contrib)

GitDoc auto-commit settings for this repo live in [.vscode/settings.json](.vscode/settings.json).
