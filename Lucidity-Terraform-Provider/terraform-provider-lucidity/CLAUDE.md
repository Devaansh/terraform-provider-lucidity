# terraform-provider-lucidity

Terraform provider for Lucidity (cloud storage optimization platform). Goal: publish
to the public Terraform Registry as a community provider, built to Partner-tier
quality. All design decisions below are FINAL from the planning phase — do not
revisit them without asking the maintainer.

## Project facts

- **Language/stack:** Go + Terraform Plugin Framework (NOT legacy SDKv2).
- **License:** MPL-2.0.
- **Publishing:** community provider from the maintainer's personal GitHub repo.
  Releases signed with a dedicated GPG key held by the maintainer.
- **API docs:** `docs/api/` contains the three Lucidity API reference PDFs
  (auth refresh, getting started, Public Tenant API). Treat these as the source
  of truth for endpoints, fields, and error tables.

## Current scope — PHASE 1: AUTH ONLY

Build ONLY the auth layer now. Tenant resource/data source come in Phase 2,
after Lucidity releases two new display-name update APIs (~1 week away).
Phase 1 definition of done:
- `terraform plan` with an empty config + provider block succeeds against sandbox.
  **Caveat (discovered 2026-08-21):** Terraform prunes a provider from the
  plan graph when nothing references it, so with zero resources/data sources
  this never actually calls `ValidateProviderConfig`/`ConfigureProvider` —
  it would "succeed" even with broken auth wiring. Real coverage of
  `ConfigValidators`/`Configure` comes from driving the `tfprotov6.ProviderServer`
  RPCs directly in `internal/provider/provider_test.go`, not from this check.
  This gap closes naturally once Phase 2 adds a resource/data source.
- Gated smoke test (`TF_ACC=1`) performs one authenticated `GET /external/client/api/v1/tenants`.
- CI green (build + unit tests on PR).

### Phase 1 deliverables

1. Repo scaffold: Go module, Plugin Framework wiring, MPL-2.0 LICENSE,
   GitHub Actions CI, GoReleaser config (GPG signing; include RELEASING.md
   with dedicated-key generation instructions).
2. `internal/client/auth.go` — token manager:
   - Exchanges long-lived refresh token via `POST /external/api/v1/auth/user-token/refresh`.
   - Access tokens expire in 15 min → proactive refresh at the 12-minute mark.
   - Mutex-protected single-flight refresh (Terraform runs concurrent ops).
   - On 401: force one refresh + one retry; if still 401, surface error.
   - Access tokens live in memory only. Never persisted, never logged.
3. `internal/client/client.go` — HTTP client:
   - Required headers on EVERY call: `X-Authtype: lucidity_access_token`,
     `Authorization: <raw token>` (NO "Bearer " prefix — enforce in one place),
     `Content-Type: application/json`.
   - Parse standard envelope `{success, data, error{code,message}, requestId}`.
     All error diagnostics MUST include error.code, error.message, requestId.
   - Retry: 500s with exponential backoff; NEVER auto-retry 400s.
     CONFLICT semantics unconfirmed — render unmapped codes generically but completely.
   - Client-side concurrency semaphore, default 10, from provider config.
   - Secret scrubbing: refresh + access tokens redacted from ALL logging
     including TF_LOG=DEBUG. Add a test that greps captured debug output
     for token material.
4. Provider config block:
   ```hcl
   provider "lucidity" {
     refresh_token         = "…"  # Sensitive, discouraged (ends up in .tf/state)
     refresh_token_file    = "…"  # Path to a file containing only the token
     refresh_token_command = "…"  # Shell command; trimmed stdout is used as the token
     base_url              = "…"  # optional, default https://dash-back.lucidity.dev
     max_parallel_requests = 10   # optional
     account_name          = "…"  # optional; reserved for Phase 2 update APIs
   }
   ```
   **Token-source precedence (locked 2026-08-20):** exactly one of
   `refresh_token` / `refresh_token_file` / `refresh_token_command` may be set.
   A `ConfigValidator` MUST hard-error at validate time if more than one is
   set, naming all attributes that were set. If none of the three are set,
   fall back to the `LUCIDITY_REFRESH_TOKEN` env var. If nothing resolves a
   token at all, error naming all three attributes and the env var.

   - `refresh_token_file`: read the file, trim trailing whitespace/newline.
     Missing/unreadable file → error naming the path (never its contents).
   - `refresh_token_command`: run via `sh -c` (unix) / `cmd /C` (windows) so
     pipelines and env expansion work as users expect (mirrors AWS CLI's
     `credential_process` / kubectl exec-auth pattern). Trim trailing
     whitespace/newline from stdout. Enforce a 30s timeout. Non-zero exit →
     surface stderr in the diagnostic. Never log stdout (it's the secret) —
     same scrubbing rule as access/refresh tokens elsewhere.
   - Malformed `base_url` caught before any API call. Base URLs vary by
     deployment (5 known, see Getting Started PDF) — never hardcode beyond
     the default.

   Rationale: rather than the provider baking in bespoke Vault/AWS-SM/
   Azure-KV/GCP-SM client integrations (real maintenance surface for a
   community provider), `refresh_token_command` is one generic escape hatch —
   docs show recipes like `vault kv get -field=token secret/lucidity` per
   backend. Fetching a secret via a Terraform *data source* and feeding it
   into `refresh_token` still works, but the docs must call out that the
   fetched value then lands in state as that data source's attribute (only
   as safe as your state encryption).
5. Unit tests against a local mock server replaying recorded envelope responses:
   token refresh, proactive renewal, single-flight, 401-retry, expired refresh
   token message, scrubbing (must cover `refresh_token_command` stdout too).
   Plus: multiple-sources-set → validator error; file read failure; command
   non-zero exit; command timeout.
