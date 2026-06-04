# microstore

**microstore** is a single-binary, local "app store" for Go micro-apps. It browses a GitHub-hosted
catalog, installs the right release binary for your OS/architecture with SHA-256 verification, and
tracks, updates, re-verifies, and uninstalls what you've installed — plus scaffolds new micro-apps
from templates and can drop an embedded Claude Code starter kit into any directory
(`microstore init`).

One shared domain, three faces:

- a **tview terminal UI** for interactive browsing and management,
- an **MCP stdio server** so LLM clients can drive the same use-cases,
- an **embedded bbolt database** — no server, no cgo, just one local data file.

microstore is online-only — the catalog is always fetched live; only your installs and the app's
own config are persisted. See [`docs/SPECIFICATIONS.md`](docs/SPECIFICATIONS.md) for the full
product contract.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Techthos/microstore/refs/heads/main/scripts/install.sh | bash
```

The installer detects your OS/arch, downloads the latest release binary, verifies its SHA-256
against the `.sha256` sidecar, and installs it as `microapp-store` into
`~/.local/share/microstore/bin` (warning if that directory is not on your `PATH`).

Environment overrides:

| Variable | Default | Purpose |
|---|---|---|
| `MICROSTORE_VERSION` | `latest` | Release tag to install (e.g. `v0.2.0`) |
| `MICROSTORE_INSTALL_DIR` | `~/.local/share/microstore/bin` | Target directory |
| `MICROSTORE_REPO` | `Techthos/microstore` | `owner/name` to install from |
| `MICROSTORE_GITHUB_TOKEN` | — | Token for private repos / higher rate limits (`GITHUB_TOKEN` also honored) |

## Prerequisites

- **Go** (toolchain that satisfies `go.mod`).
- Optional, for `make fmt` / `make lint`:
  ```sh
  go install mvdan.cc/gofumpt@latest
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
  ```
  `make` reports which are missing rather than failing silently.

## Getting started

```sh
make build    # go build ./...
make run      # go run .
make test     # go test ./... -race -cover
make fmt      # gofumpt -w .
make lint     # golangci-lint run
make tidy     # go mod tidy
make check    # fmt + tidy + lint + test, in sequence
```

Run a single test:

```sh
go test ./... -run TestName -race -v
```

## Configuration

- `MICROSTORE_GITHUB_TOKEN` — optional GitHub token; raises rate limits and enables private repos.
  Anonymous access is used when unset.

## Layout

This repo starts flat (`main.go`) and grows `internal/` packages as the spec is implemented:

```
models  ←  db  ←  server
            ↑
           tui
```

`internal/models` is storage-agnostic; `internal/db` is the only package that touches bbolt; both
`internal/server` (MCP) and `internal/tui` go through `internal/db`. See `CLAUDE.md` and
`.claude/rules/` for the layer rules.

---

Built and maintained by [Techthos](https://www.techthos.net).
