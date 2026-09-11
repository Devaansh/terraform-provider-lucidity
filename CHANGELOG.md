# Changelog

All notable changes to this project are documented here. All entries below
are authored by **Devaansh Goenka**, the maintainer of this provider.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and
this project intends to follow [Semantic Versioning](https://semver.org/)
once it reaches a stable release cadence.

## [Unreleased]

_Author: Devaansh Goenka._ Everything below has landed on `main` since
v0.1.1 but has not yet been cut as a release.

### Added

- `lucidity_tenant` resource: full `Create`/`Read`/`Update`/`Delete`/
  `terraform import` lifecycle for managing a Lucidity tenant (a connected
  cloud account), backed by a new `internal/client/tenant.go` covering the
  real onboard/list/update/deboard endpoints.
- `lucidity_tenants` data source, listing every tenant under the caller's
  account.
- Azure and GCP support for `lucidity_tenant` via `terraform import` +
  update — `cloud_provider` now accepts `AWS`, `AZURE`, or `GCP`. Onboarding
  a brand-new resource remains AWS-only for now, since Lucidity's Azure/GCP
  onboarding API isn't complete yet (pending a future Lucidity release, not
  a permanent restriction). Added `azure_service_principal_id` and
  `azure_directory_id` fields for updating an already-imported AZURE tenant.
- Three-tier destroy safety for `lucidity_tenant`
  (`lucidity_dashboard_account_delete_protection` +
  `lucidity_account_destroy_behavior`), since deboarding a tenant is
  irreversible via API.
- `lucidity_dashboard_account_name` provider attribute (required), recording
  which Lucidity dashboard account a refresh token is expected to belong
  to. Cross-validation against the token itself is not yet implemented —
  no Lucidity endpoint currently exposes which account a token belongs to.
- Conditional field validation via a `ValidateConfig` implementation:
  AWS-only onboarding fields (`aws_iam_external_id`, `aws_iam_role_name`,
  `aws_iam_policy_name`, `lucidity_product_list`) are only required when
  `cloud_provider` is `AWS`, so an AZURE/GCP resource (import-only) doesn't
  need to set meaningless AWS IAM fields just to validate.
- `aws_org_root_id` is re-added as an optional `lucidity_tenant` attribute
  (sent to Lucidity on onboard best-effort, since it's absent from the
  documented onboard/update request schema). Changing it after creation is
  a `terraform plan`-time error via a `ModifyPlan` implementation, instead
  of only surfacing once `apply` reaches `Update()`. A future release of
  this provider may add support for modifying it in place, if and when
  Lucidity exposes an update mechanism for it.
- Generated Registry documentation (`docs/index.md`, `docs/resources/`,
  `docs/data-sources/`) via `tfplugindocs`, and a recreated `examples/`
  directory in its expected layout.
- A CI check that regenerates the docs and fails the build if they've
  drifted from the schema or `examples/`.

### Changed

- Renamed `dashboard_login_url` (provider) to `lucidity_dashboard_url`.
- Renamed several `lucidity_tenant` attributes for clarity — Terraform-side
  only; the underlying API request/response field names are unchanged:
  - `display_name` → `lucidity_dashboard_display_name`
  - `product_list` → `lucidity_product_list`
  - `external_id` → `aws_iam_external_id`
  - `account_delete_protection` → `lucidity_dashboard_account_delete_protection`
  - `destroy_behavior` → `lucidity_account_destroy_behavior`
  - `aws_root_id` → `aws_root_account_id` → `aws_org_root_id` (re-added as an
    optional attribute; sent to Lucidity on onboard best-effort even though
    it's absent from the documented onboard/update request schema)
- `aws_org_root_id`'s immutability check moved from `Update()` (an
  apply-time failure) to `ModifyPlan` (a plan-time failure), so an attempt
  to change it is caught by `terraform plan` rather than only once `apply`
  reaches `Update()`.

### Fixed

- A `401` response for "the cloud account could not be validated" (a
  business-logic failure distinct from a bad/expired access token, even
  though both share HTTP 401 and `error.code` `UNAUTHORIZED`) was
  previously masked behind the generic expired-refresh-token error message,
  losing the real error message and `requestId`. Now surfaced correctly.

### Research / design record

- Reconciled the Phase 2 tenant-resource design against the real Public
  Tenant API doc and live-tested against a real Lucidity account
  (2026-09-06): confirmed `CONFLICT` error-code semantics, confirmed the
  update API is a single unified `PATCH` endpoint (not the two-API split
  originally planned), confirmed Lucidity's rate limits, confirmed onboard
  is create-only.

## [0.1.1] - 2026-09-05

_Author: Devaansh Goenka._

### Changed

- Replaced the optional, free-form `base_url` provider attribute with a
  required `dashboard_login_url` (closed set of 5 known Lucidity
  deployments, validated at plan time) — a customer on a deployment not yet
  in the table needs a provider update rather than a typo-prone free-form
  string.
- Added a configurable `proactive_refresh_buffer_minutes` provider
  attribute (default 3 minutes before the 15-minute access-token expiry).
- Moved sample/testing Terraform configuration out of the repository.

### Fixed

- Fixed a retry-budget bug: the 5xx-backoff retries and the one sanctioned
  401-forced-refresh retry used to share one bounded attempt counter. If
  three straight `500`s consumed all-but-one attempt and the final attempt
  came back as a first-time `401`, the forced refresh happened but the
  retry using the fresh token never did — it fell through to a generic
  error instead of succeeding. Gave the 401 retry its own budget,
  independent of the 5xx counter.
- The refresh-token call itself now retries `5xx` with the same backoff
  policy as every other endpoint (previously it didn't retry at all).
- The single-flight refresh's initiating call now runs under
  `context.WithoutCancel`, so one caller cancelling its own context can no
  longer abort a shared refresh that other goroutines are waiting on.
- The concurrency semaphore now respects context cancellation instead of
  blocking unconditionally on an already-cancelled/timed-out context.
- `ForceRefresh` now skips an avoidable extra refresh call when another
  goroutine has already refreshed the token first.
- Refresh-token exchanges now produce `TF_LOG=DEBUG` output, matching every
  other API call (previously silent).

## [0.1.0] - 2026-08-24

_Author: Devaansh Goenka._ Initial release: Phase 1, authentication only.

### Added

- Repo scaffold: Go module, HashiCorp Terraform Plugin Framework wiring,
  MPL-2.0 license, GitHub Actions CI, GoReleaser configuration with
  dedicated GPG-key signing.
- Token manager (`internal/client/auth.go`): exchanges a long-lived refresh
  token for a 15-minute access token, proactively renews it ahead of
  expiry, single-flight refresh under concurrent Terraform operations, and
  forces exactly one refresh-and-retry on a `401` before surfacing an
  error.
- HTTP client (`internal/client/client.go`): required auth headers on every
  call, `{success, data, error, requestId}` envelope parsing, exponential
  backoff on `5xx` (never auto-retrying `4xx`), a client-side concurrency
  semaphore, and secret scrubbing so refresh/access tokens never appear in
  logs (including at `TF_LOG=DEBUG`).
- Provider configuration: `refresh_token` / `refresh_token_file` /
  `refresh_token_command` (exactly one, or the `LUCIDITY_REFRESH_TOKEN`
  environment variable) and `max_parallel_requests`.
- Unit tests against a local mock server covering token refresh, proactive
  renewal, single-flight behavior, 401-retry, and secret scrubbing.
