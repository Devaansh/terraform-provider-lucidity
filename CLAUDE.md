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
   - Access tokens expire in 15 min (`client.AccessTokenTTL`) → proactive
     refresh at the 12-minute mark by default (`client.DefaultProactiveRefreshAge`),
     overridable via the provider's `proactive_refresh_buffer_minutes`
     attribute (see Provider config block below).
   - Mutex-protected single-flight refresh (Terraform runs concurrent ops).
   - On 401: force one refresh + one retry; if still 401, surface error. This
     retry is independent of the 5xx-backoff budget below — see bug note.
   - The refresh call itself retries 5xx with the same backoff policy as
     every other endpoint (fixed 2026-09-03 — previously `doRefresh` didn't
     retry at all, inconsistent with the policy below).
   - Access tokens live in memory only. Never persisted, never logged.
   - **Round 2 fixes (2026-09-05):**
     - `TokenManager` gained a `DebugLog` hook (wired from `Client.logDebug`
       in `NewClient`), logging `status`/`attempt` per refresh call —
       previously refresh-token exchanges produced zero `TF_LOG=DEBUG`
       output at all, unlike every other API call.
     - The single-flight refresh's initiating call now runs under
       `context.WithoutCancel(ctx)`. Previously it used whichever caller's
       context happened to start the refresh; that caller cancelling its own
       context (e.g. Terraform tearing down one operation) aborted the
       shared refresh for every other goroutine waiting on it too. Safe
       because `NewClient`'s `http.Client` already enforces its own 30s
       `Timeout` independent of context.
     - `ForceRefresh(ctx, failedToken)` now takes the token that actually
       got the 401 and skips the refetch if the cache no longer holds it
       (another goroutine already refreshed first) — returns that fresher
       token directly instead of an avoidable extra refresh call.
3. `internal/client/client.go` — HTTP client:
   - Required headers on EVERY call: `X-Authtype: lucidity_access_token`,
     `Authorization: <raw token>` (NO "Bearer " prefix — enforce in one place),
     `Content-Type: application/json`.
   - Parse standard envelope `{success, data, error{code,message}, requestId}`.
     All error diagnostics MUST include error.code, error.message, requestId.
   - Retry: 500s with exponential backoff; NEVER auto-retry 400s.
     CONFLICT semantics unconfirmed — render unmapped codes generically but completely.
   - **Bug fixed 2026-09-03:** the 5xx-backoff retries and the one sanctioned
     401-forced-refresh retry used to share one bounded attempt counter. If 3
     straight 500s consumed all-but-one attempt and the last attempt came
     back as a first-time 401, the forced refresh happened but the retry
     using the fresh token never did — fell through to a generic error
     instead of succeeding. Fixed by giving the 401 retry its own budget,
     independent of the 5xx counter (see
     `TestClient_401OnFinalRetryAttemptStillRetriesWithFreshToken`).
   - Client-side concurrency semaphore, default 10, from provider config.
     `max_parallel_requests` is validated (`AtLeast(1)`) — a 0/negative value
     is a config-time error, not a silent fallback to the default. Semaphore
     acquisition respects context cancellation (fixed 2026-09-05 — previously
     `c.sem <- struct{}{}` blocked unconditionally even with an
     already-cancelled/timed-out `ctx`).
   - Secret scrubbing: refresh + access tokens redacted from ALL logging
     including TF_LOG=DEBUG. Add a test that greps captured debug output
     for token material.
4. Provider config block:
   ```hcl
   provider "lucidity" {
     refresh_token         = "…"  # Sensitive, discouraged (ends up in .tf/state)
     refresh_token_file    = "…"  # Path to a file containing only the token
     refresh_token_command = "…"  # Shell command; trimmed stdout is used as the token
     dashboard_login_url   = "…"  # REQUIRED, no default — must be one of the 5 known values below
     max_parallel_requests = 10   # optional, must be >= 1
     proactive_refresh_buffer_minutes = 3  # optional, 1-14, default 3 (renew at the 12-min mark)
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
   - `dashboard_login_url` (locked 2026-09-03): **required, no default.**
     Replaces the old optional/free-form `base_url` entirely — the maintainer
     chose a closed, validated set over a string the user could mistype into
     a working-looking but wrong host. A `stringvalidator.OneOf` rejects
     anything outside the table below at validate time, before Configure
     ever runs; a missing value is also a validate-time error (Required, no
     default). The Dashboard Login URL → API Base URL mapping is defined in
     the provider itself (`internal/provider/deployment.go`), not read from
     user input:

     | Dashboard Login URL | API Base URL |
     |---|---|
     | `https://www.web.lucidity.dev/dashboard` | `https://dash-back.lucidity.dev` |
     | `https://web-azurepls.lucidity.cloud/dashboard` | `https://dashboard-azurepls.lucidity.cloud` |
     | `https://app.lucidity.cloud` | `https://app.lucidity.cloud` |
     | `https://in.app.lucidity.cloud` | `https://in.app.lucidity.cloud` |
     | `https://eu.app.lucidity.cloud` | `https://eu.app.lucidity.cloud` |

     Deliberate accepted trade-off: no free-form override attribute remains.
     A customer on a deployment not yet in this table can't configure the
     provider until a new release adds it — the maintainer chose closed
     validation over that escape hatch.
   - `proactive_refresh_buffer_minutes` (added 2026-09-03): optional Int64,
     `int64validator.Between(1, 14)`. How many minutes before the 15-minute
     access-token expiry (`client.AccessTokenTTL`) to proactively renew it.
     Unset → `client.DefaultProactiveRefreshAge` (3-minute buffer, i.e. renew
     at the 12-minute mark) — today's exact default behavior, unchanged.
     Deliberately made user-configurable per the maintainer's explicit call,
     overriding the initial recommendation to keep it an internal-only
     constant (the margin is a client-side implementation detail, not
     deployment-specific like `dashboard_login_url`) — kept here for the
     record in case it's revisited.

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
   non-zero exit; command timeout; every `dashboard_login_url` in the table
   maps to its correct `base_url`; an unrecognized or missing
   `dashboard_login_url` is a validate-time error.
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
  locals and use for_each keyed by account_id (see docs/examples/lucidity-tenants.tf).
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
  Note: the repo has no `examples/` directory right now — it was removed
  along with the Phase-2-only reference config (moved to
  `docs/examples/lucidity-tenants.tf`, plain documentation, not wired into
  any tooling). tfplugindocs' own convention (`examples/resources/<name>/resource.tf`,
  etc.) needs a real `examples/` directory recreated once Phase 2 adds
  `lucidity_tenant` — this note exists so that doesn't get missed.
