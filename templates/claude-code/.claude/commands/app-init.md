---
description: Scaffold a bare-minimum, best-practice Go codebase (go.mod, entry point, tooling, Makefile, CI). Run once at the start of a new Go project.
argument-hint: [module-path]
disable-model-invocation: true
allowed-tools: Bash(go *) Bash(git init*) Bash(git add*) Bash(mkdir *) Bash(ls *) Bash(which *) Bash(gofumpt*) Read Write Edit
---

## Context

- Working directory: !`pwd`
- Existing contents: !`ls -A`
- Go toolchain: !`go version 2>/dev/null || echo "go NOT installed"`
- Already a module?: !`test -f go.mod && echo "yes — go.mod exists" || echo "no"`
- Specification present?: !`test -f docs/SPECIFICATIONS.md && echo "yes — docs/SPECIFICATIONS.md exists" || echo "NO — missing"`

## Task

Initialize a minimal, idiomatic Go codebase in the current directory.

**Module path** = `$ARGUMENTS`. If empty, ask me for it (e.g. `github.com/<org>/<repo>`); do not guess.

### Guardrails (check before writing)

- **Spec gate (check first):** if `docs/SPECIFICATIONS.md` does **not** exist, **stop immediately** —
  do not init the module or write any file. Tell me the spec is missing and to run **`/product-idea`**
  first to produce it. This template is spec-driven: scaffolding only happens against an agreed spec.
- If Go is not installed, stop and tell me.
- If `go.mod` already exists, do **not** re-init — report the state and ask whether to proceed with the remaining files only.
- Never overwrite existing files without showing me what differs first.

### Steps

1. **Module init** — `go mod init <module-path>`.

2. **Layout** — Stay minimal. Start flat; do **not** create `cmd/`, `internal/`, or `pkg/` unless I ask or the project clearly needs multiple binaries. A single-binary project gets:
   - `main.go` — minimal entry point that only wires dependencies and starts the app.
   - `main_test.go` — one trivial passing test so `go test ./...` is green from day one.

3. **`.gitignore`** — Go-appropriate: compiled binaries, `*.exe`, `*.test`, `*.out`, `vendor/` (if unused), `.env`, `dist/`, IDE dirs.

4. **`README.md`** — project name, one-line description, and a Getting Started section (build, run, test, lint).

5. **Formatting & linting**:
   - `.golangci.yml` using **golangci-lint v2** format (`version: "2"`). Enable a sensible, *minimal* default set on top of the standard linters (e.g. `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, plus `gofumpt`/`gci` formatters) — start small, don't enable everything.
   - Prefer `gofumpt` over plain `gofmt` for formatting.

6. **`Makefile`** with predictable targets:
   - `fmt`   → `gofumpt -w .`
   - `lint`  → `golangci-lint run`
   - `test`  → `go test ./... -race -cover`
   - `build` → `go build ./...`
   - `tidy`  → `go mod tidy`
   - `check` → runs fmt, tidy, lint, test in sequence
   - `run`   → `go run .`

7. **CI** — `.github/workflows/ci.yml` running `go build`, `go test -race`, and `golangci-lint` on push/PR, pinning one Go version consistent with `go.mod`.

8. **Initialize git** if not already a repo (`git init`), then stage the new files (do not commit unless I ask).

### Finish

- Run `go build ./...` and `go test ./...` to prove the scaffold compiles and tests pass.
- Report any tools the project needs but that aren't installed (`golangci-lint`, `gofumpt`) with the install command, rather than failing silently.
- Summarize what was created as a short file tree.
- **Offer the release workflow:** ask whether to also add a cross-platform build-and-release
  pipeline. If I say yes, run the **`/build-and-release`** skill to generate
  `.github/workflows/build-and-release.yml` (test on push/PR + tagged multi-OS/arch binaries
  with checksums). Don't create it unprompted — keep the initial scaffold minimal.
