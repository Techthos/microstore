# microstore — Specification

> This document is the **implementation contract** for microstore. Code follows this spec; any change
> to observable product behavior updates this file in the **same commit** (see
> `.claude/rules/specification-rules.md`). When code and spec disagree, one of them is a bug.

## Overview

**Problem.** This template stamps out single-binary Go micro-apps (TUI + MCP + embedded bbolt). Once
several such apps exist, there's no convenient, local way to (a) discover which apps are available,
(b) pull the right pre-built binary for your machine, or (c) bootstrap a brand-new app of the same
shape.

**Target user.** A developer/operator who uses or builds these micro-apps and works from a terminal.

**Summary.** microstore is itself one of these micro-apps. It has two faces over GitHub:

- **Consume** — fetch a curated catalog (`catalog.json`) from GitHub live, browse/search it, view an
  app's releases and assets, detect the host `GOOS/GOARCH`, download the matching release asset,
  verify its SHA-256 against the release's `checksums.txt`, place it (executable) in a managed
  directory, and track it. Update / uninstall / re-verify tracked installs.
- **Create** — pick a starting template (also listed in the catalog), download its tarball from
  GitHub, extract the bare scaffolding into a target directory, and hand off to the existing
  spec-driven workflow by initiating `/product-idea`. Independently of the catalog, `microstore init`
  places the **embedded Claude Code bootstrap kit** (the `.claude/` directory: the `/product-idea`,
  `/app-init`, and `/app-spec-sync` commands, the layer rules, and the `build-and-release` skill)
  into the current directory and prints how the phases are used — it needs no network and opens no
  database. Re-running it over an existing setup **updates in place**: every file the kit ships is
  refreshed to the embedded version, while any files a user added under the kit's trees are kept.

It is **online-only**: there is no offline catalog cache. The embedded bbolt file persists only what
you've installed and the app's own configuration.

## Goals & Non-Goals

### Goals
- Browse and search a curated catalog of micro-apps fetched **live** from GitHub.
- Inspect an app: description, releases, and per-release assets (live).
- Install the correct asset for the host OS/arch automatically, **with SHA-256 verification**, into a
  managed directory; record the install.
- Manage tracked installs: **update** to a newer release, **uninstall**, and **re-verify** integrity.
- Scaffold a new micro-app from a catalog-listed template and **initiate `/product-idea`**.
- Bootstrap the spec-first Claude Code setup anywhere via **`microstore init`**: place the embedded
  `.claude` kit into the current directory (or update an existing one in place) and print the phase
  guide (`/product-idea` → `/app-init` → `/app-spec-sync`).
- Expose all of the above through both a **tview TUI** and an **MCP stdio server** (full read+mutate).
- Use anonymous GitHub access by default; use a token from `MICROSTORE_GITHUB_TOKEN` when present.

### Non-Goals (and the local-only envelope)
- **microstore runs no service of its own.** It is a single binary with no daemon, no embedded web
  server, no broker, and no second binary required to function. Its GitHub access is **outbound HTTPS
  client I/O** (like `git` or `go install`), not a hosted service. This is the explicit reconciliation
  of the template envelope: *acting as an HTTP client is allowed; running a service is not.*
- **No offline catalog cache.** Online-only: if GitHub is unreachable, browse/search/install/scaffold
  fail with a clear error. bbolt is **not** a catalog mirror.
- **No package-manager PATH management.** Installs go to a managed directory; microstore does not
  symlink onto `PATH` or manage system-wide installation, and it never rewrites `PATH` silently. It
  *does* check on TUI launch whether `InstallDir` is on the current `$PATH` and, when it is not, warn
  the user and — only on explicit confirmation — append a single `export PATH="$PATH:<InstallDir>"`
  line to their shell profile (see TUI Surface). That opt-in profile edit is the sole extent of PATH
  involvement; there is no symlinking, no system-wide install, and no change to the running process's
  `PATH`.
- **Scaffolding does not configure the project.** It does not set the Go module path, run `git init`,
  or rename anything — that is the job of the downstream `/app-init` step. microstore lays down the
  bare template and initiates `/product-idea`.
- **No private-registry / non-GitHub sources** in v1. The catalog and all binaries/tarballs come from
  GitHub.
- **No cross-arch override** in v1 (host `GOOS/GOARCH` only, with a manual asset-pick fallback).

