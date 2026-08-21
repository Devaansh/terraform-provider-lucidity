# Releasing

Releases are built and signed by GoReleaser, triggered by pushing a `v*` tag
(see [.github/workflows/release.yml](.github/workflows/release.yml)). This
doc covers the one-time setup and the steps for each release.

## One-time setup

### 1. Generate a dedicated release-signing GPG key

Per CLAUDE.md, this must be a **dedicated key for this provider's releases**
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

### 4. Confirm `go.mod`'s module path matches the publishing repo

`go.mod`'s module path was set to `github.com/Devaansh/terraform-provider-lucidity`
as a placeholder. Before the first release, confirm this matches the actual
GitHub repo the provider is published from (per CLAUDE.md: "community
provider from the maintainer's personal GitHub repo") — if it needs to
change, update `go.mod` and the `Address` in `main.go` together.

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
