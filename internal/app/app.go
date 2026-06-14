// Package app is microstore's use-case layer. It orchestrates the GitHub client,
// the bbolt repositories, the installer, and the scaffolder into the twelve
// product use-cases, returning plain domain models. Both internal/server (MCP)
// and internal/tui depend on this package so the orchestration lives in exactly
// one place.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"techthos.net/microstore/internal/db"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
	"techthos.net/microstore/internal/scaffold"
)

// Cataloger is the GitHub surface the service needs. *github.Client satisfies it.
type Cataloger interface {
	FetchCatalog(ctx context.Context, manifestURL string) (models.Catalog, error)
	RepoInfo(ctx context.Context, repo string) (models.RepoInfo, error)
	Releases(ctx context.Context, repo string) ([]models.Release, error)
	LatestRelease(ctx context.Context, repo string) (models.Release, error)
	Download(ctx context.Context, url string, w io.Writer) (int64, error)
	Tarball(ctx context.Context, repo, ref string) (io.ReadCloser, error)
}

// Service wires the dependencies behind the use-case methods.
type Service struct {
	gh       Cataloger
	cfg      *db.ConfigRepo
	installs *db.InstallRepo
	scaff    *scaffold.Scaffolder
}

// New builds a Service from a GitHub client and an open Store.
func New(gh Cataloger, store *db.Store) *Service {
	return &Service{
		gh:       gh,
		cfg:      store.Config(),
		installs: store.Installs(),
		scaff:    scaffold.New(gh),
	}
}

// AssetSelectionError is returned by Install when no single asset matches the
// host (zero or ambiguous), carrying the available assets so a caller can offer
// a manual choice.
type AssetSelectionError struct {
	Repo    string
	Version string
	Reason  string
	Assets  []models.Asset
}

func (e *AssetSelectionError) Error() string {
	names := make([]string, len(e.Assets))
	for i, a := range e.Assets {
		names[i] = a.Name
	}
	return fmt.Sprintf("%s %s: %s; available assets: %s", e.Repo, e.Version, e.Reason, strings.Join(names, ", "))
}

// --- UC 1: configuration ---

// GetConfig returns the persisted configuration (defaults when unset).
func (s *Service) GetConfig() (models.Config, error) { return s.cfg.Load() }

// SetConfig persists the configuration.
func (s *Service) SetConfig(c models.Config) error { return s.cfg.Save(c) }

// PathStatus reports whether the configured InstallDir is reachable on the
// current process PATH and, when it is not, the shell profile and the exact
// export line that would put it there. microstore never rewrites PATH on its own
// — this only surfaces the advice (see AddToPath for the opt-in append).
type PathStatus struct {
	InstallDir  string // the configured managed install directory
	OnPath      bool   // whether InstallDir is already on $PATH
	ProfilePath string // resolved shell rc file to edit (e.g. ~/.bashrc, ~/.zshrc)
	ExportLine  string // the line that appends InstallDir to PATH
}

// PathStatus loads the config and inspects the live environment ($PATH, $SHELL,
// home dir) so the TUI can warn — on launch — that installed binaries won't be
// runnable until InstallDir is on PATH.
func (s *Service) PathStatus() (PathStatus, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return PathStatus{}, err
	}
	home, _ := os.UserHomeDir()
	return PathStatus{
		InstallDir:  cfg.InstallDir,
		OnPath:      install.OnPath(cfg.InstallDir, os.Getenv("PATH")),
		ProfilePath: install.ProfilePath(os.Getenv("SHELL"), home),
		ExportLine:  install.ExportLine(cfg.InstallDir),
	}, nil
}

// AddToPath appends the InstallDir export line to the user's shell profile so
// installed binaries land on PATH for future shells. It is idempotent and
// returns the status it acted on (the current process PATH is unchanged until
// the profile is re-sourced).
func (s *Service) AddToPath() (PathStatus, error) {
	st, err := s.PathStatus()
	if err != nil {
		return PathStatus{}, err
	}
	if err := install.AppendExport(st.ProfilePath, st.ExportLine); err != nil {
		return PathStatus{}, err
	}
	return st, nil
}

// MergeConfig overlays the provided non-empty fields onto the current config and
// saves the result, returning it. An empty argument leaves that field unchanged
// (so callers can update one field without clearing the other).
func (s *Service) MergeConfig(manifestURL, installDir string) (models.Config, error) {
	cur, err := s.cfg.Load()
	if err != nil {
		return models.Config{}, err
	}
	if manifestURL != "" {
		cur.ManifestURL = manifestURL
	}
	if installDir != "" {
		cur.InstallDir = installDir
	}
	if err := s.cfg.Save(cur); err != nil {
		return models.Config{}, err
	}
	return cur, nil
}

// --- UC 2 / 3: catalog ---

// ListCatalog fetches the manifest live and returns its app entries.
func (s *Service) ListCatalog(ctx context.Context) ([]models.ManifestEntry, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return nil, err
	}
	cat, err := s.gh.FetchCatalog(ctx, cfg.ManifestURL)
	if err != nil {
		return nil, err
	}
	return cat.Apps, nil
}