## Domain Model

microstore distinguishes **live** entities (fetched from GitHub every time, never stored) from
**persisted** entities (kept in bbolt). All structs live in `internal/models` and are
storage-agnostic (no bbolt imports). Serialization is **JSON** throughout.

### Live entities (fetched, never persisted)

| Entity | Source | Key attributes |
|---|---|---|
| `Catalog` | manifest (`catalog.json`) | `Apps []ManifestEntry`, `Templates []Template` |
| `ManifestEntry` | manifest | `Repo` (`owner/name`), `Category`, `DisplayName` (optional), `Bin` (optional — overrides the repo's bare name in the placed `microapp-<name>` filename) |
| `Template` | manifest | `Repo` (`owner/name`), `Ref` (branch/tag), `Name`, `Description` |
| `RepoInfo` | GitHub repo API | `FullName`, `Description`, `Homepage`, `Stars` |
| `Release` | GitHub releases API | `TagName`, `Name`, `Body`, `PublishedAt` (time), `Prerelease` (bool), `Assets []Asset` |
| `Asset` | GitHub release | `Name`, `DownloadURL`, `Size` (int64), `ContentType` |

### Persisted entities (bbolt)

| Entity | Identity | Key attributes |
|---|---|---|
| `InstalledApp` | `Repo` (`owner/name`) — natural unique key | `Repo`, `DisplayName`, `Category`, `Bin` (manifest override, kept so updates re-place at the same filename), `Version` (installed tag), `AssetName`, `Path` (absolute), `SHA256`, `Size`, `InstalledAt` (time), `SourceURL` |
| `Config` | singleton | `ManifestURL`, `InstallDir` |

### Relationships

```
Catalog ─┬─ many ManifestEntry  ──(repo)──>  GitHub repo ──> many Release ──> many Asset
         └─ many Template        ──(repo,ref)──> GitHub tarball

InstalledApp.Repo  ──references──>  the ManifestEntry/repo it came from (by slug; no FK enforced)
Config             ──singleton──>   owns ManifestURL + InstallDir
```

- An installed app corresponds to exactly one catalog repo (by slug) and exactly one downloaded
  asset of one release. The reference is by slug string; microstore does not enforce that the slug
  still exists in the catalog (an installed app may have been removed from the manifest).

### Identity / key strategy
- **`InstalledApp`** — key is the repo slug bytes `[]byte("owner/name")`. Non-empty, far below the
  32 KiB key limit, and lexical (byte) order == alphabetical-by-slug order, which is the listing
  order. No surrogate `NextSequence` ID is needed.
- **`Config`** — a single JSON document stored under the well-known key `[]byte("config")` in its own
  bucket.
- Live entities have no persistence identity.

## Persistence Design

Per `.claude/rules/db-rules.md`: bbolt `v1.4.x`, aliased `bolt`, opened once at startup with a
`Timeout`; all access behind repository types in `internal/db`; callers receive domain models only.

### Buckets

| Bucket (`[]byte` const) | Key encoding | Value | Notes |
|---|---|---|---|
| `installs` | `[]byte(repo)` = `owner/name` | JSON of `InstalledApp` | Byte-sorted == alphabetical; `List` is a `ForEach`/cursor walk |
| `config` | fixed `[]byte("config")` | JSON of `Config` | Single document; absent key ⇒ apply defaults |

Both buckets are created idempotently with `CreateBucketIfNotExists` in a single startup `Update`
(migration). Bucket names are package-level `[]byte` constants in `internal/db`.

### Secondary indexes
**None.** The only queries are *get-by-slug* and *list-all-installs*; the installed set is small, so
category/name filtering of installs happens in memory. Catalog search/filter operates on the
live-fetched catalog, not bbolt.

### Serialization
JSON (`encoding/json`). Decoding is kept backward-compatible (additive fields, tolerate missing keys)
since old `InstalledApp` records persist on disk across upgrades. Marshal happens in `internal/db`
immediately before `Put`; unmarshal immediately after `Get`/inside the cursor loop — never retain a
bbolt byte slice past its transaction.

### Repositories (`internal/db`)
- `InstallRepo`: `Get(repo) (*InstalledApp, error)` (sentinel `ErrNotFound` when absent),
  `List() ([]InstalledApp, error)`, `Save(InstalledApp) error`, `Delete(repo) error`.
- `ConfigRepo`: `Load() (Config, error)` (returns defaults if unset), `Save(Config) error`.

> Network access (GitHub client), the installer (download → verify → place → `chmod`), and the
> scaffolder (tarball extract) are **not** bbolt concerns and live in their own internal packages
> consumed by both `server` and `tui`; they are implementation layout, not part of this contract.

## Use-Cases

Each use-case names the entities, the surface(s), and the repository/service operations involved.

1. **Configure store (first-run + edit).** *Entities:* `Config`. *Surfaces:* TUI, MCP. On first run,
   apply defaults: `InstallDir = ~/.local/share/microstore/bin`, `ManifestURL` = the curated catalog
   published from this repo (`https://raw.githubusercontent.com/Techthos/microstore/main/catalog.json`).
   The user may change either from the Config screen / `set_config`. *Ops:* `ConfigRepo.Load/Save`.
2. **List / refresh catalog.** *Entities:* `Catalog`, `ManifestEntry`. *Surfaces:* TUI, MCP. Fetch
   `ManifestURL` live and return app entries. *Ops:* GitHub-client fetch; no bbolt.
3. **Search / filter catalog.** *Entities:* `ManifestEntry`. *Surfaces:* TUI, MCP. In-memory filter of
   the fetched catalog by free-text (name/repo) and/or category.
4. **View app details.** *Entities:* `RepoInfo`, `Release`, `Asset`, plus `InstalledApp` (to show
   install state). *Surfaces:* TUI, MCP. Fetch repo info + latest release + its assets live; annotate
   with the installed version if a record exists. *Ops:* GitHub fetch + `InstallRepo.Get`.
5. **List releases.** *Entities:* `Release`, `Asset`. *Surfaces:* TUI, MCP. Fetch all releases for a
   repo. "Latest" excludes prereleases by default.
6. **Install app.** *Entities:* `Release`, `Asset`, `InstalledApp`. *Surfaces:* TUI, MCP. Resolve the
   target release (default: latest non-prerelease, or a specified tag), detect host `GOOS/GOARCH`,
   match an asset by naming convention, fetch the release's `checksums.txt`, download the asset,
   **verify SHA-256**, write it to `InstallDir` as `microapp-<name>` (the entry's `bin` override when
   set, else the repo's bare name, prefixed;
   any `.exe` suffix preserved on Windows) with `0755`, and record an `InstalledApp`. On zero or
   ambiguous asset matches, fall back to manual asset selection. If no checksums file is found,
   installation is **refused** unless an explicit "allow unverified" override is given. *Ops:* GitHub
   fetch + installer + `InstallRepo.Save`.
7. **List installed.** *Entities:* `InstalledApp`. *Surfaces:* TUI, MCP. *Ops:* `InstallRepo.List`.
8. **Update app.** *Entities:* `InstalledApp`, `Release`, `Asset`. *Surfaces:* TUI, MCP. Compare the
   recorded `Version` to the latest release; if newer, run the install flow for the new version and
   overwrite the record (and the binary at `Path`). No-op (reported) if already current. *Ops:* GitHub
   fetch + installer + `InstallRepo.Save`.
9. **Uninstall app.** *Entities:* `InstalledApp`. *Surfaces:* TUI, MCP. Delete the binary at `Path`
   and remove the record. *Ops:* filesystem delete + `InstallRepo.Delete`.
10. **Re-verify installed app.** *Entities:* `InstalledApp`. *Surfaces:* TUI, MCP. Recompute the
    SHA-256 of the file at `Path` and compare to the recorded `SHA256`; report match / mismatch /
    missing-file. Read-only (no record change). *Ops:* filesystem read + `InstallRepo.Get`.
11. **List templates.** *Entities:* `Template`. *Surfaces:* TUI, MCP. Return the manifest's
    `Templates` section. *Ops:* GitHub fetch; no bbolt.
12. **Scaffold new app from template + initiate `/product-idea`.** *Entities:* `Template`. *Surfaces:*
    TUI, MCP. Download the chosen template repo's tarball at its `Ref`, extract the bare scaffolding
    into a target directory (strip the tarball's top-level component; reject path-traversal entries;
    refuse a non-empty target unless forced), then **initiate `/product-idea`**: the TUI launches the
    `claude` CLI in the target dir via `app.Suspend` (printing the exact command if `claude` is
    absent); the MCP tool returns the next-step instruction for the connected LLM client. Scaffolding
    does **not** set the module path, run `git init`, or rename. *Ops:* GitHub tarball fetch +
    scaffolder; no bbolt.
13. **Run an installed app (launch sub-micro-app).** *Entities:* `InstalledApp`. *Surface:* **TUI
    only** — running a full-screen sub-app requires owning the terminal, which the stdio MCP server
    (whose stdout *is* the protocol channel) cannot do, so there is no MCP tool. From the Installed
    screen, `Enter` resolves the highlighted install to its recorded binary `Path`, suspends the TUI
    via `app.Suspend` (the same terminal handoff used for `/product-idea`), and runs the binary with
    inherited stdin/stdout/stderr; when the child exits, the TUI is restored. The path is validated
    before launch — a record whose binary is missing or is a directory yields a clear status error
    and launches nothing. *Ops:* `InstallRepo.Get` + `os.Stat` (path resolution); the process exec
    lives in the view layer. No network, no bbolt write.

## User Stories

**Consuming (TUI) — UC 2–10, 13**
- As an operator, I want to browse the catalog of available micro-apps so I can see what exists. *(UC 2)*
- As an operator, I want to search/filter by name and category so I can find an app quickly. *(UC 3)*
- As an operator, I want to open an app and read its description, releases, and assets so I can decide
  what to install. *(UC 4, UC 5)*
- As an operator, I want to install an app with one keystroke and have the correct binary for my
  machine fetched and integrity-checked so I don't pick the wrong asset or a corrupted file. *(UC 6)*
- As an operator, I want to see what I've installed, update it, uninstall it, or re-verify it. *(UC 7–10)*
- As an operator, I want to launch an installed micro-app from inside microstore so I can run it
  without leaving the store and find it again when it exits. *(UC 13)*

**Creating (TUI) — UC 11–12**
- As a developer, I want to pick a starting template and scaffold a new project into a directory, then
  drop straight into `/product-idea`, so I can begin a new micro-app immediately. *(UC 11, UC 12)*

**Automation (MCP) — UC 2–12**
- As an LLM client, I want tools to browse/search/inspect the catalog and list installs so I can
  reason about available and installed apps. *(UC 2–5, 7, 11)*
- As an LLM client, I want tools to install, update, uninstall, verify, and scaffold so I can manage
  the local app set and bootstrap new apps on the user's behalf. *(UC 6, 8, 9, 10, 12)*

**Shared — UC 1**
- As any user, I want to configure the manifest URL and install directory once and have them
  persisted. *(UC 1)*

## MCP Surface

Server built with `mark3labs/mcp-go` (`internal/server`), transport-agnostic; stdio selected in
`cmd/` for `serve`/`mcp` mode. Capabilities: tools + resources, `WithRecovery()`, `WithLogging()`.
User/input failures → `mcp.NewToolResultError(...), nil`; infrastructure/transport failures → `nil, err`.
Non-trivial inputs use typed handlers with `jsonschema`-tagged structs. **All logging goes to stderr**
(stdout is the protocol channel).

### Tools

| Tool | Purpose (UC) | Input | Output |
|---|---|---|---|
| `get_config` | Read store config (UC 1) | — | `{ config: Config }` |
| `set_config` | Update store config; empty fields unchanged (UC 1) | `{ manifest_url?: string, install_dir?: string }` | `{ config: Config }` |
| `list_catalog` | List catalog app entries (UC 2) | — | `{ apps: ManifestEntry[] }` |
| `search_apps` | Filter catalog (UC 3) | `{ query?: string, category?: string }` | `{ apps: ManifestEntry[] }` |
| `app_details` | Repo info + latest release + assets + install state (UC 4) | `{ repo: string }` | `{ repo: RepoInfo, latest: Release, installed?: InstalledApp }` |
| `list_releases` | All releases for a repo (UC 5) | `{ repo: string }` | `{ releases: Release[] }` |
| `list_installed` | Tracked installs (UC 7) | — | `{ installed: InstalledApp[] }` |
| `install_app` | Match arch, verify, download, record (UC 6) | `{ repo: string, version?: string, asset?: string, allow_unverified?: bool }` | `{ installed: InstalledApp }`; on no/ambiguous match → error result listing assets |
| `update_app` | Upgrade to latest (UC 8) | `{ repo: string }` | `{ installed: InstalledApp, updated: bool, from: string, to: string }` |
| `uninstall_app` | Remove binary + record (UC 9) | `{ repo: string }` | `{ removed: bool }` |
| `verify_app` | Re-check SHA-256 (UC 10) | `{ repo: string }` | `{ status: "ok"\|"mismatch"\|"missing" }` |
| `list_templates` | Manifest templates (UC 11) | — | `{ templates: Template[] }` |
| `scaffold_app` | Extract template + hand off (UC 12) | `{ template_repo: string, target_dir: string, ref?: string, force?: bool }` | `{ target_dir: string, files: int, next_step: string }` (instructs caller to run `/product-idea`) |

### Resources

| URI | Purpose |
|---|---|
| `catalog://list` | The current catalog app entries (live) |
| `installed://list` | The tracked installs (from bbolt) |
| `templates://list` | The manifest's templates (live) |

### Prompts
None in v1 (see Open Questions).

> **Note on mutation via MCP.** `install_app`, `update_app`, `uninstall_app`, and `scaffold_app`
> perform network downloads and filesystem writes (including `chmod` and tarball extraction). Path
> inputs (`target_dir`, install dir) are cleaned and confined; tarball entries are checked against
> path traversal before extraction.
>
> **No `run_app` tool (UC 13 is TUI-only).** Launching an installed micro-app means handing it the
> controlling terminal — which the stdio MCP server cannot do, since its stdout *is* the protocol
> channel. UC 13 is therefore exposed only on the TUI's Installed screen and has no MCP tool or
> resource.

## TUI Surface

`internal/tui`, one `*tview.Application`, single event-loop goroutine. All GitHub/disk work runs in a
goroutine and funnels a **small** mutation back via `QueueUpdateDraw`; nothing blocks the event loop.
Five screens stacked in `Pages`; a persistent status bar shows progress/errors.

### Screens & navigation

```
                 ┌───────────────────────────────────────────────────────────┐
 Tab cycles ───▶ │ 1. Catalog  2. Detail  3. Installed  4. New  5. Config     │
                 └───────────────────────────────────────────────────────────┘

[1] Catalog        Table (DisplayName/Repo, Category). `/` search, category filter.
   │ Enter ───────▶ [2] Detail
[2] Detail         RepoInfo + releases + latest assets + install state.
   │                Keys: [i] install, [Esc] back. Ambiguous/no asset match ⇒ asset-pick list.
[3] Installed      Table (Repo, Version, InstalledAt, last verify state).
   │                Keys: [Enter] run (app.Suspend → exec the binary), [u] update,
   │                [x] uninstall (Modal confirm), [v] verify.
[4] New App        Form: choose Template (from manifest), Target dir (InputField).
   │                [Enter] scaffold ⇒ extract ⇒ app.Suspend → launch `claude /product-idea`
   │                (or print the command if `claude` is absent).
[5] Config         Form: Manifest URL + Install dir (InputField), [Save] persists.
                    Pre-filled from the stored config (defaults on first run).
```

### Launch-time PATH check
On startup the TUI checks whether the configured `InstallDir` is present on the current `$PATH`. If it
is **not** (and `InstallDir` is set), a modal warns that installed binaries won't be runnable from a
shell and shows the exact `export PATH="$PATH:<InstallDir>"` line. The modal offers two actions:
**Add to `<profile>`** appends that line to the user's shell profile (resolved from `$SHELL`:
`~/.zshrc` for zsh, `~/.bashrc` otherwise) — idempotently, creating the file if absent — and **Dismiss**
closes it. The profile edit takes effect in future shells; the running process's `PATH` is unchanged.
When `InstallDir` is already on `$PATH`, no modal appears.

