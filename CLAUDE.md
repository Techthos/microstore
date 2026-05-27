# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A template for building **single-binary Go micro-applications** that run fully locally. Each app
bundles three faces over one shared domain:

- an **embedded bbolt database** (no server, no cgo) — its own data file,
- an **MCP server** over stdio (and optionally HTTP), for use by LLM clients,
- a **tview terminal UI**.

The point of the template is to stamp out new micro-apps fast. The architecture, library choices,
and conventions are fixed by the rules in `.claude/rules/`; read those before writing code in the
layer they govern — they are the source of truth, not this file.

## Base structure

One binary, multiple **modes** selected in `cmd/`. The same process can serve MCP or launch the TUI,
both backed by the same bbolt file.

**Note:** this repo ships as a bare template — only `CLAUDE.md`, `.claude/`, and `README.md` exist
until `/app-init` runs. The tree below is the *target* shape; scaffolding starts flat and adds
`cmd/`/`internal/` packages only as the spec needs them. Don't assume these dirs exist yet.

```
main.go               # thin: parse mode, call into cmd/
cmd/                  # mode/transport selection + process lifecycle (open DB, wire deps, run)
internal/
  models/             # plain domain structs — NO persistence imports
  db/                 # bbolt repositories; the ONLY package that imports bbolt
  server/             # MCP server (mark3labs/mcp-go); transport-agnostic
  tui/                # tview view layer; owns the one *tview.Application
```

A typical invocation set: default → TUI; `serve` / `mcp` → MCP stdio server; a `--db <path>` flag
shared by both.

## The dependency rule (the thing that's easy to get wrong)

Dependencies point **one direction**:

```
models  ←  db  ←  server
            ↑
           tui
```

- `internal/models` is storage-agnostic — never imports bbolt.
- `internal/db` is the **only** place that touches `go.etcd.io/bbolt`. It exposes a repository type
  that returns domain models; callers never see `*bolt.Tx`, `*bolt.Bucket`, or txn-scoped byte slices.
- **Both `server` and `tui` go through `internal/db` repositories** — neither opens bbolt, runs a
  transaction, or embeds business logic in a handler/draw call.
- `cmd/` opens the single `*bolt.DB` once at startup, constructs the repository, and injects it into
  whichever mode runs. `main` stays thin.

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

These rules are path-scoped and load automatically when you edit a matching file — they are the
source of truth for their layer.

## Commands

The spec-driven workflow: **`/product-idea`** writes `docs/SPECIFICATIONS.md`, **`/app-init <module-path>`**
scaffolds the codebase against it (once), and **`/app-spec-sync`** audits code vs. spec — detecting
drift via git-diff — then implements the gaps in small, test-covered phases. The
**`build-and-release`** skill generates `.github/workflows/build-and-release.yml` (CI on push/PR plus
a tag-triggered cross-platform release with checksums). After scaffolding, use the Makefile:

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
