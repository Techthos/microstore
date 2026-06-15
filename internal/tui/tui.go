package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
)

// Layout constants for the sidebar·body·status skeleton and the responsive
// thresholds (target a minimum of 80×24; auto-collapse the sidebar when narrow).
const (
	sidebarWidth  = 22
	minWidth      = 80
	minHeight     = 24
	collapseWidth = 100
)

// Service is the use-case surface the TUI consumes. *app.Service satisfies it.
type Service interface {
	ListCatalog(ctx context.Context) ([]models.ManifestEntry, error)
	AppDetails(ctx context.Context, repo string) (app.AppDetails, error)
	ListInstalled() ([]models.InstalledApp, error)
	Install(ctx context.Context, repo, version, asset string, allowUnverified bool) (models.InstalledApp, error)
	Update(ctx context.Context, repo string) (app.UpdateResult, error)
	Uninstall(repo string) error
	Verify(repo string) (install.VerifyStatus, error)
	RunInstalled(repo string) (string, error)
	ConfigureMCP(repo, dir string) (app.MCPConfigResult, error)
	ListTemplates(ctx context.Context) ([]models.Template, error)
	Scaffold(ctx context.Context, templateRepo, targetDir, ref string, force bool) (app.ScaffoldResult, error)
	GetConfig() (models.Config, error)
	SetConfig(models.Config) error
	SaveUIPrefs(lastSection string, sidebarCollapsed bool) error
	PathStatus() (app.PathStatus, error)
	AddToPath() (app.PathStatus, error)
}

// App owns the single tview.Application and the sidebar·body·status skeleton.
type App struct {
	app   *tview.Application
	pages *tview.Pages // body: one visible section page (+ transient overlays)
	svc   Service

	// chrome
	sidebar       *tview.List
	header        *tview.TextView
	statusContext *tview.TextView
	statusMessage *tview.TextView
	statusHints   *tview.TextView
	body          *tview.Flex // sidebar + content column; collapse resizes the sidebar
	inner         tview.Primitive
	root          tview.Primitive // responsive wrapper around inner

	section          string // active sidebar section
	sidebarCollapsed bool
	busy             int // in-flight user-initiated ops (gates quit confirm)
	cfg              models.Config

	// catalog (master-detail + filter + states)
	catalog       *tview.Table
	catalogDetail *tview.TextView
	catalogFilter *tview.InputField
	catalogCat    *tview.DropDown
	catalogPages  *tview.Pages
	catalogMsg    *tview.TextView
	allApps       []models.ManifestEntry
	catalogApps   []models.ManifestEntry
	catalogQuery  string
	detailRepo    string

	// installed (master-detail + multi-select + states)
	installed       *tview.Table
	installedDetail *tview.TextView
	installedFilter *tview.InputField
	installedPages  *tview.Pages
	installedMsg    *tview.TextView
	allInstalled    []models.InstalledApp
	installedApps   []models.InstalledApp
	installedQuery  string
	verifyState     map[string]string
	checked         map[string]bool

	// forms
	templates    []models.Template
	newForm      *tview.Form
	newErr       *tview.TextView
	templateDrop *tview.DropDown
	targetInput  *tview.InputField
	newDirty     bool

	configForm      *tview.Form
	configErr       *tview.TextView
	manifestInput   *tview.InputField
	installDirInput *tview.InputField
	configDirty     bool
}

// New builds the application and all screens. It does no I/O; call Run to start.
func New(svc Service) *App {
	a := &App{
		app:           tview.NewApplication(),
		pages:         tview.NewPages(),
		svc:           svc,
		sidebar:       tview.NewList(),
		header:        tview.NewTextView().SetDynamicColors(true),
		statusContext: tview.NewTextView().SetDynamicColors(true),
		statusMessage: tview.NewTextView().SetDynamicColors(true),
		statusHints:   tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
		section:       pageCatalog,
		verifyState:   map[string]string{},
		checked:       map[string]bool{},
	}

	a.buildSidebar()
	a.buildCatalog()
	a.buildInstalled()
	a.buildNew()
	a.buildConfig()

	a.pages.AddPage(pageCatalog, a.catalogPage(), true, true)
	a.pages.AddPage(pageInstalled, a.installedPage(), true, false)
	a.pages.AddPage(pageNew, a.newPage(), true, false)
	a.pages.AddPage(pageConfig, a.configPage(), true, false)

	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true)
	a.body = tview.NewFlex().
		AddItem(a.sidebar, sidebarWidth, 0, false).
		AddItem(content, 0, 1, true)

	statusBar := tview.NewFlex().
		AddItem(a.statusContext, 0, 2, false).
		AddItem(a.statusMessage, 0, 3, false).
		AddItem(a.statusHints, 0, 4, false)

	a.inner = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.body, 0, 1, true).
		AddItem(statusBar, 1, 0, false)
	a.root = newResponsive(a, a.inner)

	a.header.SetText(" " + screenTitle(pageCatalog))
	a.statusHints.SetText(statusHints(pageCatalog) + " ")
	a.refreshSidebar()
	a.updateContext()

	a.app.SetInputCapture(a.globalKeys)
	a.app.SetMouseCapture(a.mouseCapture)
	return a
}

// mouseCapture adds the pointer affordance for UC 13: a double-click on a row in
// the Installed table launches that app, mirroring the Enter keybinding. A single
// click only selects (tview's Table fires selection-changed, not selected, on a
// click), which already updates the detail pane — so double-click is the natural
// "activate" gesture. Other clicks pass through untouched.
func (a *App) mouseCapture(ev *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	if action == tview.MouseLeftDoubleClick && a.section == pageInstalled {
		x, y := ev.Position()
		if a.installed.InRect(x, y) {
			if ia, ok := a.installedAt(a.currentInstalledRow()); ok {
				a.runInstalled(ia.Repo)
				return nil, action
			}
		}
	}
	return ev, action
}

