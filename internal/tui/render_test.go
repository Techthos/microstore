package tui

import (
	"strings"
	"testing"
	"time"

	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/models"
)

func TestCatalogRow(t *testing.T) {
	t.Parallel()
	got := catalogRow(models.ManifestEntry{Repo: "o/a", Category: "tools", DisplayName: "Alpha"})
	want := []string{"Alpha", "o/a", "tools"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("catalogRow = %v, want %v", got, want)
	}
	// Falls back to repo when DisplayName is empty.
	got = catalogRow(models.ManifestEntry{Repo: "o/b", Category: "x"})
	if got[0] != "o/b" {
		t.Errorf("name fallback = %q, want o/b", got[0])
	}
}

func TestInstalledRow(t *testing.T) {
	t.Parallel()
	ia := models.InstalledApp{Repo: "o/a", Version: "v1", InstalledAt: time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)}
	got := installedRow(ia, "")
	if got[0] != "o/a" || got[1] != "v1" || got[2] != "2026-05-28" || got[3] != "-" {
		t.Errorf("installedRow = %v", got)
	}
	if installedRow(ia, "ok")[3] != "ok" {
		t.Errorf("verify column not propagated")
	}
}

func TestFilterApps(t *testing.T) {
	t.Parallel()
	apps := []models.ManifestEntry{
		{Repo: "o/alpha", Category: "tools", DisplayName: "Alpha"},
		{Repo: "o/beta", Category: "games", DisplayName: "Beta"},
		{Repo: "o/altimeter", Category: "tools", DisplayName: "Altimeter"},
	}
	if got := filterApps(apps, "", ""); len(got) != 3 {
		t.Errorf("no filter = %d, want 3", len(got))
	}
	if got := filterApps(apps, "", "tools"); len(got) != 2 {
		t.Errorf("category = %d, want 2", len(got))
	}
	if got := filterApps(apps, "alt", ""); len(got) != 1 || got[0].Repo != "o/altimeter" {
		t.Errorf("query = %v", got)
	}
	if got := filterApps(apps, "al", "tools"); len(got) != 2 {
		t.Errorf("combined = %d, want 2", len(got))
	}
}

func TestDistinctCategories(t *testing.T) {
	t.Parallel()
	apps := []models.ManifestEntry{
		{Category: "tools"}, {Category: "games"}, {Category: "tools"}, {Category: ""},
	}
	got := distinctCategories(apps)
	if strings.Join(got, ",") != "games,tools" {
		t.Errorf("distinctCategories = %v, want [games tools]", got)
	}
}

func TestTabsText(t *testing.T) {
	t.Parallel()
	got := tabsText(pageInstalled)
	for _, want := range []string{"1 Catalog", "3 Installed", "5 Config"} {
		if !strings.Contains(got, want) {
			t.Errorf("tabsText missing %q in:\n%s", want, got)
		}
	}
	// The active page is highlighted with the reverse style tag.
	if !strings.Contains(got, "[black:aqua:b] 3 Installed ") {
		t.Errorf("active tab not highlighted: %s", got)
	}
}

func TestHintFor(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		pageCatalog:   "search",
		pageDetail:    "install",
		pageInstalled: "uninstall",
		pageNew:       "scaffold",
		pageConfig:    "save",
	}
	for page, want := range cases {
		if h := hintFor(page); !strings.Contains(h, want) {
			t.Errorf("hintFor(%q) missing %q in %q", page, want, h)
		}
		if h := hintFor(page); !strings.Contains(h, "quit") {
			t.Errorf("hintFor(%q) missing global quit hint", page)
		}
	}
}

func TestPathWarningText(t *testing.T) {
	t.Parallel()
	st := app.PathStatus{
		InstallDir:  "/home/u/.local/share/microstore/bin",
		ProfilePath: "/home/u/.bashrc",
		ExportLine:  `export PATH="$PATH:/home/u/.local/share/microstore/bin"`,
	}
	got := pathWarningText(st)
	for _, want := range []string{st.InstallDir, st.ProfilePath, st.ExportLine, "not on your PATH"} {
		if !strings.Contains(got, want) {
			t.Errorf("pathWarningText missing %q in:\n%s", want, got)
		}
	}
}

func TestNextPrevPage(t *testing.T) {
	t.Parallel()
	if nextPage(pageCatalog) != pageDetail {
		t.Errorf("next(catalog) = %q", nextPage(pageCatalog))
	}
	if nextPage(pageNew) != pageConfig {
		t.Errorf("next(new) = %q, want config", nextPage(pageNew))
	}
	if nextPage(pageConfig) != pageCatalog {
		t.Errorf("next wraps to catalog, got %q", nextPage(pageConfig))
	}
	if prevPage(pageCatalog) != pageConfig {
		t.Errorf("prev wraps to config, got %q", prevPage(pageCatalog))
	}
	if nextPage("unknown") != pageCatalog {
		t.Errorf("unknown falls back to catalog")
	}
}

func TestDetailText(t *testing.T) {
	t.Parallel()
	d := app.AppDetails{
		Repo:   models.RepoInfo{FullName: "o/app", Description: "desc", Stars: 5},
		Latest: models.Release{TagName: "v1.0.0", Assets: []models.Asset{{Name: "bin", Size: 2048}}},
	}
	txt := detailText(d)
	for _, want := range []string{"o/app", "desc", "v1.0.0", "bin", "Not installed"} {
		if !strings.Contains(txt, want) {
			t.Errorf("detailText missing %q in:\n%s", want, txt)
		}
	}
	d.Installed = &models.InstalledApp{Version: "v0.9.0"}
	if !strings.Contains(detailText(d), "v0.9.0") {
		t.Errorf("detailText should show installed version")
	}
}

func TestHumanSize(t *testing.T) {
	t.Parallel()
	tests := map[int64]string{0: "0 B", 512: "512 B", 2048: "2.0 KB", 1048576: "1.0 MB"}
	for in, want := range tests {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
