---
description: The spec is the contract. docs/SPECIFICATIONS.md must always match the code — update the spec in the same change that alters scope, domain, persistence, or a user-facing surface. Always-on.
alwaysApply: true
---

# Specification is the contract

`docs/SPECIFICATIONS.md` is the single source of truth for **what this app is**. This is a
spec-driven template: the spec is written first (`/product-idea`), the codebase is scaffolded
against it (`/app-init`), and the implementation is reconciled with it (`/app-spec-sync`). The spec
and the code must never silently disagree.

## The rule

**Spec and code change together.** Any change that alters the product — a new or removed entity or
field, a bucket or key-encoding change, a new/changed/removed MCP tool, resource, or prompt, a
new/changed TUI screen or navigation path, a changed use-case or acceptance criterion — is **not
complete** until `docs/SPECIFICATIONS.md` reflects it. The spec edit belongs in the **same commit**
as the code, not a follow-up.

- **Scope change → spec first.** If the user asks for behavior the spec doesn't describe, update the
  spec (or surface the gap and get agreement) before writing the code that implements it.
- **Discovered drift → reconcile, don't bury.** If you notice code and spec disagree while working
  on something else, call it out. Either the code is wrong (fix it) or the spec is stale (update it),
  but never leave them in conflict.
- **Refactors that preserve behavior** (renames within a layer, internal restructuring, performance
  work) do **not** require a spec edit — the spec describes observable behavior, not internal shape.

## What stays out of the spec

The spec describes **observable product behavior** within the local-only envelope (see `CLAUDE.md`):
domain model, persistence design, use-cases, user stories, MCP surface, TUI surface, acceptance
criteria. It does **not** track internal package layout, helper functions, or implementation tactics
— those live in code and the layer rules.

## When in doubt

If you can't tell whether a change needs a spec update, it probably does — ask. A spec that drifts
behind the code defeats the entire template. Treat `/app-spec-sync` as the audit that should always
come back clean.