// Application exposes the underlying *tview.Application (for headless tests).
func (a *App) Application() *tview.Application { return a.app }

// Root exposes the root primitive (for headless tests).
func (a *App) Root() tview.Primitive { return a.root }

// Run starts the event loop after kicking off the initial data loads.
func (a *App) Run() error {
	go a.loadCatalog()
	go a.loadInstalled()
	go a.loadTemplates()
	go a.loadConfig()
	go a.checkPath()
	a.app.SetRoot(a.root, true).EnableMouse(true).EnablePaste(true)
	a.app.SetFocus(a.focusFor(a.section))
	return a.app.Run()
}

func (a *App) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	front, _ := a.pages.GetFrontPage()
	// Transient overlays own their keys (Esc/buttons handled locally).
	if front == pageAssetPick || front == pageConfirm || front == pageWarnPath || front == pageHelp {
		return ev
	}
	switch ev.Key() {
	case tcell.KeyCtrlC:
		a.requestQuit()
		return nil
	case tcell.KeyCtrlB:
		a.toggleSidebar()
		return nil
	case tcell.KeyTab:
		a.cycleFocus(+1)
		return nil
	case tcell.KeyBacktab:
		a.cycleFocus(-1)
		return nil
	case tcell.KeyRune:
		// While typing in an input/dropdown, runes belong to it, not navigation.
		if a.isTyping() {
			return ev
		}
		switch r := ev.Rune(); {
		case r == 'q':
			a.requestQuit()
			return nil
		case r == '?':
			a.showHelp()
			return nil
		case r >= '1' && r <= '9':
			if idx := int(r - '1'); idx < len(sectionOrder) {
				a.switchTo(sectionOrder[idx])
			}
			return nil
		}
	}
	return ev
}

// isTyping reports whether focus is on a text input or dropdown, where runes are
// content rather than global shortcuts.
func (a *App) isTyping() bool {
	switch a.app.GetFocus().(type) {
	case *tview.InputField, *tview.DropDown:
		return true
	}
	return false
}

// switchTo activates a sidebar section: swaps the body page, updates the header,
// hints and sidebar highlight, focuses the section's primary widget, and persists
// the choice as a view preference.
func (a *App) switchTo(section string) {
	if !isSection(section) {
		return
	}
	a.section = section
	a.pages.SwitchToPage(section)
	a.header.SetText(" " + screenTitle(section))
	a.statusHints.SetText(statusHints(section) + " ")
	if idx := sectionIndex(section); idx >= 0 {
		a.sidebar.SetCurrentItem(idx)
	}
	a.app.SetFocus(a.focusFor(section))
	a.updateContext()
	a.persistUIPrefs()
}

func (a *App) focusFor(section string) tview.Primitive {
	switch section {
	case pageCatalog:
		return a.catalog
	case pageInstalled:
		return a.installed
	case pageNew:
		return a.newForm
	case pageConfig:
		return a.configForm
	}
	return a.sidebar
}

// cycleFocus moves focus across the section's focusable regions (sidebar ↔ table
// ↔ detail / form), wrapping around.
func (a *App) cycleFocus(delta int) {
	regions := a.focusRegions()
	idx := 0
	cur := a.app.GetFocus()
	for i, p := range regions {
		if p == cur {
			idx = i
			break
		}
	}
	n := (idx + delta + len(regions)) % len(regions)
	a.app.SetFocus(regions[n])
}

func (a *App) focusRegions() []tview.Primitive {
	switch a.section {
	case pageCatalog:
		return []tview.Primitive{a.sidebar, a.catalog, a.catalogDetail}
	case pageInstalled:
		return []tview.Primitive{a.sidebar, a.installed, a.installedDetail}
	case pageNew:
		return []tview.Primitive{a.sidebar, a.newForm}
	case pageConfig:
		return []tview.Primitive{a.sidebar, a.configForm}
	}
	return []tview.Primitive{a.sidebar}
}

// toggleSidebar collapses/expands the sidebar and persists the preference. The
// width is applied during layout (see responsive.Draw).
func (a *App) toggleSidebar() {
	a.sidebarCollapsed = !a.sidebarCollapsed
	if a.sidebarCollapsed && a.app.GetFocus() == a.sidebar {
		a.app.SetFocus(a.focusFor(a.section))
	}
	a.persistUIPrefs()
}

// persistUIPrefs writes the lightweight view preferences off the event loop.
func (a *App) persistUIPrefs() {
	section, collapsed := a.section, a.sidebarCollapsed
	go func() { _ = a.svc.SaveUIPrefs(section, collapsed) }()
}

// requestQuit exits immediately unless a form is dirty or an operation is in
// flight, in which case it asks for confirmation first.
func (a *App) requestQuit() {
	if a.newDirty || a.configDirty || a.busy > 0 {
		a.confirmQuit()
		return
	}
	a.app.Stop()
}

func (a *App) confirmQuit() {
	modal := tview.NewModal().
		SetText("Quit microstore?\n\nThere are unsaved changes or work in progress.").
		AddButtons([]string{"Cancel", "Quit"}).
		SetDoneFunc(func(_ int, label string) {
			a.pages.RemovePage(pageConfirm)
			a.app.SetFocus(a.focusFor(a.section))
			if label == "Quit" {
				a.app.Stop()
			}
		})
	a.pages.AddPage(pageConfirm, modal, true, true)
	a.app.SetFocus(modal)
}