### Key interactions
- **Global:** `Tab`/`Shift-Tab` cycle screens; `q` or `Ctrl-C` quit (always available); status bar
  reports in-flight network/disk operations and errors.
- **Catalog:** `/` focuses the search field; selecting a category filters; `Enter` opens Detail.
- **Detail:** `i` installs (auto-match → verify → download; on ambiguity, a selectable asset list
  appears); `Esc` returns to Catalog.
- **Installed:** `Enter` runs the highlighted app (suspends the TUI and execs its binary, restoring
  the TUI when it exits); `u` update, `x` uninstall (confirm via `Modal`), `v` re-verify; results
  update the row.
- **New App:** a `Form` (template dropdown + target-dir input); submit scaffolds, then suspends the UI
  to hand off to `/product-idea`.
- **Config:** a `Form` (manifest-URL + install-dir inputs) pre-filled from the stored config; `Save`
  persists and refreshes the catalog.
- Long operations show a busy indicator in the status bar and never freeze the loop.

## Acceptance Criteria

- **UC 1 — Config:** Fresh DB yields defaults (`InstallDir = ~/.local/share/microstore/bin`,
  `ManifestURL` = the curated catalog published from this repo). Setting and reloading returns the
  saved values. The config is editable from both faces — the TUI Config screen and the
  `get_config`/`set_config` MCP tools; `set_config` leaves omitted/empty fields unchanged. Catalog
  actions with an empty `ManifestURL` (if the user clears it) produce a clear "manifest URL not set"
  error, not a crash.
