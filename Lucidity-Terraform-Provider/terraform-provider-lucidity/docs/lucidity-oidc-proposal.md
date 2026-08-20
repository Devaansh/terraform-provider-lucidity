# Proposal: OIDC / Workload Identity Federation for the Lucidity Public API

**Audience:** Lucidity API & Platform team
**From:** [Your name], author of the Terraform provider for Lucidity
**Ask:** Add support for exchanging externally issued OIDC identity tokens for Lucidity access tokens, eliminating stored long-lived credentials for automated workloads.

---

## The problem today

Every automated consumer of the Lucidity Public API — CI/CD pipelines, Terraform, internal tooling — must hold a **long-lived refresh token** (30-day default). This is a static bearer secret that must be stored somewhere, rotated on a schedule, and can be exfiltrated from CI logs, state files, or compromised runners. When it leaks, it grants full API access for up to 30 days. When it silently expires, pipelines break.

AWS, Azure, and GCP have all moved automated access away from this model. Enterprise security reviews increasingly treat static API secrets as a finding. As Lucidity's API adoption grows — including through the upcoming Terraform provider — this will become the most common security objection raised by customers.

## The proposal

Allow Lucidity to accept **short-lived, cryptographically verifiable identity tokens** issued by identity providers the customer already trusts, and exchange them directly for a standard 15-minute Lucidity access token. No Lucidity secret is stored anywhere.

### Flow

1. **Federation configuration (dashboard, per account).** An Admin registers one or more trusted OIDC issuers and claim conditions, e.g.:
   - Issuer: `https://token.actions.githubusercontent.com`
   - Subject condition: `sub = repo:acme/infrastructure:ref:refs/heads/main`
   - Audience: `https://api.lucidity.dev`

   Common issuers customers will register: GitHub Actions, GitLab CI, HCP Terraform / Terraform Enterprise, and the AWS / Azure / GCP workload-identity issuers.

2. **Token exchange endpoint**, e.g. `POST /external/api/v1/auth/oidc/token`:
   - Request: the workload's IdP-signed JWT.
   - Lucidity validates the signature against the issuer's published JWKS, verifies `iss`, `aud`, `exp`, and the configured claim conditions.
   - Response: a standard 15-minute Lucidity access token — **everything downstream of this point is unchanged.**

3. **Optional: claim-to-permission scoping.** Map federated identities to restricted capabilities (e.g. "this pipeline may onboard tenants but never deboard"). Valuable, but separable — the core exchange is useful without it.

## What Lucidity gains

- **Zero customer-held secrets** for automated workloads: nothing to leak, nothing to rotate, nothing to expire mid-pipeline.
- **Per-workload audit trail:** the JWT `sub` claim identifies exactly which repository, branch, and pipeline run made every API call — far richer than "which refresh token was used."
- **Pattern parity with the clouds Lucidity manages:** this is the same model as AWS `AssumeRoleWithWebIdentity`, Azure federated credentials, and GCP Workload Identity Federation. Customers' platform teams already understand and prefer it.
- **Enterprise-readiness checkbox:** removes the most common security-review objection to API adoption.
- **Better Terraform story:** the provider gains a `use_oidc = true` mode requiring no credential configuration at all in CI — matching the experience of the official AWS/Azure/GCP providers.

## Implementation notes (Lucidity side)

- OIDC token validation is standard-library territory in every major backend language; the JWKS fetch-verify-cache pattern is well documented and widely implemented.
- No changes to existing auth: the refresh-token flow remains for interactive/legacy use. The new endpoint is purely additive and mints the same access tokens the platform already understands.
- Federation config is a small per-account data model: issuer URL, audience, one or more claim-match rules.
- Suggested rollout: (1) exchange endpoint with issuer allow-list, (2) dashboard UI for federation config, (3) optional claim-based scoping later.

## The ask

A conversation about scheduling this on the API roadmap. The Terraform provider will launch with refresh-token auth and can adopt OIDC in a minor release the moment the endpoint exists — happy to serve as a design reviewer and first integration tester.