// setStatus writes the transient message/result into the status bar's middle zone.
func (a *App) setStatus(format string, args ...any) {
	a.statusMessage.SetText(" " + fmt.Sprintf(format, args...))
}

// --- sidebar ---

func (a *App) buildSidebar() {
	a.sidebar.ShowSecondaryText(false).
		SetHighlightFullLine(true).
		SetSelectedStyle(sidebarSelectedStyle())
	a.sidebar.SetMainTextColor(tcell.GetColor(hexText))
	a.sidebar.SetBorder(true).SetTitle(" microstore ")
	a.sidebar.SetSelectedFunc(func(i int, _ string, _ string, _ rune) {
		if i >= 0 && i < len(sectionOrder) {
			a.switchTo(sectionOrder[i])
		}
	})
}

// refreshSidebar rebuilds the sidebar labels from current counts/badges and
// keeps the active section highlighted.
func (a *App) refreshSidebar() {
	counts := map[string]int{pageCatalog: len(a.allApps), pageInstalled: len(a.allInstalled)}
	badges := map[string]bool{pageInstalled: a.installedNeedsAttention()}
	a.sidebar.Clear()
	for _, it := range sidebarItems(counts, badges) {
		a.sidebar.AddItem(it, "", 0, nil)
	}
	if idx := sectionIndex(a.section); idx >= 0 {
		a.sidebar.SetCurrentItem(idx)
	}
}

func (a *App) installedNeedsAttention() bool {
	for _, st := range a.verifyState {
		if st == "mismatch" || st == "missing" {
			return true
		}
	}
	return false
}

// updateContext refreshes the status bar's left (context) zone for the section.
func (a *App) updateContext() {
	switch a.section {
	case pageCatalog:
		a.setContext(pageCatalog, a.catalog, len(a.catalogApps), a.catalogQuery != "")
	case pageInstalled:
		a.setContext(pageInstalled, a.installed, len(a.installedApps), a.installedQuery != "")
	default:
		a.statusContext.SetText(" " + screenTitle(a.section))
	}
}

func (a *App) setContext(section string, table *tview.Table, total int, filtered bool) {
	pos := 0
	if total > 0 {
		row, _ := table.GetSelection()
		pos = row // header is row 0, so row == 1-based data position
		if pos < 1 {
			pos = 1
		}
		if pos > total {
			pos = total
		}
	}
	s := fmt.Sprintf(" %s · %d of %d", screenTitle(section), pos, total)
	if filtered {
		s += " (filtered)"
	}
	a.statusContext.SetText(s)
}

// --- Catalog screen (master-detail) ---

func (a *App) buildCatalog() {
	a.catalog = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	a.catalog.SetSelectedStyle(selectedStyle())
	// Moving the selection updates the detail pane and the context zone live.
	a.catalog.SetSelectionChangedFunc(func(row, _ int) {
		a.showCatalogDetail(row)
		a.updateContext()
	})
	// Enter opens (focuses) the detail pane for scrolling.
	a.catalog.SetSelectedFunc(func(int, int) { a.app.SetFocus(a.catalogDetail) })
	a.catalog.SetInputCapture(a.catalogKeys)

	a.catalogDetail = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	a.catalogDetail.SetBorder(true).SetTitle(" Details ")
	a.catalogDetail.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyEscape:
			a.app.SetFocus(a.catalog)
			return nil
		case ev.Key() == tcell.KeyRune && ev.Rune() == 'i' && a.detailRepo != "":
			a.doInstall(a.detailRepo, "", "", false)
			return nil
		}
		return ev
	})

	a.catalogFilter = tview.NewInputField().SetLabel("/ ")
	a.catalogFilter.SetChangedFunc(func(string) { a.applyCatalogFilter() })
	a.catalogFilter.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			a.catalogFilter.SetText("")
			a.applyCatalogFilter()
		}
		a.app.SetFocus(a.catalog)
	})

	a.catalogCat = tview.NewDropDown().SetLabel("Category: ")
	a.catalogCat.SetOptions([]string{"(all)"}, nil)
	a.catalogCat.SetCurrentOption(0)
	a.catalogCat.SetSelectedFunc(func(string, int) { a.applyCatalogFilter() })
}

// catalogKeys implements the list keybinding vocabulary for the catalog table.
func (a *App) catalogKeys(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() != tcell.KeyRune {
		return ev
	}
	switch ev.Rune() {
	case '/':
		a.app.SetFocus(a.catalogFilter)
		return nil
	case 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	case 'i':
		row, _ := a.catalog.GetSelection()
		if e, ok := a.catalogAt(row); ok {
			a.doInstall(e.Repo, "", "", false)
		}
		return nil
	case 'r':
		a.setStatus("refreshing catalog…")
		go a.loadCatalog()
		return nil
	}
	return ev
}

func (a *App) catalogAt(row int) (models.ManifestEntry, bool) {
	if row >= 1 && row-1 < len(a.catalogApps) {
		return a.catalogApps[row-1], true
	}
	return models.ManifestEntry{}, false
}

func (a *App) catalogPage() tview.Primitive {
	controls := tview.NewFlex().
		AddItem(a.catalogFilter, 0, 2, false).
		AddItem(a.catalogCat, 0, 1, false)
	split := tview.NewFlex().
		AddItem(a.catalog, 0, 3, true).
		AddItem(a.catalogDetail, 0, 2, false)
	a.catalogMsg = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
	a.catalogMsg.SetText("[" + tagDim + "]Loading…[-]")
	a.catalogPages = tview.NewPages()
	a.catalogPages.AddPage("data", split, true, true)
	a.catalogPages.AddPage("msg", centered(a.catalogMsg), true, false)
	a.catalogPages.SwitchToPage("msg")

	wrap := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(controls, 1, 0, false).
		AddItem(a.catalogPages, 0, 1, true)
	wrap.SetBorder(true).SetTitle(" Catalog ")
	return wrap
}

