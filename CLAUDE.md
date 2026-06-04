# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**microstore** is a single-binary, local "app store" for Go micro-apps: it browses a GitHub-hosted
catalog (`catalog.json`), installs the right release binary for the host `GOOS/GOARCH` (with SHA-256
verification), tracks/updates/uninstalls/re-verifies those installs, and scaffolds new micro-apps
from templates — then hands off to `/product-idea`. It is **online-only** (no offline catalog
cache); bbolt persists only your installs and the app's own config.

It is itself an instance of the micro-app shape this repo also templates: three faces over one
shared domain —

- an **embedded bbolt database** (no server, no cgo) — its own data file,
- an **MCP server** over stdio (`mark3labs/mcp-go`), for use by LLM clients,
- a **tview terminal UI**.

`docs/SPECIFICATIONS.md` is the **contract** — the 12 use-cases (UC 1–12), the full MCP tool/resource
surface, the TUI screens, and acceptance criteria. Read it before changing product behavior; per
`.claude/rules/specification-rules.md`, spec and code change in the **same commit**. The per-layer
conventions live in `.claude/rules/` and load automatically when you edit a matching path — they,
not this file, are the source of truth for their layer.

## Runtime shape & invocation

One binary, multiple **modes** selected in `cmd/`, all backed by the same bbolt file:

- default / `tui` → launches the terminal UI;
- `serve` / `mcp` → runs the MCP stdio server;
- `init` → places the embedded Claude Code bootstrap kit (`.claude/` from `templates/claude-code/`)
  into the current directory and prints the phase guide — opens no DB, needs no network.

The mode may lead (`microstore serve --db x`) or follow the flags (`microstore --db x serve`).
`--db <path>` overrides the DB location (default `~/.local/share/microstore/microstore.db`).
`MICROSTORE_GITHUB_TOKEN`, when set, authenticates GitHub requests (higher rate limits, private
repos); otherwise access is anonymous.

```
main.go               # thin: call cmd.Run(os.Args[1:])
cmd/                  # mode parsing + lifecycle: open the one bbolt Store, build the Service, dispatch
internal/
  models/             # plain domain structs (live vs. persisted) — NO persistence imports
  db/                 # bbolt Store + ConfigRepo/InstallRepo; the ONLY package that imports bbolt
  github/             # outbound HTTPS GitHub client (catalog, repo/releases/assets, downloads, tarballs)
  install/            # download → verify SHA-256 → place 0755; Verify/Remove; host asset matching (match.go)
  scaffold/           # download template tarball → strip top-level → extract (path-traversal-safe)
  app/                # USE-CASE LAYER: orchestrates github+db+install+scaffold into UC 1–12
  server/             # MCP server (tools.go/resources.go); delegates to app.Service
  tui/                # tview view layer; owns the one *tview.Application; delegates to app.Service
templates/            # go:embed'ed project templates; claude-code/ is the .claude kit `init` places
```

`templates/claude-code/.claude/` is a **copy** of this repo's live `.claude` (commands, rules,
skills) frozen into the binary. When the live `.claude` content changes, refresh the copy in the
same commit — the two must not drift.

## The dependency rule (the thing that's easy to get wrong)

The piece most easily gotten wrong: **`internal/app` is the use-case layer, and `server`/`tui` depend
on it — never directly on `db`, `github`, `install`, or `scaffold`.** All orchestration lives in
`app` exactly once.

```
models ← db ─────┐
github ──────────┤
install ─────────┼─ app  ─┬─ server (MCP)
scaffold ────────┘        └─ tui
```

- `internal/models` is storage-agnostic — never imports bbolt. It separates **live** entities
  (fetched from GitHub every time, never persisted: `Catalog`, `Release`, `Asset`, …) from
  **persisted** ones (`InstalledApp` keyed by `owner/name` slug, singleton `Config`).
- `internal/db` is the **only** place that touches `go.etcd.io/bbolt`. `db.Open` returns a `*Store`
  that hands out `ConfigRepo`/`InstallRepo`; callers receive domain models, never `*bolt.Tx`,
  `*bolt.Bucket`, or txn-scoped byte slices.
