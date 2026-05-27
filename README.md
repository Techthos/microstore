# golang-base-template

A **Claude Code template** for stamping out single-binary Go micro-apps that run fully locally —
no server, no cgo, no network. Each app you generate from it bundles three faces over one shared
domain:

- an **embedded bbolt database** (its own data file),
- an **MCP server** over stdio (optionally HTTP), for LLM clients,
- a **tview terminal UI**.

The template is **spec-driven**: you describe the product in a conversation, agree on a
specification, then scaffold and build against it. The architecture and library choices are fixed by
rules that Claude Code reads automatically as it edits each layer — so the apps come out consistent.

## Prerequisites

- **Go** (toolchain that satisfies the generated `go.mod`).
- **Claude Code** — this template is driven by its slash commands.
- **gofumpt** and **golangci-lint v2** for `make fmt` / `make lint`:
  ```sh
  go install mvdan.cc/gofumpt@latest
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
  ```
  `make` will tell you which are missing rather than failing silently.

## The workflow

Use this template by copying it into a fresh, empty directory (it ships only `CLAUDE.md`, the
`.claude/` config, and this README — no Go module yet), then opening Claude Code there.

### 1. Shape the idea → `/product-idea`

```
/product-idea a CLI inventory tracker for a small workshop
```

This runs a collaborative discovery session. Claude challenges vague logic, proposes options, and
keeps every requirement inside the **immovable envelope** — a single local executable with no web
server, network services, cloud APIs, or second binary. It iterates with you over the domain model,
persistence shape (buckets, key encoding, indexes), use-cases, and the MCP + TUI surfaces.

When the picture is coherent **and you confirm**, it writes **`docs/SPECIFICATIONS.md`** — the
implementation contract. Re-run `/product-idea` later to revise it; scope changes mean updating the
spec first.

### 2. Scaffold → `/app-init`

```
/app-init github.com/your-org/inventory-tracker
```

`/app-init` is **spec-gated**: if `docs/SPECIFICATIONS.md` is missing it stops and sends you back to
`/product-idea`. Otherwise it lays down a minimal, idiomatic Go codebase: `go.mod`, a thin
`main.go`, a `.golangci.yml` (v2), a `Makefile`, GitHub Actions CI, and a `.gitignore`. It starts
flat and only adds `cmd/` / `internal/` packages as the project needs them. It finishes by proving
the scaffold builds and tests pass.

### 3. Build it out → `/app-spec-sync`

```
/app-spec-sync                 # cover the whole spec
/app-spec-sync inventory item  # or scope to one entity / use-case / tool / screen
```

`/app-spec-sync` is how the code follows the spec. It detects spec drift via git-diff, audits the
spec against the codebase into a coverage matrix (done / untested / missing / drift), then plans and
implements the gaps in **small, test-forced phases** that respect the `models → db → (server | tui)`
dependency rule. Re-run it whenever the spec or the code moves; the audit should always come back
clean. As it edits files under each package it automatically picks up the matching rule file (see
below) — you don't need to paste conventions in.

## Architecture (what the rules enforce)

One binary, multiple **modes** selected in `cmd/`; the same process serves MCP or launches the TUI,
both backed by the same bbolt file. Dependencies point **one direction**:

```
models  ←  db  ←  server
            ↑
           tui
```

- `internal/models` — plain domain structs, **storage-agnostic** (never imports bbolt).
- `internal/db` — the **only** package that touches `go.etcd.io/bbolt`; exposes repositories that
  return domain models. Callers never see a `*bolt.Tx` or a txn-scoped byte slice.
- `internal/server` — the MCP server (`github.com/mark3labs/mcp-go`), transport-agnostic.
- `internal/tui` — the tview view layer (`github.com/rivo/tview` on `gdamore/tcell/v2`); owns the
  one `*tview.Application`.
- `cmd/` opens the single `*bolt.DB` once at startup, builds the repository, and injects it into
  whichever mode runs. `main` stays thin.

Two constraints shape everything:

1. **bbolt takes a process-wide exclusive write lock** — only one writer process at a time, and
   `Open` must set a `Timeout`. Design modes as alternatives, or use `ReadOnly: true` for a
   genuinely read-only consumer.
2. **MCP stdio uses stdout as the protocol channel** — in `serve` mode, **all logging goes to
   stderr**. Logging to stdout corrupts the MCP stream.

## The rules system

`CLAUDE.md` holds the big picture; the per-layer detail lives in `.claude/rules/`, each scoped by
path so Claude Code loads it only when editing that layer:

| When editing…                              | Rule loaded                  |
|--------------------------------------------|------------------------------|
| `internal/db/**`, `internal/models/**`     | `.claude/rules/db-rules.md`  |
| `internal/server/**`                       | `.claude/rules/mcp-server.md`|
| `internal/tui/**`                          | `.claude/rules/tui-rules.md` |
| `internal/**/*_test.go`                    | `.claude/rules/go-testing.md`|

These are the source of truth for their layer. Edit them to change the conventions the generated
apps follow.

## Commands

After `/app-init`, use the Makefile:

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

## Testing approach

- **MCP handlers** — test through the in-process client (`client.NewInProcessClient(s)`) so
  registration and (de)serialization run, without spawning a transport.
- **TUI** — drive a `tcell.SimulationScreen` via `app.SetScreen(sim)` headless; keep render logic in
  pure data→cells helpers that are unit-testable without the Application.
- **db** — repository round-trips against a temp bbolt file (`t.TempDir()`).
