# Live QA test logs — index

The provider is organized into functional **sections**, matching Lucidity's
own feature areas rather than Terraform's resource/data-source boundaries.
Each section that has shipped gets its own live QA test log here, tracking
execution of its test matrix against real accounts (AWS + a real Lucidity
account) — separate from [CHANGELOG.md](../../CHANGELOG.md) (what shipped).
A section's log is a pure run log: once a test resolves an open question or
uncovers new behavior, that finding gets folded into the relevant section's
notes; the log here just tracks whether/when/how each case was run.

**Status legend** (used by every section's log): `Not run` / `Pass` / `Fail`
/ `Blocked` (needs something before it can run, noted in that row's Notes).

## Sections

| Section | Status | Log |
|---|---|---|
| Authentication management | Shipped (Phase 1) | [authentication-management.md](authentication-management.md) |
| Account management | Shipped (Phase 2) | [account-management.md](account-management.md) |
| User management | Not yet implemented | — |
| Permission validation (cloud IAM vs. Lucidity requirements) | Not yet implemented | — |
| Agent install and status validation | Not yet implemented | — |
| Buffer policy management | Not yet implemented | — |
| Temporary buffer management | Not yet implemented | — |
| Onboarding and Disk management | Not yet implemented | — |

When a new section ships, add its log here as `docs/qa-testing/<section-slug>.md`
following the same conventions as the two existing logs (status legend,
prerequisites, execution order, numbered test-case tables with ID/test
case/status/date/notes columns), and add its row above with a link.
