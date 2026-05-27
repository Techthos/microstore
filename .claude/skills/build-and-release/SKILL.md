---
name: build-and-release
description: Generate a Go CI + cross-platform release GitHub Actions workflow (.github/workflows/build-and-release.yml) that tests on push/PR and builds + publishes multi-OS/arch binaries with checksums on version tags.
argument-hint: [binary-name]
paths:
  - ".github/workflows/**/*.yml"
  - ".github/workflows/**/*.yaml"
allowed-tools: Bash(go *) Bash(ls *) Bash(cat go.mod) Bash(find *) Bash(grep *) Read Write Edit
---

# Build and Release workflow generator

Create `.github/workflows/build-and-release.yml` for a Go project: a `test` job on every
push/PR to `main`, and a matrix `release` job that cross-compiles and uploads binaries +
SHA-256 checksums to a GitHub Release on `v*` tags.

The conventions in `.claude/rules/github-actions.md` apply — follow them.

## Step 1 — Gather project facts (don't guess)

- **Binary name**: use `$ARGUMENTS` if given. Otherwise infer from the entry-point package
  directory under `cmd/` (`ls cmd/`). If there's exactly one, use its name. If ambiguous, ask me.
- **Build path**: the package dir holding `main` — e.g. `./cmd/<binary>`. If the module is a
  single root `main.go`, use `.`.
- **Go version**: read the `go` directive from `go.mod` and use that major.minor (e.g. `1.23`).
- **Module path**: from `go.mod` (for context only).

## Step 2 — Write the workflow

Use the template below, substituting `<BINARY>`, `<BUILD_PATH>`, and `<GO_VERSION>`.
Keep `actions/*` on major-version tags; pin third-party actions (softprops/action-gh-release)
to a full commit SHA with a version comment — look up the current release SHA, or if you can't,
leave a clear `# TODO: pin to SHA` note and tell me.

```yaml
name: Build and Release

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
    branches: [main]

# Least privilege by default; the release job elevates locally.
permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '<GO_VERSION>'

      - name: Download dependencies
        run: go mod download

      - name: Run tests
        run: go test -race -v ./...

      - name: Build
        run: go build -v <BUILD_PATH>

  release:
    name: Release
    needs: test
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    timeout-minutes: 15
    permissions:
      contents: write   # required to create the release / upload assets
    strategy:
      fail-fast: false
      matrix:
        goos: [linux, darwin, windows]
        goarch: [amd64, arm64]
        exclude:
          - goos: windows
            goarch: arm64
    steps:
      - name: Checkout code
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '<GO_VERSION>'

      - name: Get version
        id: version
        run: echo "VERSION=${GITHUB_REF#refs/tags/}" >> "$GITHUB_OUTPUT"

      - name: Build binary
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: '0'
          VERSION: ${{ steps.version.outputs.VERSION }}
        run: |
          BINARY_NAME="<BINARY>-${VERSION}-${GOOS}-${GOARCH}"
          [ "${GOOS}" = "windows" ] && BINARY_NAME="${BINARY_NAME}.exe"
          go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
            -o "${BINARY_NAME}" <BUILD_PATH>
          sha256sum "${BINARY_NAME}" > "${BINARY_NAME}.sha256"

      - name: Upload to release
        uses: softprops/action-gh-release@<SHA>  # v2 — pin to a full commit SHA
        with:
          files: |
            <BINARY>-${{ steps.version.outputs.VERSION }}-${{ matrix.goos }}-${{ matrix.goarch }}*
          draft: false
          prerelease: false
          generate_release_notes: true
```

## Step 3 — Verify & report

- Confirm the YAML parses (basic sanity / indentation) and that `<BUILD_PATH>` actually exists.
- Summarize: what binary name, build path, Go version, and platform matrix were used, and
  flag the softprops SHA pin as a TODO if you couldn't resolve it.
- Remind me that tagging triggers a release: `git tag v0.1.0 && git push origin v0.1.0`.

## Notes / improvements over a naive workflow

- `permissions: contents: read` default, elevated to `write` only on the release job.
- `concurrency` cancels superseded runs; `timeout-minutes` caps each job.
- `CGO_ENABLED=0` + `-trimpath` for reproducible, static cross-compiled binaries; `-X main.version`
  stamps the version. If the project uses cgo, drop `CGO_ENABLED=0` and narrow the matrix.
- `fail-fast: false` so one platform failing doesn't abort the others.