// SearchApps filters the live catalog by free-text (name/repo, case-insensitive)
// and/or category. Empty filters match everything.
func (s *Service) SearchApps(ctx context.Context, query, category string) ([]models.ManifestEntry, error) {
	apps, err := s.ListCatalog(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []models.ManifestEntry
	for _, e := range apps {
		if category != "" && !strings.EqualFold(e.Category, category) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.DisplayName), q) && !strings.Contains(strings.ToLower(e.Repo), q) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// --- UC 4 / 5: details & releases ---

// AppDetails bundles a repo's live metadata, its latest non-prerelease release,
// and the install record if one exists.
type AppDetails struct {
	Repo      models.RepoInfo      `json:"repo"`
	Latest    models.Release       `json:"latest"`
	Installed *models.InstalledApp `json:"installed,omitempty"`
}

// AppDetails fetches repo info + latest release live and annotates install state.
func (s *Service) AppDetails(ctx context.Context, repo string) (AppDetails, error) {
	info, err := s.gh.RepoInfo(ctx, repo)
	if err != nil {
		return AppDetails{}, err
	}
	rels, err := s.gh.Releases(ctx, repo)
	if err != nil {
		return AppDetails{}, err
	}
	var latest models.Release
	for _, r := range rels {
		if !r.Prerelease {
			latest = r
			break
		}
	}
	details := AppDetails{Repo: info, Latest: latest}
	if rec, err := s.installs.Get(repo); err == nil {
		details.Installed = rec
	} else if !errors.Is(err, db.ErrNotFound) {
		return AppDetails{}, err
	}
	return details, nil
}

// ListReleases returns all releases for a repo, newest-first.
func (s *Service) ListReleases(ctx context.Context, repo string) ([]models.Release, error) {
	return s.gh.Releases(ctx, repo)
}

// --- UC 6 / 8: install & update ---

// Install resolves the target release (latest non-prerelease, or version),
// selects the asset (explicit name, or host auto-match), verifies and places it,
// and records the install. A zero/ambiguous auto-match returns *AssetSelectionError.
func (s *Service) Install(ctx context.Context, repo, version, assetName string, allowUnverified bool) (models.InstalledApp, error) {
	return s.doInstall(ctx, s.lookupEntry(ctx, repo), version, assetName, allowUnverified)
}

func (s *Service) doInstall(ctx context.Context, entry models.ManifestEntry, version, assetName string, allowUnverified bool) (models.InstalledApp, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return models.InstalledApp{}, err
	}
	rel, err := s.resolveRelease(ctx, entry.Repo, version)
	if err != nil {
		return models.InstalledApp{}, err
	}
	asset, err := selectAsset(entry.Repo, rel, assetName)
	if err != nil {
		return models.InstalledApp{}, err
	}
	rec, err := install.New(s.gh, cfg.InstallDir).Install(ctx, entry, rel, asset, install.Options{AllowUnverified: allowUnverified})
	if err != nil {
		return models.InstalledApp{}, err
	}
	if err := s.installs.Save(rec); err != nil {
		return models.InstalledApp{}, fmt.Errorf("record install: %w", err)
	}
	return rec, nil
}

// UpdateResult reports the outcome of an update attempt.
type UpdateResult struct {
	Installed models.InstalledApp `json:"installed"`
	Updated   bool                `json:"updated"`
	From      string              `json:"from"`
	To        string              `json:"to"`
}

// Update upgrades a tracked install to the latest release, or reports a no-op
// when already current.
func (s *Service) Update(ctx context.Context, repo string) (UpdateResult, error) {
	existing, err := s.installs.Get(repo)
	if errors.Is(err, db.ErrNotFound) {
		return UpdateResult{}, fmt.Errorf("%s is not installed", repo)
	}
	if err != nil {
		return UpdateResult{}, err
	}
	latest, err := s.gh.LatestRelease(ctx, repo)
	if err != nil {
		return UpdateResult{}, err
	}
	if latest.TagName == existing.Version {
		return UpdateResult{Installed: *existing, Updated: false, From: existing.Version, To: existing.Version}, nil
	}
	entry := models.ManifestEntry{Repo: existing.Repo, DisplayName: existing.DisplayName, Category: existing.Category, Bin: existing.Bin}
	rec, err := s.doInstall(ctx, entry, latest.TagName, "", false)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Installed: rec, Updated: true, From: existing.Version, To: latest.TagName}, nil
}

// --- UC 7 / 9 / 10: list, uninstall, verify ---

// ListInstalled returns tracked installs, alphabetical by slug.
func (s *Service) ListInstalled() ([]models.InstalledApp, error) { return s.installs.List() }

// Uninstall removes the binary and the record for repo.
func (s *Service) Uninstall(repo string) error {
	existing, err := s.installs.Get(repo)
	if errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("%s is not installed", repo)
	}
	if err != nil {
		return err
	}
	if err := install.Remove(existing.Path); err != nil {
		return err
	}
	return s.installs.Delete(repo)
}

