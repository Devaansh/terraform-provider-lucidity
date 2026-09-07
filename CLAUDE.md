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

## Current scope — PHASE 1 (auth) + PHASE 2 (tenant resource), both shipped

**Update (2026-09-07):** Phase 2 is now implemented — see the "PHASE 2"
section below for what shipped and what's still open (mainly: no live test
of a full onboard→update→deboard cycle against a real AWS account yet).
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
     **CONFLICT confirmed (2026-09-06): "Already a tenant exists with the
     provided details."** Not transient — never retry it (already the
     behavior, unchanged). This is a real, actionable business-logic state
     Phase 2's `lucidity_tenant` Create() needs to handle explicitly (likely
     the same pattern as the existing INACTIVE-entry pre-check below: a
     clear error naming the conflicting account rather than a generic
     APIError). Other still-unmapped codes continue to render generically
     but completely.
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
     lucidity_dashboard_url   = "…"  # REQUIRED, no default — must be one of the 5 known values below
     max_parallel_requests = 10   # optional, must be >= 1
     proactive_refresh_buffer_minutes = 3  # optional, 1-14, default 3 (renew at the 12-min mark)
     lucidity_dashboard_account_name = "…"  # REQUIRED (locked 2026-09-06, renamed 2026-09-07) — see below
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
   - `lucidity_dashboard_url` (locked 2026-09-03, renamed 2026-09-07 from
     `dashboard_login_url` for clarity — the "lucidity_" prefix matches
     `lucidity_dashboard_account_name` and makes it unambiguous this is a
     Lucidity-specific setting, not a generic one): **required, no default.**
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
     deployment-specific like `lucidity_dashboard_url`) — kept here for the
     record in case it's revisited.
   - `lucidity_dashboard_account_name` (locked 2026-09-06, renamed
     2026-09-07 from `account_name` for clarity — the old name was
     ambiguous against cloud/tenant account concepts): **required.** The
     maintainer wants `Configure()` to validate that the refresh token
     actually belongs to the declared account — specifically to catch
     "wrong refresh token from the wrong account" misconfiguration before
     any tenant operation runs. **Blocked on a real gap, not yet
     implementable:** live-testing on 2026-09-06 checked response headers
     and bodies across refresh/list/onboard calls and found no field or
     endpoint anywhere that identifies which dashboard account a token
     belongs to. The `lucidity_dashboard_account_name` attribute itself
     (required, plain string) can and does go in now; the actual
     cross-validation logic cannot be written until either Lucidity exposes
     such an endpoint or an alternative signal turns up. Track this as the
     top open item below, not a silently-dropped requirement.

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
   non-zero exit; command timeout; every `lucidity_dashboard_url` in the table
   maps to its correct `base_url`; an unrecognized or missing
   `lucidity_dashboard_url` is a validate-time error.
6. Provider index docs covering all credential-supply options: env var (CI
   default), `refresh_token_file`, `refresh_token_command` (with per-backend
   recipes for Vault/AWS SM/Azure KV/GCP SM), data-source-fed `refresh_token`
   (with state-encryption caveat), tfvars (discouraged, local only).
   Hardcoding in .tf: documented as never-do.
   **Done (2026-09-06):** all four backend recipes, both attribute- and
   data-source-fed options per backend, and the tfvars/hardcoding guidance
   are in the README's "Supplying the refresh token" section. Still missing:
   a real `tfplugindocs`-generated docs page — deferred with the `examples/`
   directory to Phase 2 (see the Style section note below).

### Required error message (401 / expired refresh token)

> authentication failed (401). Check your refresh token — make sure it has not
> expired (default lifetime 30 days) and was not revoked. Generate a new token
> from the Lucidity dashboard (Users → your admin user → Generate Token).

## PHASE 2 — tenant resource (implemented 2026-09-07)

