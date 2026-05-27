---
description: Interactively turn a raw product idea into a complete, logically-consistent specification for a local Go micro-app (TUI + MCP + embedded DB). Writes docs/SPECIFICATIONS.md. Spec-driven development — run this before any code.
argument-hint: [one-line idea, optional]
disable-model-invocation: true
allowed-tools: AskUserQuestion Read Write Edit Bash(mkdir *) Bash(ls *) mcp__plugin_context7_context7__query-docs mcp__plugin_context7_context7__resolve-library-id
---

## Context

- Working directory: !`pwd`
- Architecture & constraints: @CLAUDE.md
- bbolt (persistence) rules: @.claude/rules/db-rules.md
- MCP server rules: @.claude/rules/mcp-server.md
- tview TUI rules: @.claude/rules/tui-rules.md
- Existing spec?: !`test -f docs/SPECIFICATIONS.md && echo "yes — docs/SPECIFICATIONS.md exists, treat this as a REVISION session" || echo "no — fresh spec"`
- Initial idea from the user: $ARGUMENTS

## Your job

Run a **collaborative, spec-driven discovery session** that turns a product idea into
`docs/SPECIFICATIONS.md` — a specification complete and consistent enough to implement against
without further guessing. This is a conversation, not a form. Brainstorm, reflect understanding
back, challenge vague or contradictory logic, and propose options when the user is unsure.

**You are not done until the plan is logically coherent and the user has confirmed it.** Only then
write the file.

### The immovable envelope (state it, design within it)

Every spec must fit a **single local executable** with **no external runtime dependencies**:

- ✅ A **tview TUI** and/or an **MCP stdio server** as the two user-facing surfaces.
- ✅ One **embedded bbolt** database file owned by the process (no DB server, no cgo).
- ❌ **No web server, no network services, no cloud APIs, no message brokers, no external processes.**
- ❌ Nothing that requires a daemon, a second binary, or internet access to function.

If an idea implies any of the ❌ items, surface the conflict immediately and help the user reshape
it to fit the envelope (or scope that part out as a non-goal). Do not silently accept out-of-scope
requirements.

Also honor the layering from `CLAUDE.md`: pure `models`, bbolt confined to `db` repositories, both
`server` and `tui` consuming repositories. Note bbolt's **process-wide write lock** — if the spec
implies the TUI and MCP server run at the same time against the same file, resolve it (alternating
modes, or a read-only consumer) in the spec.

## Process — iterate with AskUserQuestion

Use the **AskUserQuestion** tool throughout (batch related questions, 2–4 per call). After each
round, **play back** your current understanding in prose before asking the next round. Skip
questions the user has already answered (e.g. via `$ARGUMENTS`). Loop until each area below is
unambiguous. Don't move on while a logical gap remains — name the gap and resolve it.

Cover, roughly in this order (reorder to follow the conversation naturally):

1. **Problem & goal** — what is this for, who is it for, what does success look like? Reflect it back
   in one or two sentences and get agreement before going deeper.
2. **Scope & non-goals** — what's explicitly in, what's explicitly out (v1 vs later). Pin down the
   smallest version that's still useful.
3. **Domain model** — the **entities/models**, their key attributes and types, and the
   **relationships** between them (one-to-many, references, ownership). Probe identity: what
   uniquely identifies each entity? This becomes `internal/models` + bbolt buckets.
4. **Persistence shape** — for each entity, the bbolt **bucket(s)**, the **key encoding** (so lexical
   order matches logical order — e.g. `NextSequence` big-endian IDs, RFC3339 timestamps), and any
   **secondary index** buckets needed for the lookups the use-cases require. Validate against
   `db-rules.md`.
5. **Use-cases** — the concrete operations the system performs (create/list/search/update/…), each
   tied to the entities and surfaces it touches. Flag any use-case the chosen key design can't serve
   efficiently.
6. **User stories** — "As a <role>, I want <capability> so that <benefit>", grouped by surface.
7. **MCP surface** — which **tools / resources / prompts** the server exposes, with rough
   input/output for each tool. Tie each back to a use-case.
8. **TUI surface** — the **screens/views**, the navigation map between them, primary interactions and
   key bindings. Tie each back to a use-case.
9. **Acceptance criteria** — observable conditions that mean each use-case is correctly implemented.

When the user is unsure, propose 2–3 concrete options with trade-offs rather than leaving it open.
When the user contradicts an earlier decision, point it out and reconcile.

## Finishing

When — and only when — the picture is coherent and the user confirms:

1. `mkdir -p docs` if needed.
2. Write **`docs/SPECIFICATIONS.md`** with these sections (omit a section only if truly N/A):
   - **Overview** — problem, target user, one-paragraph summary.
   - **Goals & Non-Goals** — non-goals explicitly restate the local-only envelope.
   - **Domain Model** — entities with attributes/types; a relationships list or simple ASCII diagram;
     identity/key strategy per entity.
   - **Persistence Design** — bucket list, key encoding per bucket, secondary indexes, serialization
     choice (default: JSON), referencing `db-rules.md` constraints.
   - **Use-Cases** — numbered; each names the entities, the surface(s), and the repository operations
     involved.
   - **User Stories** — grouped by surface, each traceable to a use-case.
   - **MCP Surface** — table/list of tools (name, purpose, input, output), resources, prompts.
   - **TUI Surface** — screen list, navigation map, key interactions per screen.
   - **Acceptance Criteria** — per use-case, observable pass conditions.
   - **Open Questions / Assumptions** — anything deferred, with the assumption made.
3. If `docs/SPECIFICATIONS.md` already existed, show what changed rather than silently overwriting.
4. Summarize the spec back in a few lines and state that it's the implementation contract — code
   should follow it, and changes to scope mean updating the spec first.