// Verify recomputes the on-disk SHA-256 of a tracked install and compares it to
// the recorded hash.
func (s *Service) Verify(repo string) (install.VerifyStatus, error) {
	existing, err := s.installs.Get(repo)
	if errors.Is(err, db.ErrNotFound) {
		return "", fmt.Errorf("%s is not installed", repo)
	}
	if err != nil {
		return "", err
	}
	return install.Verify(existing.Path, existing.SHA256)
}

// --- UC 13: run an installed app ---

// RunInstalled resolves a tracked install to its on-disk binary path so a caller
// can hand the terminal to it. It does not execute the binary — process handoff
// (e.g. the TUI's app.Suspend) is the view layer's concern — but it guarantees
// the recorded path exists and is a regular file, returning a clear error
// otherwise, so a stale record whose binary was deleted out-of-band fails loudly
// rather than spawning nothing.
func (s *Service) RunInstalled(repo string) (string, error) {
	existing, err := s.installs.Get(repo)
	if errors.Is(err, db.ErrNotFound) {
		return "", fmt.Errorf("%s is not installed", repo)
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(existing.Path)
	if err != nil {
		return "", fmt.Errorf("%s binary missing at %s: %w", repo, existing.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s path is a directory, not a binary: %s", repo, existing.Path)
	}
	return existing.Path, nil
}

// --- UC 11 / 12: templates & scaffold ---

// ListTemplates returns the manifest's templates section, fetched live.
func (s *Service) ListTemplates(ctx context.Context) ([]models.Template, error) {
	cfg, err := s.cfg.Load()
	if err != nil {
		return nil, err
	}
	cat, err := s.gh.FetchCatalog(ctx, cfg.ManifestURL)
	if err != nil {
		return nil, err
	}
	return cat.Templates, nil
}

// ScaffoldResult reports a completed scaffold and the next workflow step.
type ScaffoldResult struct {
	TargetDir string `json:"target_dir"`
	Files     int    `json:"files"`
	NextStep  string `json:"next_step"`
}

// Scaffold extracts a template into targetDir (resolving the ref from the catalog
// when not given) and returns the hand-off instruction to run /product-idea.
func (s *Service) Scaffold(ctx context.Context, templateRepo, targetDir, ref string, force bool) (ScaffoldResult, error) {
	if strings.TrimSpace(ref) == "" {
		ref = s.lookupTemplateRef(ctx, templateRepo)
	}
	if strings.TrimSpace(ref) == "" {
		return ScaffoldResult{}, fmt.Errorf("no ref given and %s is not a catalog template", templateRepo)
	}
	files, err := s.scaff.Scaffold(ctx, templateRepo, ref, targetDir, scaffold.Options{Force: force})
	if err != nil {
		return ScaffoldResult{}, err
	}
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		abs = targetDir
	}
	return ScaffoldResult{
		TargetDir: abs,
		Files:     files,
		NextStep:  fmt.Sprintf("Run /product-idea in %s to begin building the new micro-app.", abs),
	}, nil
}

// --- helpers ---

func (s *Service) resolveRelease(ctx context.Context, repo, version string) (models.Release, error) {
	if version == "" {
		return s.gh.LatestRelease(ctx, repo)
	}
	rels, err := s.gh.Releases(ctx, repo)
	if err != nil {
		return models.Release{}, err
	}
	for _, r := range rels {
		if r.TagName == version {
			return r, nil
		}
	}
	return models.Release{}, fmt.Errorf("release %q not found for %s", version, repo)
}

func selectAsset(repo string, rel models.Release, assetName string) (models.Asset, error) {
	if assetName != "" {
		for _, a := range rel.Assets {
			if a.Name == assetName {
				return a, nil
			}
		}
		return models.Asset{}, &AssetSelectionError{Repo: repo, Version: rel.TagName, Reason: fmt.Sprintf("asset %q not found", assetName), Assets: rel.Assets}
	}
	matches := install.MatchAssets(rel.Assets, install.HostOS(), install.HostArch())
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return models.Asset{}, &AssetSelectionError{Repo: repo, Version: rel.TagName, Reason: "no asset matched this host", Assets: rel.Assets}
	default:
		return models.Asset{}, &AssetSelectionError{Repo: repo, Version: rel.TagName, Reason: "multiple assets matched this host", Assets: rel.Assets}
	}
}

func (s *Service) lookupEntry(ctx context.Context, repo string) models.ManifestEntry {
	if apps, err := s.ListCatalog(ctx); err == nil {
		for _, e := range apps {
			if e.Repo == repo {
				return e
			}
		}
	}
	return models.ManifestEntry{Repo: repo}
}

func (s *Service) lookupTemplateRef(ctx context.Context, repo string) string {
	tmpls, err := s.ListTemplates(ctx)
	if err != nil {
		return ""
	}
	for _, t := range tmpls {
		if t.Repo == repo {
			return t.Ref
		}
	}
	return ""
}