6. Provider index docs covering all credential-supply options: env var (CI
   default), `refresh_token_file`, `refresh_token_command` (with per-backend
   recipes for Vault/AWS SM/Azure KV/GCP SM), data-source-fed `refresh_token`
   (with state-encryption caveat), tfvars (discouraged, local only).
   Hardcoding in .tf: documented as never-do.

### Required error message (401 / expired refresh token)

> authentication failed (401). Check your refresh token — make sure it has not
> expired (default lifetime 30 days) and was not revoked. Generate a new token
> from the Lucidity dashboard (Users → your admin user → Generate Token).

## PHASE 2 (do NOT build yet) — locked design for the tenant resource

Everything below is decided; implement when the maintainer says Phase 2 starts.

### `lucidity_tenant` resource

- One resource per cloud account. Users group accounts by environment in
  locals and use for_each keyed by account_id (see examples/complete/).
- Computed attributes: `tenant_id`, `status`.
- Immutable (RequiresReplace, gated by protection below): `cloud_provider`,
  `cloud_provider_account_id`.
- In-place updatable via onboard re-trigger: role/policy names and equivalents.
  **One-change-at-a-time rule:** a plan-time ConfigValidator MUST error if more
  than one mutable config field changes in a single plan (Lucidity's re-trigger
  mechanism supports one change at a time; all other fields carried over
  verbatim from state).
- `display_name`: NEVER RequiresReplace under any circumstance. Until the
  update API ships, changing it is a plan-time ERROR telling the user the
  update API is coming. After: in-place update via the tenant-ID endpoint.

### Deboard safety (business-critical — deboarding is IRREVERSIBLE)

An INACTIVE tenant CANNOT be reactivated via API — only Lucidity support can
restore it. Deboarding an account with running services causes disruption.
Three-tier destroy behavior:

| Config | `terraform destroy` result |
|---|---|
| `account_delete_protection = true` (DEFAULT) | Hard error before any API call |
| protection=false, `destroy_behavior = "forget"` (default) | Remove from state only; tenant stays ACTIVE; warning emitted |
| protection=false, `destroy_behavior = "deboard"` | Actual deboard call — the ONLY path to it |

Carry loud warnings in: registry docs (admonition block), attribute
descriptions, code comments above Delete(), and runtime diagnostics (even the
successful "forget" path warns). Tests must assert all three paths.

### INACTIVE handling

- Read: fetch full tenant list (no GET-by-ID exists), match on
  provider+account_id. ACTIVE → refresh state. INACTIVE (or DECOMMISSIONED —
  synonym per Lucidity, single code path) → KEEP in state, set status, emit
  ERROR with support-contact message and `terraform state rm` escape hatch.
  No match → remove from state with re-onboarding warning.
- Create: pre-check list for an INACTIVE entry with same account id → fail
  fast with:
  > cloud account <id> was previously deboarded and is INACTIVE on Lucidity.
  > Once a tenant is made inactive it cannot be made active again —
  > re-onboarding via API is not possible. Contact Lucidity support to
  > restore this account.

### Update APIs (specs from maintainer; confirm request shapes when released)

- "Update Tenant Friendly Name" (by current name): renames ALL tenants with
  that display name INCLUDING ones outside Terraform state → NEVER used by the
  resource. Go client only, documented for scripting.
- "Update Tenant Friendly Name With Tenant ID": inputs = Account (Lucidity
  dashboard account name, e.g. "customerA"), tenant/account id, new display
  name. This is what the resource uses. Requires provider `account_name`
  (open question: whether derivable from token — ask Lucidity).
- Environment rename = N independent per-tenant-ID calls; transient mixed
  dashboard state mid-apply is expected and fine.

### `skip_cloud_permission_check` attribute (optional, default false)

Doc note (maintainer's wording): "Only disable permission validation if using
a custom permission set or if your permission set is not yet up to date with
the latest Lucidity permissions. Note: this ignores permission validation
entirely — even if the account connects successfully, you may run into
permission issues later on."

### Import

`terraform import lucidity_tenant.x AWS/123456789012` (provider/account-id).
Imported resources get account_delete_protection=true regardless of config
until first apply.

### Data source `lucidity_tenants`

Wraps GET /external/client/api/v1/tenants; exposes status per tenant. Enables
the "desired vs actual" output pattern (Output 3 in planning).

## Testing conventions

- Unit tests: mock server, recorded envelopes, cover INACTIVE / NOT_FOUND /
  permission-failure / expired-token cases. Run on every PR.
- Acceptance tests (TF_ACC=1): maintainer's sandbox accounts, treated as live.
  AWS first; Azure/GCP schema support ships in v0.1.0 regardless.
- Destroy-path tests: protection-error and forget paths run freely; the
  actual-deboard test is separately tagged and run deliberately (sandbox
  tenants burn permanently on each deboard).

## Open questions (do not block Phase 1)

1. CONFLICT error-code semantics — maintainer chasing Lucidity.
2. Whether update APIs' `Account` param is derivable from token.
3. Onboard re-trigger response shape for existing ACTIVE tenant (201 vs 200?);
   skipCloudPermissionCheck semantics on re-trigger; failure atomicity.
4. Rate limits / parallel-onboard safety on Lucidity side.
5. Long-term: OIDC workload identity (proposal in docs/lucidity-oidc-proposal.md;
   future `use_oidc = true` provider mode).

## Style

- Comments explain WHY, especially around safety logic. The Delete() function
  gets the full business-critical warning block from the plan.
- Diagnostics are actionable: name the attribute/env var/next step, include
  requestId on API failures.
- Generate docs with tfplugindocs from schema descriptions + examples/.
