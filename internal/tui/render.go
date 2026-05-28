// Package tui is microstore's terminal UI, built on rivo/tview. It owns the one
// *tview.Application and is a thin view layer: all data flows through the
// injected Service (the internal/app use-case layer). Network and disk work runs
// off the event loop and is funnelled back via QueueUpdateDraw; render logic
// lives in the pure helpers below so it is testable without the Application.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/models"
)

// Page identifiers within the root Pages primitive.
const (
	pageCatalog   = "catalog"
	pageDetail    = "detail"
	pageInstalled = "installed"
	pageNew       = "new"
	pageConfig    = "config"
	pageAssetPick = "assetpick"
	pageConfirm   = "confirm"
	pageWarnPath  = "warnpath"
)

// pageOrder is the Tab-cycle order of the primary screens.
var pageOrder = []string{pageCatalog, pageDetail, pageInstalled, pageNew, pageConfig}

// tabLabels is the human label shown for each primary page in the tab bar. The
// 1-based position in pageOrder doubles as the quick-switch number key.
var tabLabels = map[string]string{
	pageCatalog:   "Catalog",
	pageDetail:    "Detail",
	pageInstalled: "Installed",
	pageNew:       "New App",
	pageConfig:    "Config",
}

// tabsText renders the top tab bar as dynamic-color markup, highlighting the
// active page. The leading number matches the 1-5 quick-switch keys.
func tabsText(active string) string {
	parts := make([]string, 0, len(pageOrder))
	for i, p := range pageOrder {
		label := fmt.Sprintf(" %d %s ", i+1, tabLabels[p])
		if p == active {
			parts = append(parts, "[black:aqua:b]"+label+"[-:-:-]")
		} else {
			parts = append(parts, "[aqua]"+label+"[-]")
		}
	}
	return strings.Join(parts, " ")
}

// hintFor returns the context-sensitive keybinding hints for a page, shown in
// the footer hint bar so every shortcut is discoverable on screen.
func hintFor(page string) string {
	const global = "[::b]1-5[::-]/[::b]Tab[::-] switch  [::b]q[::-] quit"
	switch page {
	case pageCatalog:
		return "[::b]↑↓[::-] move  [::b]Enter[::-] details  [::b]/[::-] search  " + global
	case pageDetail:
		return "[::b]i[::-] install  [::b]Esc[::-] back  " + global
	case pageInstalled:
		return "[::b]u[::-] update  [::b]x[::-] uninstall  [::b]v[::-] verify  " + global
	case pageNew:
		return "[::b]↑↓[::-] fields  [::b]Enter[::-] scaffold  " + global
	case pageConfig:
		return "[::b]↑↓[::-] fields  [::b]Enter[::-] save  " + global
	}
	return global
}

// pathWarningText renders the launch-time warning shown when InstallDir is not
// on $PATH: it names the directory, explains why installed binaries won't run,
// and shows the exact export line plus the profile it can be added to.
func pathWarningText(st app.PathStatus) string {
	return fmt.Sprintf(
		"Install dir is not on your PATH:\n\n  %s\n\nInstalled apps won't be runnable from your shell until it is.\n"+
			"Add this line to %s?\n\n  %s",
		st.InstallDir, st.ProfilePath, st.ExportLine)
}

func nextPage(cur string) string { return cyclePage(cur, +1) }
func prevPage(cur string) string { return cyclePage(cur, -1) }

func cyclePage(cur string, delta int) string {
	for i, p := range pageOrder {
		if p == cur {
			n := (i + delta + len(pageOrder)) % len(pageOrder)
			return pageOrder[n]
		}
	}
	return pageCatalog
}

var catalogHeader = []string{"Name", "Repo", "Category"}

func catalogRow(e models.ManifestEntry) []string {
	name := e.DisplayName
	if name == "" {
		name = e.Repo
	}
	return []string{name, e.Repo, e.Category}
}

var installedHeader = []string{"Repo", "Version", "Installed", "Verify"}

func installedRow(a models.InstalledApp, verify string) []string {
	if verify == "" {
		verify = "-"
	}
	return []string{a.Repo, a.Version, a.InstalledAt.Format("2006-01-02"), verify}
}

// filterApps applies the catalog's in-memory search: free-text on name/repo
// (case-insensitive) and/or an exact category. Empty filters match everything.
func filterApps(apps []models.ManifestEntry, query, category string) []models.ManifestEntry {
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
	return out
}

// distinctCategories returns the sorted unique categories present in apps.
func distinctCategories(apps []models.ManifestEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range apps {
		if e.Category != "" && !seen[e.Category] {
			seen[e.Category] = true
			out = append(out, e.Category)
		}
	}
	sort.Strings(out)
	return out
}

// detailText renders an app's details as dynamic-color markup for the TextView.
func detailText(d app.AppDetails) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[::b]%s[::-]", orDash(d.Repo.FullName))
	if d.Repo.Stars > 0 {
		fmt.Fprintf(&b, "   [yellow]★ %d[-]", d.Repo.Stars)
	}
	b.WriteString("\n")
	if d.Repo.Description != "" {
		fmt.Fprintf(&b, "%s\n", d.Repo.Description)
	}
	if d.Repo.Homepage != "" {
		fmt.Fprintf(&b, "[blue]%s[-]\n", d.Repo.Homepage)
	}
	b.WriteString("\n")

	if d.Latest.TagName == "" {
		b.WriteString("[gray]No published release.[-]\n")
	} else {
		fmt.Fprintf(&b, "[::b]Latest:[::-] %s", d.Latest.TagName)
		if !d.Latest.PublishedAt.IsZero() {
			fmt.Fprintf(&b, "  (%s)", d.Latest.PublishedAt.Format("2006-01-02"))
		}
		b.WriteString("\n[::b]Assets:[::-]\n")
		if len(d.Latest.Assets) == 0 {
			b.WriteString("  [gray](none)[-]\n")
		}
		for _, a := range d.Latest.Assets {
			fmt.Fprintf(&b, "  - %s [gray](%s)[-]\n", a.Name, humanSize(a.Size))
		}
	}

	b.WriteString("\n")
	if d.Installed != nil {
		fmt.Fprintf(&b, "[green]Installed:[-] %s\n", d.Installed.Version)
	} else {
		b.WriteString("[gray]Not installed.[-]\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