- **UC 2 — Catalog:** With a reachable manifest, all `Apps` entries are returned. With GitHub
  unreachable or a malformed manifest, a clear error is surfaced (no partial/silent success). Nothing
  is written to bbolt.
- **UC 3 — Search:** A query returns only entries matching name/repo (case-insensitive); a category
  filter returns only that category; combined filters intersect.
- **UC 4 — Details:** Returns the repo description and latest non-prerelease release with its assets;
  if a matching `InstalledApp` exists, the response includes the installed version.
- **UC 5 — Releases:** Returns releases newest-first; prereleases are flagged and excluded from
  "latest" resolution.
- **UC 6 — Install:** On a host where exactly one asset matches `GOOS/GOARCH`, microstore downloads it,
  the computed SHA-256 equals the `checksums.txt` entry, the file lands in `InstallDir` named
  `microapp-<name>` (the entry's `bin` override when set, else the repo's bare name, prefixed) mode
  `0755`, and an `InstalledApp` record exists
  keyed by slug with its `Path` pointing at that file. A checksum mismatch aborts the install, writes no
  record, and leaves no partial binary. Zero/ambiguous matches trigger manual selection (TUI) or an
  error result enumerating assets (MCP). A missing checksums file refuses install unless
  `allow_unverified` is set.
