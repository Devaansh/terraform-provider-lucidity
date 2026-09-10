# Live QA test log — `lucidity_tenant` / `lucidity_tenants`

Tracks execution of the live test matrix against real AWS accounts and a
real Lucidity account. Separate from [CHANGELOG.md](CHANGELOG.md) (what
shipped) and [CLAUDE.md](CLAUDE.md) (the design record) — this file is
purely a run log. Once a test resolves an open question or uncovers new
behavior, that finding gets folded into CLAUDE.md's "Live API testing
notes" section; this file just tracks whether/when/how each case was run.

**Status legend:** `Not run` / `Pass` / `Fail` / `Blocked` (needs something
before it can run, noted in Notes).

**Prerequisites — done as of 2026-09-10:**
- ~~A live `LUCIDITY_REFRESH_TOKEN`~~ — have one, for the `Lucidity_Business`
  account (`dash-back.lucidity.dev`).
- ~~Local Terraform pointed at a locally-built provider binary via
  `dev_overrides`~~ — working (no release has been cut yet — see CLAUDE.md).
- ~~`LucidityRole`/`LucidityPolicy` on `testaccount1`~~ — added.
- **New:** the account pool grew to 6: pool1–pool5 plus `testaccount1`.
  pool4/pool5 sit under two new AWS Organizations OUs (`Lucidity-QA-Alpha`,
  `Lucidity-QA-Beta`) rather than directly under root — see
  `aws-terraform-account-creation`'s `lucidity_test_accounts.tf`.

## Execution order

Run top-to-bottom within each phase; phases are ordered by dependency, not
by the section numbers below (e.g. an update test needs something already
onboarded).

1. ~~**Phase 0 — setup**~~ Done — see Prerequisites above.
2. **Phase 1 — concurrency first:** 4.1, onboarding pool1/pool2/pool3
   together via `concurrency/main.tf`'s own state — do this *before*
   anything else touches those accounts, since it's a separate state and
   would otherwise hit the ACTIVE-duplicate conflict against an
   already-onboarded pool1/pool2.
3. **Phase 2 — import pool1 into the main config:** 2.1, via
   `terraform import` — continues the sequence on pool1 from the main
   `lucidity_tenant.tf` config rather than re-onboarding it.
4. **Phase 3 — remaining onboard tests not requiring a fresh account:**
   1.1.3–1.1.15, 1.1.17 (against testaccount1/spare accounts as needed;
   1.1.1/1.1.2 are effectively covered by how 4.1 onboarded pool1/pool2).
5. **Phase 4 — modify:** 1.2.1–1.2.17, run against pool1 (imported in
   Phase 2).
6. **Phase 5 — data source:** 3.1–3.3.
7. **Phase 6 — duplicate-onboard conflict:** 1.1.16, against pool1 while
   still ACTIVE.
8. **Phase 7 — deboard and everything downstream:** 1.3.1–1.3.9 (pool3 is
   the account earmarked to actually end up permanently INACTIVE).
9. **Phase 8 — remaining import tests:** 2.2–2.9.
10. **Phase 9 — auth edge cases:** 5.2–5.3 (5.3 is long-running — schedule
    it deliberately, not back-to-back with everything else).

## 1. Basic functionality tests