func (a *App) loadCatalog() {
	apps, err := a.svc.ListCatalog(context.Background())
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.catalogMsg.SetText(errState("catalog: " + err.Error()))
			a.catalogPages.SwitchToPage("msg")
			a.setStatus("[red]catalog: %s", err.Error())
			return
		}
		a.allApps = apps
		cats := append([]string{"(all)"}, distinctCategories(apps)...)
		a.catalogCat.SetOptions(cats, nil)
		a.catalogCat.SetCurrentOption(0)
		a.applyCatalogFilter()
		a.setStatus("catalog: %d app(s)", len(apps))
		a.refreshSidebar()
	})
}

// applyCatalogFilter runs on the event loop and filters the already-fetched
// catalog in memory (no network), switching between the data and message states.
func (a *App) applyCatalogFilter() {
	category := ""
	if idx, opt := a.catalogCat.GetCurrentOption(); idx > 0 {
		category = opt
	}
	a.catalogQuery = a.catalogFilter.GetText()
	a.catalogApps = filterApps(a.allApps, a.catalogQuery, category)
	a.renderCatalog()
	switch {
	case len(a.catalogApps) > 0:
		a.catalogPages.SwitchToPage("data")
	case a.catalogQuery != "" || category != "":
		a.catalogMsg.SetText("[" + tagDim + "]No matches.[-]")
		a.catalogPages.SwitchToPage("msg")
	default:
		a.catalogMsg.SetText("[" + tagDim + "]No apps in catalog.[-]")
		a.catalogPages.SwitchToPage("msg")
	}
	a.updateContext()
}

func (a *App) renderCatalog() {
	renderTable(a.catalog, catalogHeader, len(a.catalogApps), nil, func(i int) []string {
		return catalogRow(a.catalogApps[i])
	})
}

// showCatalogDetail loads and renders the selected app's details into the pane.
func (a *App) showCatalogDetail(row int) {
	e, ok := a.catalogAt(row)
	if !ok {
		a.detailRepo = ""
		a.catalogDetail.SetText("[" + tagDim + "]Select an app to see details.[-]")
		return
	}
	a.detailRepo = e.Repo
	a.catalogDetail.SetText("[" + tagDim + "]loading " + e.Repo + "…[-]")
	repo := e.Repo
	go func() {
		d, err := a.svc.AppDetails(context.Background(), repo)
		a.app.QueueUpdateDraw(func() {
			if a.detailRepo != repo {
				return // selection moved on while we fetched
			}
			if err != nil {
				a.catalogDetail.SetText("[red]" + err.Error())
				return
			}
			a.catalogDetail.SetText(detailText(d))
		})
	}()
}

// --- install (from the Catalog detail pane) ---

func (a *App) doInstall(repo, version, asset string, allowUnverified bool) {
	a.busy++
	a.setStatus("installing %s…", repo)
	go func() {
		rec, err := a.svc.Install(context.Background(), repo, version, asset, allowUnverified)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			var sel *app.AssetSelectionError
			if errors.As(err, &sel) {
				a.showAssetPick(repo, sel.Assets)
				return
			}
			if err != nil {
				a.setStatus("[red]install: %s", err.Error())
				return
			}
			a.setStatus("[green]✓ installed %s %s", rec.Repo, rec.Version)
			go a.loadInstalled()
		})
	}()
}

func (a *App) showAssetPick(repo string, assets []models.Asset) {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Select asset for " + repo + " ([Esc] cancel) ")
	for _, as := range assets {
		name := as.Name
		list.AddItem(name, "", 0, func() {
			a.pages.RemovePage(pageAssetPick)
			a.doInstall(repo, "", name, false)
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape {
			a.pages.RemovePage(pageAssetPick)
			a.app.SetFocus(a.focusFor(a.section))
			return nil
		}
		return ev
	})
	a.pages.AddPage(pageAssetPick, modalWrap(list, 70, 18), true, true)
	a.app.SetFocus(list)
}

// --- Installed screen (master-detail + multi-select) ---

func (a *App) buildInstalled() {
	a.installed = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	a.installed.SetSelectedStyle(selectedStyle())
	a.installed.SetSelectionChangedFunc(func(row, _ int) {
		a.showInstalledDetail(row)
		a.updateContext()
	})
	// Enter runs the highlighted installed app (UC 13): for an installed item the
	// natural "open" is to launch it. The detail pane auto-shows on selection and
	// stays reachable via Tab, so Enter is free to hand over the terminal.
	a.installed.SetSelectedFunc(func(row, _ int) {
		if ia, ok := a.installedAt(row); ok {
			a.runInstalled(ia.Repo)
		}
	})
	a.installed.SetInputCapture(a.installedKeys)

	a.installedDetail = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	a.installedDetail.SetBorder(true).SetTitle(" Details ")
	a.installedDetail.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape {
			a.app.SetFocus(a.installed)
			return nil
		}
		return ev
	})

	a.installedFilter = tview.NewInputField().SetLabel("/ ")
	a.installedFilter.SetChangedFunc(func(string) { a.applyInstalledFilter() })
	a.installedFilter.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			a.installedFilter.SetText("")
			a.applyInstalledFilter()
		}
		a.app.SetFocus(a.installed)
	})
}

