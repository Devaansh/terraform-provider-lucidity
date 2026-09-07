# terraform-provider-lucidity

Terraform provider for Lucidity: authentication (Phase 1) and the
`lucidity_tenant` resource / `lucidity_tenants` data source (Phase 2).
See [CLAUDE.md](CLAUDE.md) for the full design record and phase plan.

- `docs/api/` — Lucidity API reference PDFs (source of truth)
- `docs/lucidity-oidc-proposal.md` — OIDC proposal for the Lucidity team
- `docs/examples/lucidity-tenants.tf` — reference tenant-management configuration

This repo holds provider source only — no sample/testing Terraform configs.
Published at `registry.terraform.io/Devaansh/lucidity`; pull it from there to
try it out.

## Supplying the refresh token

The provider takes the refresh token via `refresh_token` / `refresh_token_file`
/ `refresh_token_command` (exactly one) or the `LUCIDITY_REFRESH_TOKEN` env
var — see `internal/provider/provider.go` for the full schema. `refresh_token_command`
is the one generic hook that covers every secret backend below without the
provider needing bespoke client code for each — prefer it over feeding
`refresh_token` from a data source (see the state-encryption caveat repeated
under each backend).

### AWS Secrets Manager

**Option 1 (recommended): `refresh_token_command`, via the AWS CLI**

```hcl
provider "lucidity" {
  lucidity_dashboard_url          = "https://www.web.lucidity.dev/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token_command           = "aws secretsmanager get-secret-value --secret-id lucidity/refresh-token --query SecretString --output text"
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
  lucidity_dashboard_url          = "https://www.web.lucidity.dev/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token                   = data.aws_secretsmanager_secret_version.lucidity_refresh_token.secret_string
}
```

Works, but the fetched value gets written into Terraform state as that data
source's attribute (every data source result is persisted to state, not just
resources) — only as safe as your state's encryption/access controls. Also
requires configuring the `aws` provider just for this lookup.

Option 1 is preferred for exactly that reason: no bespoke per-backend client
code in the provider, and the secret never lands in state.

### HashiCorp Vault

```hcl
provider "lucidity" {
  lucidity_dashboard_url          = "https://www.web.lucidity.dev/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token_command           = "vault kv get -field=token secret/lucidity"
}
```

Requires the Vault CLI installed wherever `terraform` runs, and Vault
authentication already established in that environment (`VAULT_ADDR` +
`VAULT_TOKEN`, AppRole, or whichever auth method your setup uses — the
provider just execs `vault`, it doesn't manage Vault auth itself), plus a
Vault policy granting `read` on that KV path.

### Azure Key Vault

**Option 1 (recommended): `refresh_token_command`, via the Azure CLI**

```hcl
provider "lucidity" {
  lucidity_dashboard_url          = "https://web-azurepls.lucidity.cloud/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token_command           = "az keyvault secret show --vault-name my-vault --name lucidity-refresh-token --query value -o tsv"
}
```

Requires the Azure CLI installed and authenticated (`az login`, a managed
identity, or a service principal already logged in) wherever `terraform`
runs, and the "Key Vault Secrets User" role (or a classic access policy with
`get` permission) on that secret.

**Option 2: `refresh_token`, fed from a data source**

```hcl
provider "azurerm" {
  features {}
}

data "azurerm_key_vault" "this" {
  name                = "my-vault"
  resource_group_name = "my-resource-group"
}

data "azurerm_key_vault_secret" "lucidity_refresh_token" {
  name         = "lucidity-refresh-token"
  key_vault_id = data.azurerm_key_vault.this.id
}

provider "lucidity" {
  lucidity_dashboard_url          = "https://web-azurepls.lucidity.cloud/dashboard"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token                   = data.azurerm_key_vault_secret.lucidity_refresh_token.value
}
```

Same state-encryption caveat as the AWS data-source option: the fetched
value is persisted to Terraform state as this data source's attribute.

### GCP Secret Manager

**Option 1 (recommended): `refresh_token_command`, via the gcloud CLI**

```hcl
provider "lucidity" {
  lucidity_dashboard_url          = "https://app.lucidity.cloud"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token_command           = "gcloud secrets versions access latest --secret=lucidity-refresh-token"
}
```

Requires the gcloud CLI installed and authenticated (Application Default
Credentials or a service account already active) wherever `terraform` runs,
and `roles/secretmanager.secretAccessor` on that secret.

**Option 2: `refresh_token`, fed from a data source**

```hcl
provider "google" {
  project = "my-project"
}

data "google_secret_manager_secret_version" "lucidity_refresh_token" {
  secret = "lucidity-refresh-token"
}

provider "lucidity" {
  lucidity_dashboard_url          = "https://app.lucidity.cloud"
  lucidity_dashboard_account_name = "Acme Corp"
  refresh_token                   = data.google_secret_manager_secret_version.lucidity_refresh_token.secret_data
}
```

Same state-encryption caveat as the other data-source options above.

### Never do this

- **Never hardcode the refresh token directly in a `.tf` file.** Even if you
  remove it later, it stays in your git history in plaintext forever.
- **Avoid `.tfvars` files for it.** If you must (e.g. no secret manager
  available), treat the `.tfvars` file exactly like the direct `refresh_token`
  attribute — same plaintext-on-disk risk — keep it out of version control
  (`.gitignore`), and prefer the `LUCIDITY_REFRESH_TOKEN` env var or one of
  the `refresh_token_command` recipes above instead.

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