- **UC 7 — List installed:** Returns exactly the saved records, alphabetical by slug.
- **UC 8 — Update:** When a newer release exists, the binary and record move to the new version
  (`updated: true`, `from`/`to` reported); when already current, it is a reported no-op (`updated: false`).
- **UC 9 — Uninstall:** The binary at `Path` is gone and `InstallRepo.Get` returns `ErrNotFound`.
  Uninstalling an unknown slug yields a clear "not installed" error.
- **UC 10 — Re-verify:** Returns `ok` when the on-disk SHA-256 matches the record, `mismatch` when it
  differs, `missing` when the file is absent; the record is unchanged.
- **UC 11 — List templates:** Returns the manifest's `Templates` section; empty section yields an
  empty list, not an error.
- **UC 12 — Scaffold:** Into an empty target dir, the template's bare files are extracted with the
  tarball's top-level component stripped and no entry escaping the target (path-traversal entries are
  rejected). A non-empty target is refused unless `force`. After extraction, `/product-idea` is
  initiated (TUI launches `claude`, or prints the exact command if unavailable; MCP returns the
  next-step instruction). The module path is **not** modified and `git init` is **not** run.
- **UC 13 — Run installed app (TUI only):** `RunInstalled(repo)` returns the recorded absolute `Path`
  for a tracked install whose binary exists as a regular file. An unknown slug yields a clear "not
  installed" error; a record whose binary is missing or is a directory yields an error naming the
  path — in every error case nothing is executed. On the Installed screen, `Enter` resolves the
  highlighted row through `RunInstalled`, then suspends the TUI (`app.Suspend`) and runs the binary
  with inherited stdin/stdout/stderr; when the child exits the TUI is restored and the status bar
  reports the return. No MCP tool exposes this (the stdio server cannot own a terminal). No bbolt
  write occurs.