- `internal/github`, `internal/install`, `internal/scaffold` are leaf services depending only on
  `models` (and on small interfaces — e.g. `install.Downloader`, `app.Cataloger` — that
  `*github.Client` satisfies). They do **no** persistence.
- `internal/app` (`Service`) wires those together behind the twelve use-case methods and returns
  plain domain models (or `*AssetSelectionError` when an install needs a manual asset pick).
- `internal/server` and `internal/tui` each define a narrow interface that `*app.Service` satisfies,
  and call **only** through it — no bbolt, no GitHub client, no business logic in a handler/draw call.
- `cmd/` opens the single `*bolt.DB` once, builds `app.New(github.New(), store)`, and injects that
  one `Service` into whichever mode runs. `main` stays thin.

## Two cross-cutting constraints that shape everything

1. **bbolt takes a process-wide exclusive write lock.** Only one writer process at a time, and
   `Open` must set a `Timeout`. Practically: don't expect the MCP server and the TUI to both hold the
   same DB file read-write from two processes — design modes as alternatives, or use `ReadOnly: true`
   for a genuinely read-only consumer.
2. **MCP stdio uses stdout as the protocol channel.** In `serve` mode, **all logging goes to stderr**.
   Writing logs to stdout corrupts the MCP stream.

## Layer rules — read before editing

| Editing under… | Read first |
|---|---|
| `internal/db/**`, `internal/models/**` | `.claude/rules/db-rules.md` (bbolt: txns, key encoding, buckets, sentinel errors) |
| `internal/server/**` | `.claude/rules/mcp-server.md` (tool/resource/prompt registration, typed handlers, error semantics, transports) |
| `internal/tui/**` | `.claude/rules/tui-rules.md` (single-goroutine event loop, `QueueUpdateDraw`, layout, testing) |
| `internal/**/*_test.go` | `.claude/rules/go-testing.md` (black-box packages, table-driven subtests, testify usage) |
| `.github/workflows/**` | `.claude/rules/github-actions.md` (least-privilege permissions, SHA-pinned third-party actions) |
| anything that changes product behavior | `.claude/rules/specification-rules.md` (always-on: keep `docs/SPECIFICATIONS.md` in sync with the code) |

`internal/app`, `internal/github`, `internal/install`, and `internal/scaffold` have no dedicated
rule file — they follow ordinary Go conventions plus the dependency rule above and the spec
contract. When adding a use-case, add the method to `app.Service` first, then expose it from both
`server` (a tool/resource) and `tui` (a screen/keybinding) through the interface each defines.

## Commands

This repo is already scaffolded; the active maintenance command is **`/app-spec-sync`**, which audits
code vs. `docs/SPECIFICATIONS.md` (detecting drift via git-diff) and implements gaps in small,
test-covered phases. (The earlier-stage **`/product-idea`** → **`/app-init <module-path>`** commands
that wrote the spec and laid down the tree have already run.) The **`build-and-release`** skill
generates a tag-triggered cross-platform release workflow with checksums. Day to day, use the Makefile:

```
make run      # go run .
make build    # go build ./...
make test     # go test ./... -race -cover
make fmt      # gofumpt -w .
make lint     # golangci-lint run
make tidy     # go mod tidy
make check    # fmt + tidy + lint + test, in sequence
```

Run a single test: `go test ./internal/db -run TestName -race -v`.

Formatting is **gofumpt**, not plain gofmt. Linting is **golangci-lint v2** (`.golangci.yml`,
`version: "2"`).

## Testing approach

- **MCP handlers:** test through the in-process client (`client.NewInProcessClient(s)`) so
  registration + (de)serialization run, without spawning a transport.
- **TUI:** drive a `tcell.SimulationScreen` via `app.SetScreen(sim)` headless; keep render logic in
  pure data→cells helpers that are unit-testable without the Application.
- **db:** repository round-trips against a temp bbolt file (`t.TempDir()`).
