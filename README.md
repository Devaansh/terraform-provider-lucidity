# terraform-provider-lucidity

Terraform provider for Lucidity. Currently in Phase 1 (auth layer).
See [CLAUDE.md](CLAUDE.md) for the full design record and phase plan.

- `docs/api/` — Lucidity API reference PDFs (source of truth)
- `docs/lucidity-oidc-proposal.md` — OIDC proposal for the Lucidity team
- `docs/examples/lucidity-tenants.tf` — target end-user configuration pattern (Phase 2)

This repo holds provider source only — no sample/testing Terraform configs.
Published at `registry.terraform.io/Devaansh/lucidity`; pull it from there to
try it out.

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