- **`init` mode:** In a directory with no `.claude` entry, `microstore init` places the embedded
  bootstrap kit byte-for-byte (`.claude/commands`, `.claude/rules`, `.claude/skills`) and prints the
  phase guide naming `/product-idea`, `/app-init`, and `/app-spec-sync` in that order plus the
  `build-and-release` skill, reporting `Initialized`. When a `.claude` **directory** already exists,
  init **updates in place**: every file the kit ships is rewritten to the embedded version while any
  user-added files under the kit's trees are left untouched, and it reports `Updated`. If a `.claude`
  entry exists but is **not** a directory, init refuses with a clear error and writes nothing. The
  mode never opens the database, performs no network I/O, and ignores `--db`. The kit is embedded
  from `templates/claude-code/` at build time.
- **PATH check (TUI launch):** When `InstallDir` is absent from `$PATH`, launching the TUI raises a
  modal showing the exact `export PATH="$PATH:<InstallDir>"` line and the target shell profile
  (`~/.zshrc` for zsh, else `~/.bashrc`). Confirming appends that line to the profile (idempotently;
  the file is created if missing) and leaves the running process's `PATH` unchanged; dismissing makes
  no change. When `InstallDir` is already on `$PATH`, no modal appears. microstore never modifies the
  profile without confirmation and never alters `PATH` by any other means.
