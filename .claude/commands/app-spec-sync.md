---
description: Audit the codebase against docs/SPECIFICATIONS.md, detect spec drift via git-diff, then plan and implement the gaps in small, test-covered phases. Spec-driven reconciliation — run whenever the spec or the code moves.
argument-hint: [focus area or entity, optional]
disable-model-invocation: true
allowed-tools: AskUserQuestion Read Write Edit Glob Grep Bash(git *) Bash(go *) Bash(make *) Bash(gofumpt*) Bash(golangci-lint*) Bash(ls *) Bash(mkdir *) mcp__plugin_context7_context7__query-docs mcp__plugin_context7_context7__resolve-library-id
---

## Context

- Working directory: !`pwd`
- Architecture & dependency rule: @CLAUDE.md
- Spec rule (the contract): @.claude/rules/specification-rules.md
- bbolt / models rules: @.claude/rules/db-rules.md
- MCP server rules: @.claude/rules/mcp-server.md
- tview TUI rules: @.claude/rules/tui-rules.md
- Go testing rules: @.claude/rules/go-testing.md
- Spec present?: !`test -f docs/SPECIFICATIONS.md && echo "yes" || echo "NO — missing"`
- Is a Go module?: !`test -f go.mod && echo "yes — $(head -1 go.mod)" || echo "no go.mod"`
- Spec working-tree changes: !`git status --short -- docs/SPECIFICATIONS.md 2>/dev/null || echo "(not in git)"`
- Recent spec history: !`git log --oneline -8 -- docs/SPECIFICATIONS.md 2>/dev/null || echo "(no history)"`
- Go source tracked: !`git ls-files '*.go' 2>/dev/null | head -100`
- Focus (optional): $ARGUMENTS

## Your job

Reconcile **what the spec says** with **what the code does**, then close the gap in small,
test-covered increments. You operate in three movements: **detect drift → audit coverage → plan &
implement**. The spec is the contract (see the spec rule) — when code and spec disagree, one of them
is wrong, and you must resolve it, never bury it.

If `$ARGUMENTS` names a focus (an entity, a use-case, a tool, a screen), scope the audit and the
first implementation phase to it. Otherwise cover the whole spec.

### Gate (check before anything else)

- **No `docs/SPECIFICATIONS.md`** → stop. Tell me to run **`/product-idea`** first; there is nothing
  to sync against.
- **No `go.mod`** → the project isn't scaffolded. Tell me to run **`/app-init`** first, then return.
- Do not invent requirements. If the spec is silent on something the code needs, surface it as an
  **open question** rather than guessing — and if we decide it, the spec gets updated (spec rule).

## 1. Detect spec drift (git-diff)

Use git to learn **what in the spec recently moved**, so the audit focuses where risk is highest:

- Uncommitted spec edits: `git diff -- docs/SPECIFICATIONS.md`.
- Spec changes since the last few commits: `git log -p -3 -- docs/SPECIFICATIONS.md` (or
  `git diff <since>.. -- docs/SPECIFICATIONS.md` if I name a ref).
- Whether code under `internal/`, `cmd/`, or `main.go` changed in the same range, to spot a spec that
  moved without code (or code that moved without the spec — a spec-rule violation to flag).

Classify each spec area as **new**, **changed**, or **stable**. New/changed areas are the prime
suspects for missing or stale implementation.

## 2. Audit coverage (spec → code → tests)

Parse the spec into its concrete elements and check each against the codebase. Read the spec
sections — Domain Model, Persistence Design, Use-Cases, MCP Surface, TUI Surface, Acceptance
Criteria — and for each element verify two things independently:

1. **Implemented?** Is there code that realizes it, in the right layer per `CLAUDE.md`?
   - Entities/fields → structs in `internal/models` (no persistence imports).
   - Buckets / key encoding / indexes → repositories in `internal/db` (the only bbolt importer).
   - Use-cases → repository methods, consumed by `server` and/or `tui` (never bbolt in those layers).
   - MCP tools/resources/prompts → registrations in `internal/server` (per `mcp-server.md`).
   - TUI screens/navigation → views in `internal/tui` (per `tui-rules.md`).
2. **Tested?** Is there a sibling `*_test.go` that actually exercises it (per `go-testing.md`)?
   Implemented-but-untested is a **gap**, not a done item.

Use structural search to find declarations and registrations reliably — prefer the **ast-grep skill
over grep** for "does a struct/method/tool-registration named X exist", since it matches the syntax
tree. Use Glob/Read to confirm test files and read what they cover. Don't trust file names alone —
open the test to confirm it asserts the behavior, not just compiles.

Produce a **coverage matrix**. For each spec element, one of:

| Status | Meaning |
|---|---|
| ✅ done | implemented in the right layer **and** covered by a passing `_test.go` |
| 🟡 untested | code exists but no real test → must add tests |
| 🔴 missing | spec requires it, no code yet |
| 🟠 drift | code and spec disagree (stale code vs. changed spec, or code with no spec basis) |

Run `go build ./...` and `go test ./... -race -cover` once to ground the audit in reality (what
compiles, what passes). Report the matrix as a table before planning.

## 3. Plan small, test-forced phases

Turn the 🔴/🟡/🟠 rows into an ordered list of **small** phases. Constraints:

- **Respect the dependency rule.** `models` → `db` → (`server` | `tui`). Never plan a server/tui
  phase before the repository method it needs exists. A natural ordering: model → bucket+repo →
  the use-case's MCP tool and/or TUI view.
- **Small.** One entity, one repository operation, one tool, or one screen per phase — not a whole
  layer at once. A phase should be a reviewable, independently-green slice.
- **Every phase is test-forced.** The definition of done for a phase is: the new/changed code has a
  sibling `*_test.go` following `go-testing.md`, and `go test ./... -race` passes. **No phase ships
  code without tests** — that is non-negotiable for this template. Tie each phase's tests to the
  spec's **Acceptance Criteria** where they exist.
- **Drift first, then gaps.** 🟠 drift rows are resolved before new 🔴 work, because building on a
  spec/code disagreement compounds the problem. Resolving drift means either fixing the code or
  updating `docs/SPECIFICATIONS.md` (spec rule) — decide which with me if it's not obvious.

Present the plan as a numbered phase list: each phase names its spec element(s), the layer(s) it
touches, the repository/handler/view it adds, and its acceptance check.

## 4. Decide what to start, and implement it

You may **decide the starting phase** yourself: pick the lowest-dependency unblocked 🟠/🔴/🟡 item
(scoped to `$ARGUMENTS` if given). If the choice is genuinely ambiguous or several independent
slices compete, use **AskUserQuestion** to let me pick; otherwise state your choice and proceed.

Then implement **one phase at a time**:

1. Write the code in the correct layer, honoring that layer's rule file.
2. Write the sibling `*_test.go` (db: round-trip on a `t.TempDir()` file; server: in-process client;
   tui: `tcell.SimulationScreen` + pure data→cells helpers — see the layer rules and `go-testing.md`).
3. Run `make check` (or `go test ./... -race -cover` + `gofumpt` + `golangci-lint`). The phase is
   **done only when it's green**.
4. If the work revealed a spec inaccuracy, update `docs/SPECIFICATIONS.md` in the same change (spec
   rule) and show the diff.
5. Report what landed, re-state the remaining phases, and stop for review before starting the next
   phase unless I've told you to run straight through.

## Finish

End with: the coverage matrix as it stands now, what you implemented this run (with test results),
and the ordered list of phases still outstanding so the next `/app-spec-sync` picks up cleanly.