// installedKeys implements the list keybinding vocabulary for the installed table.
func (a *App) installedKeys(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() != tcell.KeyRune {
		return ev
	}
	switch ev.Rune() {
	case '/':
		a.app.SetFocus(a.installedFilter)
		return nil
	case 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	case ' ':
		a.toggleChecked()
		return nil
	case 'u':
		a.actionInstalled("update")
		return nil
	case 'd':
		a.actionInstalled("uninstall")
		return nil
	case 'v':
		a.actionInstalled("verify")
		return nil
	case 'm':
		a.actionInstalled("mcp")
		return nil
	case 'r':
		a.setStatus("refreshing…")
		go a.loadInstalled()
		return nil
	}
	return ev
}

func (a *App) installedAt(row int) (models.InstalledApp, bool) {
	if row >= 1 && row-1 < len(a.installedApps) {
		return a.installedApps[row-1], true
	}
	return models.InstalledApp{}, false
}

// checkedRepos returns the multi-selected repos in stable (list) order.
func (a *App) checkedRepos() []string {
	var out []string
	for _, ia := range a.allInstalled {
		if a.checked[ia.Repo] {
			out = append(out, ia.Repo)
		}
	}
	return out
}

func (a *App) clearChecked() {
	a.checked = map[string]bool{}
	a.renderInstalled()
}

func (a *App) toggleChecked() {
	row, _ := a.installed.GetSelection()
	if ia, ok := a.installedAt(row); ok {
		if a.checked[ia.Repo] {
			delete(a.checked, ia.Repo)
		} else {
			a.checked[ia.Repo] = true
		}
		a.renderInstalled()
	}
}

// actionInstalled applies an action to the checked rows, or the highlighted row
// when nothing is checked. Destructive actions confirm with a count first.
func (a *App) actionInstalled(action string) {
	targets := a.checkedRepos()
	if len(targets) == 0 {
		row, _ := a.installed.GetSelection()
		if ia, ok := a.installedAt(row); ok {
			targets = []string{ia.Repo}
		}
	}
	if len(targets) == 0 {
		return
	}
	switch action {
	case "uninstall":
		a.confirmBatch("Uninstall", targets, func() {
			for _, r := range targets {
				a.doUninstall(r)
			}
		})
	case "update":
		for _, r := range targets {
			a.doUpdate(r)
		}
		a.clearChecked()
	case "verify":
		for _, r := range targets {
			a.doVerify(r)
		}
		a.clearChecked()
	case "mcp":
		for _, r := range targets {
			a.doConfigureMCP(r)
		}
		a.clearChecked()
	}
}

// confirmBatch shows a centered confirm naming the exact target or the count,
// defaulting focus to the safe (Cancel) choice; do runs only on confirm.
func (a *App) confirmBatch(verb string, targets []string, do func()) {
	text := fmt.Sprintf("%s %d items?", verb, len(targets))
	if len(targets) == 1 {
		text = fmt.Sprintf("%s %s?", verb, targets[0])
	}
	text += "\n\nThis cannot be undone."
	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"Cancel", verb}).
		SetDoneFunc(func(_ int, label string) {
			a.pages.RemovePage(pageConfirm)
			a.app.SetFocus(a.installed)
			if label == verb {
				do()
				a.clearChecked()
			}
		})
	a.pages.AddPage(pageConfirm, modal, true, true)
	a.app.SetFocus(modal)
}

func (a *App) installedPage() tview.Primitive {
	split := tview.NewFlex().
		AddItem(a.installed, 0, 3, true).
		AddItem(a.installedDetail, 0, 2, false)
	a.installedMsg = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
	a.installedMsg.SetText("[" + tagDim + "]Loading…[-]")
	a.installedPages = tview.NewPages()
	a.installedPages.AddPage("data", split, true, true)
	a.installedPages.AddPage("msg", centered(a.installedMsg), true, false)
	a.installedPages.SwitchToPage("msg")

	wrap := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.installedFilter, 1, 0, false).
		AddItem(a.installedPages, 0, 1, true)
	wrap.SetBorder(true).SetTitle(" Installed ")
	return wrap
}

func (a *App) loadInstalled() {
	list, err := a.svc.ListInstalled()
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.installedMsg.SetText(errState("installed: " + err.Error()))
			a.installedPages.SwitchToPage("msg")
			a.setStatus("[red]installed: %s", err.Error())
			return
		}
		a.allInstalled = list
		a.applyInstalledFilter()
		a.refreshSidebar()
	})
}

// applyInstalledFilter filters the installed list in memory and switches between
// the data and message (empty) states.
func (a *App) applyInstalledFilter() {
	a.installedQuery = a.installedFilter.GetText()
	a.installedApps = filterInstalled(a.allInstalled, a.installedQuery)
	a.renderInstalled()
	switch {
	case len(a.installedApps) > 0:
		a.installedPages.SwitchToPage("data")
	case a.installedQuery != "":
		a.installedMsg.SetText("[" + tagDim + "]No matches.[-]")
		a.installedPages.SwitchToPage("msg")
	default:
		a.installedMsg.SetText("[" + tagDim + "]No installs yet — install one from the Catalog (press i).[-]")
		a.installedPages.SwitchToPage("msg")
	}
	a.updateContext()
}

func (a *App) renderInstalled() {
	now := time.Now()
	renderTable(a.installed, installedHeader, len(a.installedApps),
		func(i int) bool { return a.checked[a.installedApps[i].Repo] },
		func(i int) []string {
			ia := a.installedApps[i]
			return installedRow(ia, a.verifyState[ia.Repo], now)
		})
}

func (a *App) showInstalledDetail(row int) {
	ia, ok := a.installedAt(row)
	if !ok {
		a.installedDetail.SetText("[" + tagDim + "]No install selected.[-]")
		return
	}
	a.installedDetail.SetText(installedDetailText(ia, a.verifyState[ia.Repo]))
}