- **Cross-cutting:** In `serve`/`mcp` mode no logs are written to stdout. bbolt is opened with a
  `Timeout`; a second writer process fails fast rather than hanging. The TUI never blocks its event
  loop during network/disk operations.

## Open Questions / Assumptions

- **Modes & lock.** Default invocation → TUI; `serve`/`mcp` → MCP stdio server; `init` → place the
  embedded Claude Code bootstrap kit into the current directory (or update it in place) and exit (no DB, no network);
  `--db <path>` overrides the DB location (default `~/.local/share/microstore/microstore.db`).
  Because bbolt takes a process-wide write lock, the TUI and the MCP server are **alternative
  modes**, not concurrent against one file. *Assumption accepted.*
- **GitHub auth.** Anonymous by default (60 req/hr). If `MICROSTORE_GITHUB_TOKEN` is set, requests use
  it (`Authorization: Bearer …`, 5000 req/hr, private repos). On HTTP 403 rate-limit, the error
  surfaces the limit/reset. *Assumption accepted.*
- **Manifest schema.** `catalog.json` is `{ "apps": ManifestEntry[], "templates": Template[] }`, fetched
  from `Config.ManifestURL` (a raw JSON URL). App entries are **minimal** (`repo`, `category`, optional
  `display_name`, optional `bin`); richer metadata is read live from GitHub. *Assumption — finalize the
  exact field names with the first published manifest.*
- **Self-hosting.** The curated catalog lists microstore itself (`Techthos/microstore`, `bin: "store"`),
  so the store installs and updates itself as `microapp-store` alongside the apps it manages. The
  `scripts/install.sh` bootstrap places that same file (`microapp-store` in the default `InstallDir`,
  `~/.local/share/microstore/bin`), so a later `install`/`update` of `Techthos/microstore` from within
  microstore overwrites it in place.
- **Asset naming convention.** An asset matches when its lower-cased name contains an OS token and an
  arch token for the host runtime, with aliases: `amd64`↔`x86_64`/`x64`, `arm64`↔`aarch64`,
  `386`↔`i386`/`x86`, `darwin`↔`macos`/`osx`, `windows`↔`win` (`.exe`). This matches the template's
  `build-and-release` output. *Assumption — irregularly named releases fall back to manual pick.*
- **Checksums.** Two artifact shapes are accepted, both parsed as `sha256sum`-style
  `<hex>  <filename>` lines (case-insensitive): a **per-asset sidecar** named `<asset>.sha256`
  (what the template's `build-and-release` workflow uploads — preferred when present), or an
  **aggregated** file named `checksums.txt` / `SHA256SUMS` (goreleaser-style). A single-entry
  sidecar falls back to its sole line if the recorded inner name differs. Sidecar files are excluded
  from host asset matching so they are never mistaken for an installable binary. No checksum source
  for the chosen asset ⇒ install refused unless explicitly allowed (`allow_unverified`).
- **`/product-idea` hand-off depends on the `claude` CLI** being on `PATH`; microstore's core never
  requires it — when absent it prints the exact command to run. *Assumption accepted.*
- **Prompts.** None in v1; a "suggest an app for a task" MCP prompt is a candidate for later.
- **No cross-arch override** in v1 (host arch only + manual asset pick). A `--os/--arch` override is a
  later candidate.
