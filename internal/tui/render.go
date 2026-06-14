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
	"time"

	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/models"
)

// emDash is the dim placeholder for an empty/missing value (never blank, never
// "null"), per the shared design language.
const emDash = "—"

// Page identifiers within the body Pages primitive. The first group are the
// navigable sidebar sections; the second are transient overlays layered on top.
const (
	pageCatalog   = "catalog"
	pageInstalled = "installed"
	pageNew       = "new"
	pageConfig    = "config"

	pageAssetPick = "assetpick"
	pageConfirm   = "confirm"
	pageWarnPath  = "warnpath"
	pageHelp      = "help"
)

// sectionOrder is the sidebar's top-to-bottom order; the 1-based position is the
// numeric shortcut (1–4) that jumps to each section.
var sectionOrder = []string{pageCatalog, pageInstalled, pageNew, pageConfig}

// sectionTitles is the human label for each navigable section.
var sectionTitles = map[string]string{
	pageCatalog:   "Catalog",
	pageInstalled: "Installed",
	pageNew:       "New App",
	pageConfig:    "Config",
}

// isSection reports whether page is a navigable sidebar section (not an overlay).
func isSection(page string) bool {
	_, ok := sectionTitles[page]
	return ok
}

// sectionIndex returns the 0-based position of a section in sectionOrder, or -1.
func sectionIndex(page string) int {
	for i, s := range sectionOrder {
		if s == page {
			return i
		}
	}
	return -1
}

// screenTitle is the body header text for a section.
func screenTitle(section string) string {
	if t, ok := sectionTitles[section]; ok {
		return t
	}
	return "microstore"
}

// sidebarItems renders the sidebar labels: a numeric shortcut, the section title,
// an optional dim count, and an attention badge (●) when a section needs it. The
// active section is highlighted by the List's own selection, not here.
func sidebarItems(counts map[string]int, badges map[string]bool) []string {
	items := make([]string, len(sectionOrder))
	for i, s := range sectionOrder {
		label := fmt.Sprintf("%d  %s", i+1, sectionTitles[s])
		if n, ok := counts[s]; ok {
			label += fmt.Sprintf("  [%s]%d[-]", tagDim, n)
		}
		if badges[s] {
			label += "  [" + tagWarn + "]●[-]"
		}
		items[i] = label
	}
	return items
}

// statusHints returns the few most relevant keys for a section, always ending in
// "? help", for the right zone of the status bar.
func statusHints(section string) string {
	help := k("?") + " help"
	switch section {
	case pageCatalog:
		return k("↑↓") + " move  " + k("Enter") + " detail  " + k("i") + " install  " +
			k("/") + " filter  " + k("r") + " refresh  " + help
	case pageInstalled:
		return k("↑↓") + " move  " + k("Enter") + " run  " + k("u") + " update  " + k("d") + " uninstall  " +
			k("v") + " verify  " + k("Space") + " select  " + k("/") + " filter  " + help
	case pageNew:
		return k("↑↓") + " fields  " + k("Ctrl-S") + " scaffold  " + k("Esc") + " cancel  " + help
	case pageConfig:
		return k("↑↓") + " fields  " + k("Ctrl-S") + " save  " + k("Esc") + " cancel  " + help
	}
	return help
}

// helpText renders the full keybinding vocabulary for the ? overlay.
func helpText() string {
	rows := [][2]string{
		{"1–4", "jump to section"},
		{"↑ ↓ / j k", "move selection"},
		{"Enter", "open / confirm · run (Installed)"},
		{"Esc", "back / cancel"},
		{"Tab / Shift-Tab", "cycle focus"},
		{"Ctrl-B", "toggle sidebar"},
		{"/", "filter list"},
		{"i", "install (Catalog)"},
		{"u", "update (Installed)"},
		{"d", "uninstall (Installed)"},
		{"v", "verify (Installed)"},
		{"r", "refresh list"},
		{"Space", "toggle row select"},
		{"Ctrl-S", "save (forms)"},
		{"?", "this help"},
		{"q / Ctrl-C", "quit"},
	}
	var b strings.Builder
	b.WriteString("[" + tagAccent + "::b]microstore — keys[-]\n\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  ["+tagAccent+"::b]%-16s[-] %s\n", r[0], r[1])
	}
	b.WriteString("\n[" + tagDim + "]Esc or ? to close[-]")
	return b.String()
}

// k wraps a keybinding glyph in the accent+bold key style for the status bar.
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

// installedRow is one Installed-table row. Per the design standards, list views
// show the timestamp relative to now; the absolute time lives in the detail pane.
func installedRow(a models.InstalledApp, verify string, now time.Time) []string {
	return []string{a.Repo, a.Version, relTime(a.InstalledAt, now), verifyCell(verify)}
}

// installedDetailText renders the full record for an installed app in the detail
// pane: absolute timestamp, path, and verify state (field order matches the row).
func installedDetailText(a models.InstalledApp, verify string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "["+tagAccent+"::b]%s"+keyClose+"\n\n", orDash(a.Repo))
	fmt.Fprintf(&b, "["+tagAccent2+"::b]Version:"+keyClose+"   %s\n", orDash(a.Version))
	fmt.Fprintf(&b, "["+tagAccent2+"::b]Installed:"+keyClose+" %s\n", absTime(a.InstalledAt))
	fmt.Fprintf(&b, "["+tagAccent2+"::b]Asset:"+keyClose+"     %s\n", orDash(a.AssetName))
	fmt.Fprintf(&b, "["+tagAccent2+"::b]Path:"+keyClose+"      %s\n", orDash(a.Path))
	fmt.Fprintf(&b, "["+tagAccent2+"::b]Verify:"+keyClose+"    %s\n", verifyCell(verify))
	return b.String()
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
		return "[" + tagDim + "]" + emDash + " not checked[-]"
	}
}

// relTime renders a short relative age ("just now", "5m ago", "2h ago",
// "3d ago") for list cells. now is passed in so the helper stays pure/testable.
func relTime(t, now time.Time) string {
	if t.IsZero() {
		return emDash
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// absTime renders an absolute timestamp for the detail pane, or a dim em-dash.
func absTime(t time.Time) string {
	if t.IsZero() {
		return emDash
	}
	return t.Format("2006-01-02 15:04")
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

// filterInstalled applies the installed list's in-memory filter: a
// case-insensitive substring across the visible columns (repo, version). An
// empty query matches everything.
func filterInstalled(apps []models.InstalledApp, query string) []models.InstalledApp {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return apps
	}
	var out []models.InstalledApp
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.Repo), q) || strings.Contains(strings.ToLower(a.Version), q) {
			out = append(out, a)
		}
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
		return emDash
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