func (a *App) doUpdate(repo string) {
	a.busy++
	a.setStatus("updating %s…", repo)
	go func() {
		res, err := a.svc.Update(context.Background(), repo)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			var sel *app.AssetSelectionError
			if errors.As(err, &sel) {
				a.showAssetPick(repo, sel.Assets)
				return
			}
			if err != nil {
				a.setStatus("[red]update: %s", err.Error())
				return
			}
			if res.Updated {
				a.setStatus("[green]✓ updated %s %s → %s", repo, res.From, res.To)
			} else {
				a.setStatus("%s already current (%s)", repo, res.To)
			}
			go a.loadInstalled()
		})
	}()
}

func (a *App) doUninstall(repo string) {
	a.busy++
	a.setStatus("uninstalling %s…", repo)
	go func() {
		err := a.svc.Uninstall(repo)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			if err != nil {
				a.setStatus("[red]uninstall: %s", err.Error())
				return
			}
			delete(a.verifyState, repo)
			a.setStatus("[green]✓ uninstalled %s", repo)
			go a.loadInstalled()
		})
	}()
}

// doConfigureMCP wires the install into the .mcp.json of the directory microstore
// was launched from (the use-case resolves "" to the working directory). A no-MCP
// app is a benign outcome surfaced as a warning, not a red error.
func (a *App) doConfigureMCP(repo string) {
	a.busy++
	a.setStatus("configuring MCP for %s…", repo)
	go func() {
		res, err := a.svc.ConfigureMCP(repo, "")
		a.app.QueueUpdateDraw(func() {
			a.busy--
			switch {
			case errors.Is(err, app.ErrNoMCPSupport):
				a.setStatus("[yellow]%s advertises no MCP server", repo)
			case err != nil:
				a.setStatus("[red]mcp: %s", err.Error())
			case res.Created:
				a.setStatus("[green]✓ created %s with %s", res.Path, res.Server)
			case res.Updated:
				a.setStatus("[green]✓ updated %s in %s", res.Server, res.Path)
			default:
				a.setStatus("[green]✓ added %s to %s", res.Server, res.Path)
			}
		})
	}()
}

func (a *App) doVerify(repo string) {
	a.busy++
	a.setStatus("verifying %s…", repo)
	go func() {
		st, err := a.svc.Verify(repo)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			if err != nil {
				a.setStatus("[red]verify: %s", err.Error())
				return
			}
			a.verifyState[repo] = string(st)
			a.renderInstalled()
			a.showInstalledDetail(a.currentInstalledRow())
			a.refreshSidebar()
			a.setStatus("%s: %s", repo, string(st))
		})
	}()
}

func (a *App) currentInstalledRow() int {
	row, _ := a.installed.GetSelection()
	return row
}

// runInstalled hands the terminal to an installed micro-app's binary via
// app.Suspend — the same handoff used for /product-idea — then restores the TUI
// when the child exits. Resolving (and validating) the path stays in the Service;
// spawning the process is a view concern, so it lives here. The Suspend call runs
// in a goroutine (off the event loop) so resolving the path never blocks the loop.
func (a *App) runInstalled(repo string) {
	a.setStatus("launching %s…", repo)
	go func() {
		path, err := a.svc.RunInstalled(repo)
		if err != nil {
			a.app.QueueUpdateDraw(func() { a.setStatus("[red]run: %s", err.Error()) })
			return
		}
		a.app.Suspend(func() {
			cmd := exec.Command(path)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			_ = cmd.Run()
		})
		a.app.QueueUpdateDraw(func() { a.setStatus("%s exited — back in microstore", repo) })
	}()
}

// --- New App screen ---

func (a *App) buildNew() {
	a.templateDrop = tview.NewDropDown().SetLabel("Template: ").SetOptions([]string{"(loading…)"}, nil)
	a.templateDrop.SetSelectedFunc(func(string, int) { a.newDirty = true; a.validateNew() })
	a.targetInput = tview.NewInputField().SetLabel("Target dir: ")
	a.targetInput.SetChangedFunc(func(string) { a.newDirty = true; a.validateNew() })
	a.newForm = tview.NewForm().
		AddFormItem(a.templateDrop).
		AddFormItem(a.targetInput).
		AddButton("Scaffold (Ctrl-S)", a.doScaffold)
	a.newForm.SetBorder(true).SetTitle(" New App (scaffold → /product-idea) ")
	a.newForm.SetInputCapture(a.formKeys(a.doScaffold, func() bool { return a.newDirty }, a.resetNew))
	a.newErr = tview.NewTextView().SetDynamicColors(true)
}

func (a *App) newPage() tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.newForm, 0, 1, true).
		AddItem(a.newErr, 1, 0, false)
}

// validateNew checks the form live, shows the first error inline, and reports
// validity so the save path can block while invalid.
func (a *App) validateNew() bool {
	if strings.TrimSpace(a.targetInput.GetText()) == "" {
		a.newErr.SetText("[red]target dir is required[-]")
		return false
	}
	if idx, _ := a.templateDrop.GetCurrentOption(); idx < 0 || idx >= len(a.templates) {
		a.newErr.SetText("[red]select a template[-]")
		return false
	}
	a.newErr.SetText("")
	return true
}

func (a *App) resetNew() {
	a.targetInput.SetText("")
	a.newDirty = false
	a.validateNew()
}

func (a *App) loadTemplates() {
	tmpls, err := a.svc.ListTemplates(context.Background())
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]templates: %s", err.Error())
			return
		}
		a.templates = tmpls
		names := make([]string, len(tmpls))
		for i, t := range tmpls {
			names[i] = templateLabel(t)
		}
		if len(names) == 0 {
			names = []string{"(no templates)"}
		}
		a.templateDrop.SetOptions(names, nil)
		a.templateDrop.SetCurrentOption(0)
		a.newDirty = false
	})
}

