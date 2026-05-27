---
paths:
  - ".github/workflows/**/*.yml"
  - ".github/workflows/**/*.yaml"
description: Conventions and security best practices for GitHub Actions workflow files.
---

# GitHub Actions Workflow Conventions

Apply these whenever creating or editing any file under `.github/workflows/`.

## Naming & structure

- Every workflow has a top-level `name:`. Every job and every step has a clear `name:`.
- One workflow = one purpose (CI, release, lint). Don't overload a single file.
- Scope triggers tightly: list explicit `branches`/`tags` under `push`/`pull_request` rather than firing on everything.

## Permissions (least privilege)

- Set a restrictive default at the top of the file: `permissions: { contents: read }`.
- Grant elevation **per job**, not workflow-wide. Only the job that needs it gets it
  (e.g. the release job gets `permissions: { contents: write }`).
- Never put long-lived cloud credentials in secrets when OIDC is available — use
  `permissions: { id-token: write }` + the provider's OIDC login action instead.

## Action pinning (supply-chain safety)

- **GitHub-authored actions** (`actions/*`) and other well-known first parties may use a
  major-version tag: `actions/checkout@v4`, `actions/setup-go@v5`.
- **Third-party actions** must be pinned to a **full-length commit SHA**, with the human
  version in a trailing comment, e.g.:
  ```yaml
  uses: softprops/action-gh-release@<full-40-char-sha>  # v2.x.x
  ```
  A floating tag on a third-party action is a supply-chain risk and (since Aug 2025) may be
  rejected by org policy. Never use `@main`/`@master`.

## Secrets & inputs

- Reference secrets via `${{ secrets.NAME }}`; never hardcode tokens, keys, or URLs with creds.
- `GITHUB_TOKEN` is auto-provided — prefer it over PATs, and keep its scope minimal via `permissions`.
- Treat workflow inputs and `github.event.*` (PR titles, branch names) as untrusted: never
  interpolate them directly into a `run:` shell line — pass via `env:` and reference as `"$VAR"`.

## Reliability

- Add `timeout-minutes:` to every job to cap runaway runs.
- Add a `concurrency:` group (e.g. `group: ${{ github.workflow }}-${{ github.ref }}`,
  `cancel-in-progress: true`) so superseded runs are cancelled.
- Enable dependency caching (`actions/setup-go@v5` caches the module/build cache by default
  when a `go.sum` is present).
- Pin runner-installed toolchain versions explicitly (e.g. `go-version: '1.23'`) rather than `latest`.

## Go specifics

- `go-version` should match (or be ≥) the `go` directive in `go.mod`.
- Run `go test ./...` (with `-race` where feasible) before any build/release job; gate release on it (`needs:`).
- For release binaries, build with `-ldflags="-s -w"` to strip debug info, and publish a
  `.sha256` checksum alongside each artifact.
