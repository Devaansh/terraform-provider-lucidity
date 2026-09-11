# Releasing

Releases are built and signed by GoReleaser, triggered by pushing a `v*` tag
(see [.github/workflows/release.yml](.github/workflows/release.yml)). This
doc covers the one-time setup and the steps for each release.

## One-time setup

### 1. Generate a dedicated release-signing GPG key

This must be a **dedicated key for this provider's releases**
— not the maintainer's personal GPG key. The Terraform Registry keeps this
key's public half on file to verify every release signature, so treat it as
project infrastructure, not a personal credential.

```bash
gpg --full-generate-key
```

- Key type: `RSA and RSA` (default)
- Key size: `4096`
- Expiration: your call — a 1-2 year expiry with a calendar reminder to
  rotate is reasonable; a non-expiring key is also fine if you're confident
  about long-term custody.
- Name: something identifying it as this provider's release key, e.g.
  `terraform-provider-lucidity releases`
- Email: an address you control and monitor
- Passphrase: required — GoReleaser needs it as `PASSPHRASE` below.

Get the key's fingerprint and export both halves:

```bash
gpg --list-secret-keys --keyid-format=long
# note the fingerprint on the "sec" line, e.g. 4096R/ABCDEF0123456789

gpg --armor --export-secret-key <fingerprint>  > private.pgp
gpg --armor --export <fingerprint>              > public.pgp
```

**`private.pgp` is sensitive.** Store it in your password manager or another
secure location, then delete the local copy once it's in GitHub Secrets
(step 3). Never commit it.

### 2. Register the public key with the Terraform Registry

Registry publishing verification needs the public key on file for your
account: Terraform Registry → your account → **Publish** → **GPG Keys** →
add `public.pgp`'s contents. See HashiCorp's ["Publishing Providers"](https://developer.hashicorp.com/terraform/registry/providers/publishing)
docs for the current flow.

### 3. Add GitHub repository secrets

Repo → Settings → Secrets and variables → Actions:

| Secret | Value |
|---|---|
| `GPG_PRIVATE_KEY` | contents of `private.pgp` |
| `PASSPHRASE` | the key's passphrase |

`GITHUB_TOKEN` is provided automatically by Actions — no setup needed.

### 4. Module path

`go.mod`'s module path (`github.com/Devaansh/terraform-provider-lucidity`)
matches the repo this provider is published from (renamed 2026-08-22 for
exactly this reason — the Terraform Registry requires the repo itself to be
named `terraform-provider-<name>` with the module at its root). If the repo
is ever renamed or transferred again, update `go.mod` and the `Address` in
`main.go` together.

## Cutting a release

1. Make sure `main` is green (CI passing) and everything you want released
   is merged.
2. Tag with a semver version, prefixed `v`:
   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```
3. The `release` workflow picks up the tag, runs GoReleaser, builds for all
   target platforms, signs the checksum file with the dedicated key, and
   publishes a GitHub Release with the binaries, checksums, signature, and
   `terraform-registry-manifest.json` attached.
4. First release only: go to the Terraform Registry and point it at this
   GitHub repo to start indexing releases. Subsequent tags are picked up
   automatically.
5. Verify the release shows up correctly on the Registry and that
   `terraform init` against a fresh config can resolve and download it.

## Local dry run

To sanity-check the GoReleaser config without publishing anything:

```bash
goreleaser release --snapshot --clean --skip=sign,publish
```

## Promoting to the official stable repo

`luciditycloud/lucidity` is the official-stable Registry namespace,
published from a separate repo,
[github.com/luciditycloud/terraform-provider-lucidity](https://github.com/luciditycloud/terraform-provider-lucidity).
This repo (`Devaansh/lucidity`) stays the beta channel and the only place
development happens; promoting a release to stable is a deliberate, manual
step from here, never automatic.

### One-time setup (per new stable repo, not per release)

1. **Push access:** create a fine-grained GitHub PAT scoped to just
   `luciditycloud/terraform-provider-lucidity` with **Contents: Read and
   write**, then add it as a secret named `LUCIDITYCLOUD_PUSH_TOKEN` in
   *this* repo's Settings → Secrets and variables → Actions (the promotion
   workflow runs from here, not from the stable repo).
2. **The stable repo's own release signing:** repeat steps 1-3 of this
   doc's "One-time setup" above, but for `luciditycloud/terraform-provider-lucidity`
   specifically — a dedicated GPG key (reusing this repo's key is fine, or
   generate a new one), its public half registered against the
   `luciditycloud` Registry account, and `GPG_PRIVATE_KEY`/`PASSPHRASE`
   added as secrets in the **stable repo**, not here. Without this, the tag
   push in the promotion below will still land but the stable repo's own
   `release.yml` run will fail at the signing step.
3. First promotion only: once a release has landed there, point the
   Terraform Registry at the stable repo (same "Publish" flow as step 2 of
   the main one-time setup) so it starts indexing.

### Promoting a release

1. Cut a normal release here first (see "Cutting a release" above) — `main`
   must be exactly at a version tag before promoting.
2. Actions tab → **Promote to luciditycloud stable** → Run workflow.
3. This (`.github/workflows/promote-to-stable.yml`) rewrites the Go module
   path and provider address from `Devaansh/lucidity` to
   `luciditycloud/lucidity` throughout the tree, drops `CLAUDE.md`,
   `RELEASING.md` (this file), and `.vscode/` since none of them belong in
   the stable repo, verifies the rewrite still builds and tests clean,
   regenerates the Registry docs,
   and force-pushes the result to the stable repo's `main` plus the same
   version tag — which fires that repo's own `ci.yml`/`release.yml` exactly
   as a normal push would (pushing with a PAT isn't subject to the
   `GITHUB_TOKEN` loop-prevention rule that blocks a workflow from
   triggering further workflows in the *same* repo).
4. Confirm the tag's `release.yml` run succeeds on the stable repo and the
   release appears there and (once indexed) on the Registry.
