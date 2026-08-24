# Sample code

A minimal, runnable config that exercises the `lucidity` provider **pulled
from the Terraform Registry** — not a local dev-override build. Clone this
repo on any machine, set one env var, and it works the same way everywhere.

## Prerequisites

- Terraform >= 1.0
- A Lucidity refresh token (Lucidity dashboard → Users → your admin user →
  Generate Token)
- The provider published on the Terraform Registry at
  `registry.terraform.io/Devaansh/lucidity` (see the repo root's
  `RELEASING.md` for how releases get published there)

## Usage

```bash
export LUCIDITY_REFRESH_TOKEN="your-refresh-token"
terraform init
terraform plan
```

## Known limitation (Phase 1)

The provider currently ships auth only — no `lucidity_tenant` resource or
`lucidity_tenants` data source yet. Terraform prunes a provider from the plan
graph when nothing in the config references it, so `terraform plan` here
mainly confirms the config parses and the provider is resolvable from the
Registry; it does not yet exercise the auth flow end-to-end. That changes
once Phase 2 adds a resource (see the repo root's `CLAUDE.md`) — this sample
will grow a real resource block at that point.
