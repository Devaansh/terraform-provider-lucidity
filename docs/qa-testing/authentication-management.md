# Live QA test log — Authentication management

**Section:** Authentication management (Phase 1) — the provider's shared
token-refresh and credential-sourcing layer (`internal/client/auth.go`),
used underneath every resource/data-source regardless of which section they
belong to. There's no resource/data-source of its own to test directly, so
these cases are exercised through whatever section-level operations are
available today (currently Account management's `lucidity_tenant`/
`lucidity_tenants`) — as more sections ship, this log stays the place for
auth-layer-specific findings rather than being duplicated per section. See
[README.md](README.md) for the full list of provider sections.

**Status legend:** `Not run` / `Pass` / `Fail` / `Blocked` (needs something
before it can run, noted in Notes).

**Prerequisites:** a live `LUCIDITY_REFRESH_TOKEN` for a real Lucidity
account (`Lucidity_Business` / `dash-back.lucidity.dev`); local Terraform
pointed at a locally-built provider binary via `dev_overrides` (no Registry
release had been cut yet at the time of this run).

## 5. Provider / auth sanity tests

| ID | Test case | Status | Date | Notes / evidence |
|---|---|---|---|---|
| 5.1 | Full apply cycle — end-to-end auth sanity check | Pass | 2026-09-10 | Real refresh token against `dash-back.lucidity.dev` (`Lucidity_Business` account) — token exchange, access token use, and retries all worked correctly through a full session of applies/imports (exercised via Account management operations). |
| 5.2 | Deliberately invalid/expired refresh token | Pass (nuance found) | 2026-09-11 | A garbage refresh token surfaces clearly at first API use: `"Invalid refresh token"`. **Nuance:** this specific failure mode returns `{message,status,data,trackingCode}` — a different envelope shape than the standard `{success,data,error,requestId}` — so it doesn't parse as `client.APIError` and falls through to a generic "unexpected response body" message rather than the provider's polished "authentication failed (401)" text (see `client.AuthFailedMessage`). That polished message is designed around a real, *expired* token's 401 response (standard envelope); a malformed/garbage token gets a 400 with a different shape instead. Not fixed — low priority, the underlying failure is still surfaced clearly, just less politely worded. |
| 5.3 | Long-running apply spanning the proactive refresh buffer | Pass | 2026-09-12 | A normal `terraform plan`/`apply` can't exercise this — each CLI invocation is a fresh process, so the token-age clock never accumulates past a few seconds. Tested instead with a small throwaway Go harness (not committed) holding one real `client.Client` alive for ~18 minutes, calling `ListTenants` every ~90s. Confirmed: the proactive refresh fired at `12m5s` elapsed — exactly the default 12-minute threshold (`DefaultProactiveRefreshAge` = 15min − 3min) — and every call from `13m35s` through `18m7s` (well past the original token's 15-minute hard expiry) kept succeeding on the new token. Zero failures across the full run. |
