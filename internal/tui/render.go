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

// headerText is the static brand bar drawn above the tabs.
const headerText = "[" + tagAccent + "::b] ◆ microstore " + keyClose +
	" [" + tagDim + "]local app store for Go micro-apps[-]"

// tabsText renders the top tab bar as dynamic-color markup, highlighting the
// active page. The leading number matches the 1-5 quick-switch keys.
func tabsText(active string) string {
	parts := make([]string, 0, len(pageOrder))
	for i, p := range pageOrder {
		label := fmt.Sprintf(" %d %s ", i+1, tabLabels[p])
		if p == active {
			parts = append(parts, "["+hexBg+":"+tagAccent+":b]"+label+"[-:-:-]")
		} else {
			parts = append(parts, "["+tagAccent+"]"+label+"[-]")
		}
	}
	return strings.Join(parts, " ")
}

// hintFor returns the context-sensitive keybinding hints for a page, shown in
// the footer hint bar so every shortcut is discoverable on screen.
func hintFor(page string) string {
	global := k("1-5") + "/" + k("Tab") + " switch  " + k("q") + " quit"
	switch page {
	case pageCatalog:
		return k("↑↓") + " move  " + k("Enter") + " details  " + k("/") + " search  " + global
	case pageDetail:
		return k("i") + " install  " + k("Esc") + " back  " + global
	case pageInstalled:
		return k("Enter") + " run  " + k("u") + " update  " + k("x") + " uninstall  " + k("v") + " verify  " + global
	case pageNew:
		return k("↑↓") + " fields  " + k("Enter") + " scaffold  " + global
	case pageConfig:
		return k("↑↓") + " fields  " + k("Enter") + " save  " + global
	}
	return global
}

// k wraps a keybinding glyph in the accent+bold key style for the hint bar.
func k(glyph string) string { return keyOpen + glyph + keyClose }

// pathWarningText renders the launch-time warning shown when InstallDir is not
// on $PATH: it names the directory, explains why installed binaries won't run,
// and shows the exact export line plus the profile it can be added to.
func pathWarningText(st app.PathStatus) string {
	return fmt.Sprintf(
		"Install dir is not on your PATH:\n\n  %s\n\nInstalled apps won't be runnable from your shell until it is.\n"+
			"Add this line to %s?\n\n  %s",
		st.InstallDir, st.ProfilePath, st.ExportLine,
	)
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
	return []string{name, "[" + tagDim + "]" + e.Repo + "[-]", categoryTag(e.Category)}
}

// categoryPalette is the rotation of accent colors that categoryTag draws from,
// chosen by a stable hash so the same category always gets the same color.
var categoryPalette = []string{tagAccent, tagAccent2, tagGood, tagWarn, "#7dcfff", "#ff9e64"}

func categoryTag(cat string) string {
	if cat == "" {
		return ""
	}
	var h uint32
	for _, r := range cat {
		h = h*31 + uint32(r)
	}
	return "[" + categoryPalette[h%uint32(len(categoryPalette))] + "]" + cat + "[-]"
}

var installedHeader = []string{"Repo", "Version", "Installed", "Verify"}

func installedRow(a models.InstalledApp, verify string) []string {
	return []string{a.Repo, a.Version, a.InstalledAt.Format("2006-01-02"), verifyCell(verify)}
}

// verifyCell maps a re-verification status to a colored glyph + label for the
// Installed table. The empty status means "not re-verified this session".
func verifyCell(status string) string {
	switch status {
	case "ok":
		return "[" + tagGood + "]✓ verified[-]"
	case "mismatch":
		return "[" + tagBad + "]✗ mismatch[-]"
	case "missing":
		return "[" + tagBad + "]✗ missing[-]"
	default:
		return "[" + tagDim + "]– not checked[-]"
	}
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
	fmt.Fprintf(&b, "["+tagAccent+"::b]%s"+keyClose, orDash(d.Repo.FullName))
	if d.Repo.Stars > 0 {
		fmt.Fprintf(&b, "   ["+tagWarn+"]★ %d[-]", d.Repo.Stars)
	}
	b.WriteString("\n")
	if d.Repo.Description != "" {
		fmt.Fprintf(&b, "["+tagText+"]%s[-]\n", d.Repo.Description)
	}
	if d.Repo.Homepage != "" {
		fmt.Fprintf(&b, "["+tagAccent+"]%s[-]\n", d.Repo.Homepage)
	}
	b.WriteString("\n")

	if d.Latest.TagName == "" {
		b.WriteString("[" + tagDim + "]No published release.[-]\n")
	} else {
		fmt.Fprintf(&b, "["+tagAccent2+"::b]Latest:"+keyClose+" %s", d.Latest.TagName)
		if !d.Latest.PublishedAt.IsZero() {
			fmt.Fprintf(&b, "  [%s](%s)[-]", tagDim, d.Latest.PublishedAt.Format("2006-01-02"))
		}
		b.WriteString("\n[" + tagAccent2 + "::b]Assets:" + keyClose + "\n")
		if len(d.Latest.Assets) == 0 {
			b.WriteString("  [" + tagDim + "](none)[-]\n")
		}
		for _, a := range d.Latest.Assets {
			fmt.Fprintf(&b, "  • %s ["+tagDim+"](%s)[-]\n", a.Name, humanSize(a.Size))
		}
	}

	b.WriteString("\n")
	if d.Installed != nil {
		fmt.Fprintf(&b, "["+tagGood+"::b]✓ Installed:"+keyClose+" %s\n", d.Installed.Version)
	} else {
		b.WriteString("[" + tagDim + "]Not installed.[-]\n")
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
