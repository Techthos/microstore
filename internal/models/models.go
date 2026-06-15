// Package models holds microstore's plain domain structs. They are
// storage-agnostic — no bbolt (or any persistence) imports live here — and are
// serialized as JSON throughout, both into bbolt and across the MCP surface.
//
// Entities split into two groups:
//   - Live entities are fetched from GitHub on every use and never persisted.
//   - Persisted entities (InstalledApp, Config) are the only state kept in bbolt.
package models

import "time"

// --- Live entities (fetched from GitHub, never persisted) ---

// Catalog is the manifest document fetched from Config.ManifestURL.
type Catalog struct {
	Apps      []ManifestEntry `json:"apps"`
	Templates []Template      `json:"templates"`
}

// ManifestEntry is a minimal catalog listing for an installable micro-app;
// richer metadata is read live from GitHub via RepoInfo/Release.
type ManifestEntry struct {
	Repo        string `json:"repo"` // "owner/name"
	Category    string `json:"category"`
	DisplayName string `json:"display_name,omitempty"`
	// Description is a short, manifest-authored summary of what the app does,
	// shown alongside the live GitHub metadata.
	Description string `json:"description,omitempty"`
	// Bin optionally overrides the repo's bare name in the placed filename:
	// the installed binary is "microapp-<Bin>" instead of "microapp-<name>".
	// The catalog uses this to list microstore itself as "microapp-store".
	Bin string `json:"bin,omitempty"`
	// MCP, when present, tells an LLM client how to launch this app's MCP
	// server over stdio (mirrors an .mcp.json entry).
	MCP *MCPLaunch `json:"mcp,omitempty"`
}

// MCPLaunch describes how to start a micro-app's MCP server over stdio:
// an executable plus its arguments, mirroring an .mcp.json server entry.
type MCPLaunch struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Template is a catalog-listed starting point for scaffolding a new micro-app.
type Template struct {
	Repo        string `json:"repo"` // "owner/name"
	Ref         string `json:"ref"`  // branch or tag
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RepoInfo is the subset of a GitHub repository we surface.
type RepoInfo struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	Stars       int    `json:"stars"`
}

// Release is a GitHub release with its downloadable assets.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []Asset   `json:"assets"`
}

// Asset is a single downloadable file attached to a Release.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// --- Persisted entities (bbolt) ---

// InstalledApp records one installed binary, keyed by its repo slug. Field tags
// are stable: old records persist on disk across upgrades, so decoding must stay
// backward-compatible (additive fields, tolerate missing keys).
type InstalledApp struct {
	Repo        string    `json:"repo"` // "owner/name" — the bbolt key
	DisplayName string    `json:"display_name,omitempty"`
	Category    string    `json:"category,omitempty"`
	Bin         string    `json:"bin,omitempty"` // manifest bin override, kept so updates re-place at the same filename
	Version     string    `json:"version"`       // installed release tag
	AssetName   string    `json:"asset_name"`
	Path        string    `json:"path"` // absolute path of the placed binary
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installed_at"`
	SourceURL   string    `json:"source_url"`
	// MCP carries the manifest's MCP launch info forward from install, so the
	// Installed face can wire the app into a project's .mcp.json without a live
	// catalog fetch. Nil means the app advertises no MCP server.
	MCP *MCPLaunch `json:"mcp,omitempty"`
}

// Config is the singleton store configuration persisted under a well-known key.
//
// ManifestURL and InstallDir are the store settings, editable from both faces
// (TUI Config screen and the get_config/set_config MCP tools). LastSection and
// SidebarCollapsed are lightweight TUI view preferences (which section the app
// reopens on, and whether the sidebar starts collapsed); they are written only
// by the TUI, surfaced read-only by get_config, and never touched by set_config.
type Config struct {
	ManifestURL string `json:"manifest_url"`
	InstallDir  string `json:"install_dir"`

	LastSection      string `json:"last_section,omitempty"`
	SidebarCollapsed bool   `json:"sidebar_collapsed,omitempty"`
}