func (a *App) doScaffold() {
	if !a.validateNew() {
		a.app.SetFocus(a.targetInput)
		return
	}
	idx, _ := a.templateDrop.GetCurrentOption()
	tmpl := a.templates[idx]
	target := strings.TrimSpace(a.targetInput.GetText())
	a.busy++
	a.setStatus("scaffolding %s…", tmpl.Repo)
	go func() {
		res, err := a.svc.Scaffold(context.Background(), tmpl.Repo, target, tmpl.Ref, false)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			if err != nil {
				a.setStatus("[red]scaffold: %s", err.Error())
				return
			}
			a.newDirty = false
			a.setStatus("[green]✓ scaffolded %d file(s) into %s", res.Files, res.TargetDir)
		})
		if err == nil {
			a.launchProductIdea(res.TargetDir)
		}
	}()
}

// formKeys returns an input-capture for a form: Ctrl-S saves; Esc cancels,
// prompting to discard when the form is dirty.
func (a *App) formKeys(save func(), dirty func() bool, reset func()) func(*tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlS:
			save()
			return nil
		case tcell.KeyEscape:
			if dirty() {
				a.confirmDiscard(reset)
			}
			return nil
		}
		return ev
	}
}

func (a *App) confirmDiscard(reset func()) {
	modal := tview.NewModal().
		SetText("Discard changes?").
		AddButtons([]string{"Keep editing", "Discard"}).
		SetDoneFunc(func(_ int, label string) {
			a.pages.RemovePage(pageConfirm)
			a.app.SetFocus(a.focusFor(a.section))
			if label == "Discard" {
				reset()
			}
		})
	a.pages.AddPage(pageConfirm, modal, true, true)
	a.app.SetFocus(modal)
}

// launchProductIdea hands off to the claude CLI in dir, or reports the exact
// command when claude is not on PATH.
func (a *App) launchProductIdea(dir string) {
	path, err := exec.LookPath("claude")
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			a.setStatus("scaffolded into %s — run `claude` there, then /product-idea (claude not on PATH)", dir)
		})
		return
	}
	a.app.Suspend(func() {
		cmd := exec.Command(path)
		cmd.Dir = dir
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		_ = cmd.Run()
	})
}

// --- PATH check (launch) ---

// checkPath runs once at launch: if the managed InstallDir is not on $PATH,
// installed binaries won't be runnable from a shell, so it raises a modal
// offering to append the export line to the user's shell profile.
func (a *App) checkPath() {
	st, err := a.svc.PathStatus()
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]path check: %s", err.Error())
			return
		}
		if st.OnPath || st.InstallDir == "" {
			return
		}
		a.showPathWarning(st)
	})
}

// showPathWarning displays the launch-time PATH advisory. "Add" appends the
// export line to the resolved shell profile; "Dismiss" closes the overlay.
func (a *App) showPathWarning(st app.PathStatus) {
	addLabel := "Add to " + filepath.Base(st.ProfilePath)
	modal := tview.NewModal().
		SetText(pathWarningText(st)).
		AddButtons([]string{addLabel, "Dismiss"}).
		SetDoneFunc(func(_ int, label string) {
			a.pages.RemovePage(pageWarnPath)
			a.app.SetFocus(a.focusFor(a.section))
			if label == addLabel {
				a.doAddToPath()
			}
		})
	a.pages.AddPage(pageWarnPath, modal, true, true)
	a.app.SetFocus(modal)
}

func (a *App) doAddToPath() {
	a.setStatus("updating PATH…")
	go func() {
		st, err := a.svc.AddToPath()
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.setStatus("[red]path: %s", err.Error())
				return
			}
			a.setStatus("added install dir to %s — restart your shell to apply", st.ProfilePath)
		})
	}()
}

// --- Config screen ---

func (a *App) buildConfig() {
	a.manifestInput = tview.NewInputField().SetLabel("Manifest URL: ").SetFieldWidth(60)
	a.manifestInput.SetChangedFunc(func(string) { a.configDirty = true; a.validateConfig() })
	a.installDirInput = tview.NewInputField().SetLabel("Install dir:  ").SetFieldWidth(60)
	a.installDirInput.SetChangedFunc(func(string) { a.configDirty = true })
	a.configForm = tview.NewForm().
		AddFormItem(a.manifestInput).
		AddFormItem(a.installDirInput).
		AddButton("Save (Ctrl-S)", a.doSaveConfig)
	a.configForm.SetBorder(true).SetTitle(" Config (manifest URL required for catalog actions) ")
	a.configForm.SetInputCapture(a.formKeys(a.doSaveConfig, func() bool { return a.configDirty }, a.reloadConfig))
	a.configErr = tview.NewTextView().SetDynamicColors(true)
}

func (a *App) configPage() tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.configForm, 0, 1, true).
		AddItem(a.configErr, 1, 0, false)
}

// validateConfig flags a malformed (non-empty, non-http) manifest URL inline.
func (a *App) validateConfig() bool {
	u := strings.TrimSpace(a.manifestInput.GetText())
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		a.configErr.SetText("[red]manifest URL must start with http:// or https://[-]")
		return false
	}
	a.configErr.SetText("")
	return true
}

func (a *App) loadConfig() {
	cfg, err := a.svc.GetConfig()
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]config: %s", err.Error())
			return
		}
		a.cfg = cfg
		a.manifestInput.SetText(cfg.ManifestURL)
		a.installDirInput.SetText(cfg.InstallDir)
		a.configDirty = false
		a.restoreUIPrefs(cfg)
	})
}

