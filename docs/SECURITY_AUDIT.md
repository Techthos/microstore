# Security Audit: microstore

_Date: 2026-05-28_

No HIGH or MEDIUM findings met the confidence threshold (≥8/10) for reporting. All three
candidate issues were investigated and filtered out as design/policy observations or
defense-in-depth hardening rather than concrete, exploitable vulnerabilities.

## Summary

A full review traced untrusted inputs (catalog.json fields, GitHub release metadata, downloaded
tarball entries, asset URLs/names, checksum-file contents) to sensitive sinks (file writes, file
paths, network requests, the bbolt DB). The highest-risk areas for this class of app — archive
extraction and binary placement — are well-defended.

## Candidate findings reviewed and dismissed

| Candidate | Category | Confidence | Disposition |
|---|---|---|---|
| Checksum trust anchor comes from the same release as the binary | Supply-chain integrity | 3/10 | Design observation. The SHA-256 check is an integrity guard against corrupted downloads, never claimed as an authenticity control. Trusting the upstream release publisher is the intended model (same as Homebrew / `go install`); no control is being bypassed. |
| `allow_unverified` skips verification | SHA verification bypass | 2/10 | Secure-by-default opt-out flag (defaults to `false`, documented). The caller who sets it is the operator overriding a control they own — a trusted flag, not an attacker-controllable input. |
| GitHub token attached to user-set `manifest_url` | Credential exposure | 4/10 | Mechanically real (the `Authorization: Bearer` header is sent to whatever host `manifest_url` points to, with no allowlist), but `manifest_url` is operator/user config — the same trust tier as the token. Worth hardening (scope the header to GitHub hosts), but not a concrete high-confidence vulnerability. |

## Areas reviewed and found solid

- **Tar extraction / zip-slip (`internal/scaffold/scaffold.go`):** `sanitizeEntry` rejects absolute
  paths and any `..` component after `path.Clean`; top-strip runs before sanitization without
  weakening it; symlinks/hardlinks/device entries are explicitly skipped. No traversal or
  symlink-plant path found.
- **Binary placement (`internal/install/install.go`):** Placed name is reduced to the final path
  segment of `repo` and `filepath.Join`-ed into the managed dir; traversal-style repo slugs are
  independently rejected by `github.parseRepo` before download. Download goes to a temp file and is
  atomically renamed only after the checksum check.
- **DB key encoding (`internal/db`):** Raw repo-slug byte keys in a dedicated bucket with JSON
  values; bbolt has no query language, so no injection surface.
- **Download SSRF / `file://`:** Asset download URLs originate from GitHub's API
  (`browser_download_url`), and the HTTP client does not handle `file://`.
- **TLS:** Default `http.Client`/transport; certificate verification on, no `InsecureSkipVerify`.

## Optional hardening (not vulnerabilities)

1. Scope the `Authorization` header in `internal/github/github.go` `do()` to GitHub API hosts only,
   so a non-GitHub `manifest_url` never receives the token.
2. Consider supporting a catalog-pinned digest or signature as the trust anchor for installs, rather
   than relying on the release-bundled `checksums.txt`, if a stronger upstream-trust model is
   desired.