### 1.1 Onboard account tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 1.1.1 | Onboard with `aws_org_root_id` set | Not run | | |
| 1.1.2 | Onboard without `aws_org_root_id` | Pass | 2026-09-10 | Onboarded pool1 (810100779479) against `Lucidity_Business`. Needed `skip_cloud_permission_check=true` to get past a new, undocumented `500 INTERNAL_ERROR` — see 1.1.5/1.1.6 and CLAUDE.md Open Q8. |
| 1.1.3 | Onboard with a dummy/nonexistent AWS account number | Not run | | |
| 1.1.4 | Onboard with a real account number but no IAM role/policy created | Not run | | |
| 1.1.5 | Onboard with real account + role created, incorrect permissions, `skip_cloud_permission_check=false` | Fail (unexpected) | 2026-09-10 | Tested with *correct* permissions (real `LucidityPolicy`), not incorrect ones — got `500 INTERNAL_ERROR` "Tenant PermissionCheck Failed" instead of the documented behavior either way. New, undocumented failure mode — CLAUDE.md Open Q8. Reproduced twice, not transient. |
| 1.1.6 | Onboard with real account + role created, incorrect permissions, `skip_cloud_permission_check=true` | Pass | 2026-09-10 | Confirmed this flag bypasses the 1.1.5 failure specifically — onboard proceeded to a real `201 Created`. Still tested against *correct* permissions, not incorrect — the "incorrect permissions" half of this case is still open. |
| 1.1.7 | Onboard with a real, correctly-permissioned role and policy (happy path) | Blocked | 2026-09-10 | Blocked by the same Open Q8 failure — a real, correctly-permissioned role hit `500 INTERNAL_ERROR` with the permission check enabled (default). Only succeeded with `skip_cloud_permission_check=true`, which isn't the intended happy path. Also surfaced and fixed a real bug: the follow-up list-and-match had no retry tolerance for a propagation delay, so this successful onboard initially came back as a Terraform-level failure with the tenant left untracked (recovered via `terraform import`; see `internal/provider/resource_tenant.go`'s `refreshFromList`). |
| 1.1.8 | Onboard with `aws_iam_role_name` pointing at a role name that doesn't exist | Not run | | |
| 1.1.9 | Onboard with a mismatched `aws_iam_external_id` (doesn't match trust policy) | Not run | | |
| 1.1.10 | Onboard with `aws_iam_external_id` in a non-UUID format | Not run | | |
| 1.1.11 | Onboard with `aws_iam_external_id` blank/whitespace | Not run | | |
| 1.1.12 | Onboard with `lucidity_dashboard_display_name` blank/empty | Not run | | |
| 1.1.13 | Onboard with an AWS account number in an invalid format | Not run | | |
| 1.1.14 | Onboard where role/policy name casing doesn't match the real IAM resource | Not run | | |
| 1.1.15 | Onboard an account ID belonging to an unrelated AWS Organization | Not run | | |
| 1.1.16 | Onboard the same account a second time while still ACTIVE | Not run | | |
| 1.1.17 | Onboard with `cloud_provider = AZURE` or `GCP` | Not run | | |

### 1.2 Modify (update) tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 1.2.1 | Modify role name to a dummy/nonexistent role | Not run | | |
| 1.2.2 | Modify role name to an existing role with incorrect permissions | Not run | | |
| 1.2.3 | Modify role name to an existing role with correct permissions | Not run | | |
| 1.2.4 | Modify policy name to a dummy/nonexistent policy | Not run | | |
| 1.2.5 | Modify policy name to an existing policy with incorrect permissions | Not run | | |
| 1.2.6 | Modify policy name to an existing policy with correct permissions | Not run | | |
| 1.2.7 | Modify display name only | Not run | | |
| 1.2.8 | Modify display name to a blank/empty string | Not run | | |
| 1.2.9 | Modify role name, policy name, and display name together | Not run | | |
| 1.2.10 | Modify role name + policy name where only one new value is valid | Not run | | |
| 1.2.11 | No-op apply (no changes) | Not run | | |
| 1.2.12 | Attempt to modify `aws_org_root_id` | Not run | | Covered by unit + live-mock tests already; live run is a sanity check |
| 1.2.13 | Attempt to modify `lucidity_product_list` | Not run | | |
| 1.2.14 | Attempt to modify `aws_iam_external_id` | Not run | | |
| 1.2.15 | Attempt to modify `cloud_provider_account_id` | Not run | | |
| 1.2.16 | Attempt to modify `cloud_provider` | Not run | | |
| 1.2.17 | Attempt to modify a tenant that is not ACTIVE | Not run | | Needs 1.3.x done first |

### 1.3 Account inactive/deboard test cases

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 1.3.1 | Destroy with `account_delete_protection=true` (default) | Not run | | |
| 1.3.2 | Destroy with protection off, `destroy_behavior="forget"` | Not run | | |
| 1.3.3 | Destroy with protection off, `destroy_behavior="deboard"` | Not run | | |
| 1.3.4 | Deboard an already-INACTIVE tenant | Not run | | |
| 1.3.5 | Attempt to re-onboard an INACTIVE account | Not run | | Resolves Open Q6 |
| 1.3.6 | Deboard out-of-band, then `terraform plan`/refresh | Not run | | |
| 1.3.7 | Attempt an update immediately after an out-of-band deboard | Not run | | |
| 1.3.8 | Confirm `lucidity_tenants` reflects INACTIVE after deboard | Not run | | |
| 1.3.9 | Confirm AWS-side IAM role/policy untouched after deboard | Not run | | |

## 2. Terraform import test cases

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 2.1 | Import an existing ACTIVE tenant | Pass | 2026-09-10 | Imported pool1's tenant (onboarded out-of-band relative to the local config, per 1.1.7's aborted apply) via `terraform import lucidity_tenant.under_test AWS/810100779479`. Warning message matched design exactly. |
| 2.2 | Import with a malformed import ID | Not run | | |
| 2.3 | Import with the cloud provider in the wrong case | Not run | | |
| 2.4 | Import with extraneous whitespace in the ID string | Not run | | |
| 2.5 | Import an account/tenant combination that doesn't exist | Not run | | |
| 2.6 | Import an INACTIVE tenant | Not run | | |
| 2.7 | Import the same resource address twice | Not run | | |
| 2.8 | Import, then apply with mismatched `external_id`/`product_list` | Pass | 2026-09-10 | After 2.1's import, `plan` against the real original values (which import couldn't recover) correctly showed a forced replace — exactly as designed, since state had them null post-import. Did not actually apply (would attempt a real destroy — safely blocked by `account_delete_protection=true` anyway, but not worth exercising for real). |
| 2.9 | Import, then write a config missing required AWS-only fields | Not run | | |

## 3. Data source tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 3.1 | `lucidity_tenants` with mixed ACTIVE/INACTIVE tenants | Pass | 2026-09-10 | Real `Lucidity_Business` account had 8 pre-existing tenants (AWS/AZURE/GCP), all INACTIVE, plus pool1 ACTIVE after 1.1.7/2.1 — all listed correctly. |
| 3.2 | `lucidity_tenants` field-by-field accuracy check | Pass | 2026-09-10 | Fields matched the real dashboard data (`cloud_entity_name`, `display_name`, `status`, provider) for all 9 tenants observed. |
| 3.3 | Cross-reference data source vs. resource in the same config | Not run | | |

## 4. Concurrency tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 4.1 | Onboard multiple accounts in a single `apply` | Not run | | |
| 4.2 | (Stretch) Race two concurrent applies against the same new account | Not run | | Optional |

## 5. Provider / auth sanity tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 5.1 | Full apply cycle — end-to-end auth sanity check | Pass | 2026-09-10 | Real refresh token against `dash-back.lucidity.dev` (`Lucidity_Business` account) — token exchange, access token use, and retries all worked correctly through a full session of applies/imports. |
| 5.2 | Deliberately invalid/expired refresh token | Not run | | |
| 5.3 | Long-running apply spanning the proactive refresh buffer | Not run | | Schedule deliberately — needs 15+ min wall clock |