func (a *App) reloadConfig() {
	a.manifestInput.SetText(a.cfg.ManifestURL)
	a.installDirInput.SetText(a.cfg.InstallDir)
	a.configDirty = false
	a.validateConfig()
}

// restoreUIPrefs reopens the app on the last active section with the sidebar in
// its last collapsed/expanded state.
func (a *App) restoreUIPrefs(cfg models.Config) {
	a.sidebarCollapsed = cfg.SidebarCollapsed
	if isSection(cfg.LastSection) && cfg.LastSection != a.section {
		a.switchTo(cfg.LastSection)
	}
}

func (a *App) doSaveConfig() {
	if !a.validateConfig() {
		a.app.SetFocus(a.manifestInput)
		return
	}
	// Start from the retained config so the TUI view-pref fields survive the save.
	cfg := a.cfg
	cfg.ManifestURL = strings.TrimSpace(a.manifestInput.GetText())
	cfg.InstallDir = strings.TrimSpace(a.installDirInput.GetText())
	a.busy++
	a.setStatus("saving config…")
	go func() {
		err := a.svc.SetConfig(cfg)
		a.app.QueueUpdateDraw(func() {
			a.busy--
			if err != nil {
				a.setStatus("[red]config: %s", err.Error())
				return
			}
			a.cfg = cfg
			a.configDirty = false
			a.setStatus("[green]✓ config saved")
			go a.loadCatalog()
		})
	}()
}

func templateLabel(t models.Template) string {
	if t.Name != "" {
		return t.Name + " (" + t.Repo + ")"
	}
	return t.Repo
}

// --- help overlay ---

func (a *App) showHelp() {
	tv := tview.NewTextView().SetDynamicColors(true).SetText(helpText())
	tv.SetBorder(true).SetTitle(" Help ")
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape || (ev.Key() == tcell.KeyRune && ev.Rune() == '?') {
			a.hideHelp()
			return nil
		}
		return ev
	})
	a.pages.AddPage(pageHelp, modalWrap(tv, 56, 22), true, true)
	a.app.SetFocus(tv)
}

func (a *App) hideHelp() {
	a.pages.RemovePage(pageHelp)
	a.app.SetFocus(a.focusFor(a.section))
}

// --- shared helpers ---

// renderTable rebuilds a table from a header and rowCount rows: a frozen bold
// header, header row excluded from selection, and the prior selection preserved
// across rebuilds. When marks is non-nil, a leading check column reflects it.
func renderTable(table *tview.Table, header []string, rowCount int, marks func(i int) bool, row func(i int) []string) {
	prevRow, _ := table.GetSelection()
	table.Clear()

	col0 := 0
	if marks != nil {
		table.SetCell(0, 0, headerCell(""))
		col0 = 1
	}
	for c, h := range header {
		table.SetCell(0, c+col0, headerCell(h))
	}
	for i := 0; i < rowCount; i++ {
		if marks != nil {
			mark := " "
			if marks(i) {
				mark = "[" + tagGood + "]✓[-]"
			}
			table.SetCell(i+1, 0, tview.NewTableCell(mark))
		}
		for c, v := range row(i) {
			table.SetCell(i+1, c+col0, tview.NewTableCell(v).SetExpansion(1))
		}
	}
	if rowCount > 0 {
		if prevRow < 1 || prevRow > rowCount {
			prevRow = 1
		}
		table.Select(prevRow, 0)
	}
}

func headerCell(h string) *tview.TableCell {
	return tview.NewTableCell(strings.ToUpper(h)).
		SetTextColor(tcell.GetColor(hexAccent)).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false).
		SetExpansion(1)
}

// errState renders the mandatory error message (with a retry hint) for a data
// view that failed to load.
func errState(msg string) string {
	return "[red]" + msg + "[-]\n\n[" + tagDim + "]press r to retry[-]"
}

// centered vertically centers a one-line primitive for the loading/empty/error
// states.
func centered(p tview.Primitive) tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(p, 1, 0, false).
		AddItem(nil, 0, 1, false)
}

func modalWrap(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 1, true).
			AddItem(nil, 0, 1, false), width, 1, true).
		AddItem(nil, 0, 1, false)
}

// --- responsive wrapper ---

// responsive wraps the inner layout and enforces the 80×24 minimum (showing a
// centered notice below it) and auto-collapses the sidebar on narrow widths.
type responsive struct {
	*tview.Box
	a        *App
	inner    tview.Primitive
	tooSmall *tview.TextView
}

func newResponsive(a *App, inner tview.Primitive) *responsive {
	tv := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
	tv.SetText(fmt.Sprintf("[%s]Terminal too small — need %d×%d[-]", tagWarn, minWidth, minHeight))
	return &responsive{Box: tview.NewBox(), a: a, inner: inner, tooSmall: tv}
}

func (r *responsive) Draw(screen tcell.Screen) {
	r.DrawForSubclass(screen, r)
	x, y, w, h := r.GetInnerRect()
	if w < minWidth || h < minHeight {
		r.tooSmall.SetRect(x, y+h/2, w, 1)
		r.tooSmall.Draw(screen)
		return
	}
	// Effective sidebar width: collapsed by user preference or by a narrow window.
	width := sidebarWidth
	if r.a.sidebarCollapsed || w < collapseWidth {
		width = 0
	}
	r.a.body.ResizeItem(r.a.sidebar, width, 0)
	r.inner.SetRect(x, y, w, h)
	r.inner.Draw(screen)
}

func (r *responsive) Focus(delegate func(tview.Primitive)) { delegate(r.inner) }
func (r *responsive) HasFocus() bool                       { return r.inner.HasFocus() }

func (r *responsive) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return r.inner.InputHandler()
}

func (r *responsive) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return r.inner.MouseHandler()
}