Everything below is decided. **Implemented 2026-09-07:** `lucidity_tenant`
(Create/Read/Update/Delete/Import) and `lucidity_tenants` live in
`internal/provider/resource_tenant.go` / `datasource_tenants.go`, backed by
`internal/client/tenant.go`. Verified via mock-server unit tests
(`internal/client/tenant_test.go`, `internal/provider/resource_tenant_test.go`)
and `terraform validate`/`plan` against the real compiled schema with
`docs/examples/lucidity-tenants.tf` (dev-override, no live API calls — see
Open Question #6 below for what's still untested against a real account).
Onboard endpoint: `POST /external/client/api/v1/tenants/onboard` → `201
Created`. Deboard endpoint: `PUT /external/client/api/v1/tenants/deboard` →
`200 OK`. (List and Update's paths were already recorded above.)

### `lucidity_tenant` resource

- One resource per cloud account. Onboarding (Create) is AWS-only today
  (Azure/GCP return `400 INVALID_REQUEST`); List/Deboard/Update accept all
  three providers. See `docs/examples/lucidity-tenants.tf` for plain,
  non-abstracted example usage — one explicit resource block per account, no
  locals map/for_each (the maintainer explicitly rejected a JSON-like
  grouping structure here, twice, in favor of writing it "as per terraform").
- **Multi-cloud support (added 2026-09-07):** `cloud_provider` accepts `AWS`,
  `AZURE`, or `GCP` at the schema level. Since Create() only ever handles
  AWS, an AZURE/GCP resource can only enter Terraform via `terraform import`
  of a tenant that already exists on Lucidity some other way — Create()
  itself rejects a non-AWS `cloud_provider` outright with a clear error
  pointing at import, before making any API call. `cloud_provider_account_id`
  holds whichever identifier that provider uses: AWS account ID, Azure
  subscription ID (or name), or GCP project ID.
  - The AWS-only fields (`aws_iam_external_id`, `aws_iam_role_name`,
    `aws_iam_policy_name`, `lucidity_product_list`) are `Optional` at the
    schema level, not `Required` — a blanket `Required` would force every
    AZURE/GCP resource to fill in meaningless AWS IAM fields just to
    validate. Instead, a `ResourceWithValidateConfig.ValidateConfig`
    implementation enforces "if `cloud_provider == AWS`, these must be set"
    as a conditional invariant, checked on every plan.
  - New `azure_service_principal_id`/`azure_directory_id` fields (in
    `cloud_entity_information`, both optional, both updatable in-place) —
    match the real Update API's AZURE fields. Not usable at onboard time
    (Azure onboarding isn't supported); only meaningful for updating an
    imported AZURE tenant.
  - GCP's real Update API only accepts `displayName` — no GCP-specific auth
    fields exist to add.
- Computed attributes: `tenant_id`, `status`. New (2026-09-06):
  `cloud_entity_name` should also become computed — the provider-side
  account name, only available from List, not from onboard's response.
- Non-empty list attribute (**required for AWS only**, enforced via
  `ValidateConfig` — see above, not schema `Required`): `lucidity_product_list`
  (new field, not in earlier planning). Only `AUTOSCALER` is valid today —
  recommend validating it as a closed set the same way `lucidity_dashboard_url`
  is (`stringvalidator`-style), consistent with this project's established
  philosophy. Request field is `productList`; the onboard *response* field
  is `products` (different name) — don't conflate the two in Go struct tags.
  Not accepted by Update for any provider, and never returned by List, so it
  can't be recovered by import either.
- `aws_iam_external_id` (in `cloud_entity_information`, **required for AWS
  onboarding, `Optional` at the schema level** — enforced via `ValidateConfig`
  instead, so AZURE/GCP resources aren't forced to set it): a value the
  practitioner generates (a UUID
  works) and places in the target IAM role's trust policy; Lucidity sends it
  on every `AssumeRole`. **Write-only on the real API** — never returned by
  onboard, update, or list responses, and update silently keeps the
  onboarding-time value forever regardless of what's sent (see "Update
  APIs"). RequiresReplace: since this provider can never read back or verify
  the true server-side value, any config change is modeled as a full
  destroy+re-onboard rather than a silent no-op, which would risk state and
  the real IAM trust policy quietly disagreeing. **Known import gap:**
  `terraform import` cannot recover this value (nor can any Read); the
  practitioner must supply the real one matching the account's trust policy,
  or the very next apply forces a replace.
- `aws_root_account_id` (re-added 2026-09-07, **optional**, top-level attribute —
  sibling of `lucidity_dashboard_display_name`/`lucidity_product_list`, NOT nested inside
  `cloud_entity_information`, per the maintainer's explicit placement in the
  approved reference example): the AWS Organization root/management account
  ID for the account being onboarded. **Behavior changed 2026-09-07 per the
  maintainer's explicit instruction: now sent to Lucidity on onboard when
  set**, best-effort — it remains absent from both the onboard and update
  field tables in the current Public Tenant API doc (confirmed again
  2026-09-07 against the doc directly, not just the 2026-09-06 live-test
  notes), so Lucidity may silently ignore it or, if it validates request
  bodies strictly, reject the onboard call outright. This is a deliberate,
  flagged risk, not an oversight — nothing about the current doc suggests
  Lucidity accepts an extra field named `awsRootId`. **Not used for grouping
  or resolution** — the tenant is still identified purely by `cloud_provider`
  + `cloud_provider_account_id`. Useful for audits, and for cases where a
  shared IAM role/policy is assumed across multiple member accounts under
  the same org. **Cannot be modified once set:** there is no update
  mechanism for it (documented or otherwise, and it's not returned by List
  either), so `Update()` rejects a changed value with a plan-time error
  rather than silently dropping it or forcing a replace (replacing over a
  pure metadata field would mean an irreversible deboard/re-onboard cycle
  for no functional reason). A future release may add real update support if
  Lucidity ever documents a path for it.
- Immutable (RequiresReplace, gated by protection below): `cloud_provider`,
  `cloud_provider_account_id`, `aws_iam_external_id` (see above), `lucidity_product_list`
  (not listed as updatable in the real Update API's field table either —
  see "Update APIs").
- **Updates go through the real `PATCH /tenants` endpoint (see "Update
  APIs" below) — not onboard re-trigger.** Onboard is create-only; it does
  not support re-triggering at all (an existing tenant, ACTIVE or INACTIVE,
  always gets `409 CONFLICT`). The old "one-change-at-a-time" rule is
  dropped: `PATCH` is a normal partial-update endpoint with no stated
  restriction on how many fields you send in one call, and its old rationale
  (re-trigger only supported one field) no longer exists. `Update()` sends
  every changed field in a single `PATCH` call.
- `lucidity_dashboard_display_name`: NEVER RequiresReplace, updatable in-place immediately —
  the update API this was waiting on has shipped, so there's no "plan-time
  ERROR until the API ships" fallback path to build anymore.

### Deboard safety (business-critical — deboarding is IRREVERSIBLE)

An INACTIVE tenant CANNOT be reactivated via API — only Lucidity support can
restore it. Deboarding an account with running services causes disruption.
Confirmed by the current doc: deboard is idempotent (`200 OK` either way),
distinguishing `DE_BOARDED` (was active) from `ALREADY_DE_BOARDED` (no-op) in
the response message. Three-tier destroy behavior:

| Config | `terraform destroy` result |
|---|---|
| `lucidity_dashboard_account_delete_protection = true` (DEFAULT) | Hard error before any API call |
| protection=false, `lucidity_account_destroy_behavior = "forget"` (default) | Remove from state only; tenant stays ACTIVE; warning emitted |
| protection=false, `lucidity_account_destroy_behavior = "deboard"` | Actual deboard call — the ONLY path to it |

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

  **Locked 2026-09-06:** keep this pre-check even though onboard itself now
  natively returns `409 CONFLICT` for exactly this case too (distinct
  message: "An already deboarded (INACTIVE) tenant exists for
  cloudProviderAccountId '…'; re-onboarding support does not exist right
  now."). Maintainer's explicit call: the list-and-match pre-check stays as
  designed; onboard's native 409 is a defense-in-depth backstop for the race
  window between the pre-check and the actual onboard call, not a
  replacement for it. The *other* CONFLICT variant — "A tenant already
  exists for cloudProviderAccountId '…'" (tenant is ACTIVE, not INACTIVE) —
  needs the same explicit-error treatment in Create(), without the
  support-contact framing (an active duplicate isn't a Lucidity-support
  situation, it's a config error — most likely a `for_each` key collision).

### Update APIs — real design, replaces the old two-API assumption (rewritten 2026-09-06)

The actual API is a single unified endpoint, architecturally different from
what earlier planning assumed (a global by-name rename + a separate
tenant-ID-scoped one needing an "Account" param). That older description is
gone from current docs entirely — treated as superseded, not implemented.

- **Endpoint:** `PATCH /external/client/api/v1/tenants` → `200 OK`.
- Tenant identified by `cloudProvider` + `cloudProviderAccountId`, resolved
  under the caller's account from the token — same pattern as onboard/list/
  deboard. **No Account param anywhere in this API.** Tenant must be
  `ACTIVE`.
- Partial update: send only what changes. Must send `displayName` and/or at
  least one provider auth field, or the request is rejected.
- Cloud `authInfo` is **merged** — only the provider fields you send are
  overwritten; the rest of the existing `authInfo` is preserved.
- `externalId` can **never** be changed via update — the value from
  onboarding is kept forever regardless of what's sent.
- Per-provider updatable fields: **AWS** — `awsIAMRoleName`,
  `awsIAMPolicyName` (ARN rebuilt, existing `externalId` preserved);
  **AZURE** — `azureServicePrincipalId`, `azureDirectoryId` (merged into
  `authInfo`); **GCP** — `displayName` only.
- No re-trigger concept exists — see the `lucidity_tenant` resource bullets
  above.

### `skip_cloud_permission_check` attribute (optional, default false)

Doc note (maintainer's wording): "Only disable permission validation if using
a custom permission set or if your permission set is not yet up to date with
the latest Lucidity permissions. Note: this ignores permission validation
entirely — even if the account connects successfully, you may run into
permission issues later on."

**Live-tested 2026-09-06:** confirmed this only skips the *permission*
check — Lucidity still performs a baseline cloud-account-reachability
validation regardless of this flag. A synthetic/unreachable AWS account
number (tested twice, consistent) fails with `401 UNAUTHORIZED` /
"Authentication failed: the cloud account could not be validated." even
with `skipCloudPermissionCheck: true`. No orphaned tenant record is left
behind by a failed attempt. Create()'s error handling needs a case for this
401 distinctly from the "bad/expired access token" 401 — same HTTP status
and error code, different meaning, only distinguishable by message text.

### Import

`terraform import lucidity_tenant.x AWS/123456789012` (provider/account-id).
Imported resources get lucidity_dashboard_account_delete_protection=true regardless of config
until first apply.

**Known gap, implemented as designed rather than hidden:** `aws_iam_external_id` and
`lucidity_product_list` are never returned by List (write-only / not exposed at all),
so import cannot populate them — the practitioner must write a matching
resource block, and since both are RequiresReplace, a value that doesn't
match reality forces a destroy+recreate on the next apply rather than
drifting silently. `aws_iam_role_name`, `aws_iam_policy_name`, and
`lucidity_dashboard_display_name` reconcile safely instead: since they're real updatable
fields, a mismatch after import just triggers a normal `Update()` call on
the next apply. `ImportState` emits a warning listing all of this at import
time.

### Data source `lucidity_tenants`

Wraps `GET /external/client/api/v1/tenants`; response is `{tenants: [...],
meta: {totalCount}}` (object wrapper, not a bare array — deliberately
future-proofed for pagination without breaking clients, per the doc).
Results ordered ACTIVE first, then INACTIVE. Item fields: `tenantId`,
`cloudProvider`, `cloudProviderAccountId`, `cloudEntityName` (new,
provider-side account name), `displayName`, `status`. Exposes status per
tenant. Enables the "desired vs actual" output pattern (Output 3 in
planning). Live-confirmed 2026-09-06 against a real account (`LucidityPLS`)
— response shape matches this exactly.

### Live API testing notes (2026-09-06)

Tested against `LucidityPLS` (`dashboard-azurepls.lucidity.cloud`) using two
refresh tokens confirmed to belong to the same account (identical 3-tenant
list from both — a useful confirmation, though not a general account-
identity mechanism).

- List response shape, and all 10 invalid-onboard-input scenarios (missing
  `cloudEntityInformation`; blank `cloudProvider`/`cloudProviderAccountId`/
  `displayName`/`externalId`/`awsIAMRoleName`/`awsIAMPolicyName`; non-AWS
  provider; empty/invalid `productList`) — all confirmed matching the doc's
  `400 INVALID_REQUEST` behavior (one cosmetic wording difference: the
  invalid-product-value message is `"X" is not a supported product.` rather
  than the doc's paraphrase — same code/behavior).
- A rejected onboard attempt leaves no orphaned/partial tenant record.
- **Not yet tested:** a full real onboard→update→deboard→re-onboard-conflict
  cycle. Dummy AWS account numbers can't complete it — see the
  `skip_cloud_permission_check` note above. Deferred until a real,
  disposable AWS account with an actually-assumable IAM role is available.
- **No account-identity signal found anywhere** — checked response headers
  and bodies across every call made. Directly blocks implementing the
  `lucidity_dashboard_account_name` validation from the Phase 1 provider
  config block above.

## Testing conventions

- Unit tests: mock server, recorded envelopes, cover INACTIVE / NOT_FOUND /
  permission-failure / expired-token cases. Run on every PR.
- Acceptance tests (TF_ACC=1): maintainer's sandbox accounts, treated as live.
  AWS first (onboarding is AWS-only per the current API); Azure/GCP schema
  support (List/Deboard/Update already accept all three providers) ships in
  Phase 2 regardless of onboard's AWS-only limitation.
- Destroy-path tests: protection-error and forget paths run freely; the
  actual-deboard test is separately tagged and run deliberately (sandbox
  tenants burn permanently on each deboard).

## Open questions (do not block Phase 1)

1. ~~CONFLICT error-code semantics~~ **Resolved 2026-09-06:** "Already a
   tenant exists with the provided details." See the client.go bullet above.
2. ~~Whether update APIs' `Account` param is derivable from token~~
   **Resolved 2026-09-06:** moot — the real update API (`PATCH /tenants`)
   has no Account param at all; see "Update APIs" under Phase 2.
3. ~~Onboard re-trigger response shape for existing ACTIVE tenant~~
   **Resolved 2026-09-06:** no re-trigger exists — onboard is create-only,
   an existing tenant (ACTIVE or INACTIVE) always gets `409 CONFLICT`.
4. ~~Rate limits / parallel-onboard safety on Lucidity side~~ **Resolved
   2026-09-06:** 10 concurrent requests supported — already matches the
   existing `max_parallel_requests` default, no code change needed. Avoid
   two concurrent operations against the *same* account/tenant; Terraform's
   own per-resource-instance serialization already prevents this for normal
   `lucidity_tenant` usage.
5. **`lucidity_dashboard_account_name` validation mechanism** (new
   2026-09-06, renamed 2026-09-07): no endpoint found that identifies which
   dashboard account a token belongs to — checked response headers and
   bodies across refresh/list/onboard calls. Blocks implementing the
   cross-validation Configure() is supposed to do. Needs either a
   Lucidity-provided endpoint or an alternative signal.
6. **Full destructive-cycle confirmation** (new 2026-09-06): a real
   onboard→update→deboard→re-onboard-conflict cycle hasn't been exercised —
   dummy AWS account numbers fail cloud-account validation regardless of
   `skipCloudPermissionCheck`. Deferred until a real, disposable AWS account
   with an actually-assumable IAM role is available. **Note (2026-09-07):**
   the resource implementation itself is done and verified at the
   schema/plan level (`terraform validate`/`plan` against the compiled
   provider with the reference example, plus mock-server unit tests) — this
   item is specifically about confirming real API behavior end-to-end, not
   about whether the Go code exists.
7. **`aws_root_account_id` on the real onboard payload** (new 2026-09-07): sent
   best-effort per the maintainer's explicit instruction, but the current
   Public Tenant API doc has no such field in its onboard request table —
   unverified whether Lucidity silently ignores it, silently drops it, or
   rejects the whole call. Needs a live onboard test (blocked on the same
   real-AWS-account gap as #6) to know which.

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
