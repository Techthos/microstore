package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
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
	ListTemplates(ctx context.Context) ([]models.Template, error)
	Scaffold(ctx context.Context, templateRepo, targetDir, ref string, force bool) (app.ScaffoldResult, error)
	GetConfig() (models.Config, error)
	SetConfig(models.Config) error
	PathStatus() (app.PathStatus, error)
	AddToPath() (app.PathStatus, error)
}

// App owns the single tview.Application and the four screens.
type App struct {
	app    *tview.Application
	pages  *tview.Pages
	tabs   *tview.TextView
	hint   *tview.TextView
	status *tview.TextView
	root   tview.Primitive
	svc    Service

	catalog       *tview.Table
	catalogSearch *tview.InputField
	catalogCat    *tview.DropDown
	allApps       []models.ManifestEntry
	catalogApps   []models.ManifestEntry

	detail     *tview.TextView
	detailRepo string

	installed     *tview.Table
	installedApps []models.InstalledApp
	verifyState   map[string]string

	templates    []models.Template
	newForm      *tview.Form
	templateDrop *tview.DropDown
	targetInput  *tview.InputField

	configForm      *tview.Form
	manifestInput   *tview.InputField
	installDirInput *tview.InputField
}

// New builds the application and all screens. It does no I/O; call Run to start.
func New(svc Service) *App {
	a := &App{
		app:         tview.NewApplication(),
		pages:       tview.NewPages(),
		tabs:        tview.NewTextView().SetDynamicColors(true),
		hint:        tview.NewTextView().SetDynamicColors(true),
		status:      tview.NewTextView().SetDynamicColors(true),
		svc:         svc,
		verifyState: map[string]string{},
	}
	a.tabs.SetText(tabsText(pageCatalog))
	a.hint.SetText(hintFor(pageCatalog))

	a.buildCatalog()
	a.buildDetail()
	a.buildInstalled()
	a.buildNew()
	a.buildConfig()

	a.pages.AddPage(pageCatalog, a.catalogPage(), true, true)
	a.pages.AddPage(pageDetail, a.detailPage(), true, false)
	a.pages.AddPage(pageInstalled, a.installedPage(), true, false)
	a.pages.AddPage(pageNew, a.newPage(), true, false)
	a.pages.AddPage(pageConfig, a.configPage(), true, false)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.tabs, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.hint, 1, 0, false).
		AddItem(a.status, 1, 0, false)

	a.app.SetInputCapture(a.globalKeys)
	return a
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
	return a.app.SetRoot(a.root, true).EnableMouse(true).Run()
}

func (a *App) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	front, _ := a.pages.GetFrontPage()
	if front == pageAssetPick || front == pageConfirm || front == pageWarnPath {
		return ev // let the overlay handle everything
	}
	switch ev.Key() {
	case tcell.KeyCtrlC:
		a.app.Stop()
		return nil
	case tcell.KeyTab:
		a.switchTo(nextPage(front))
		return nil
	case tcell.KeyBacktab:
		a.switchTo(prevPage(front))
		return nil
	case tcell.KeyRune:
		// While typing in a field, runes belong to the input, not to navigation.
		if _, typing := a.app.GetFocus().(*tview.InputField); typing {
			return ev
		}
		r := ev.Rune()
		if r == 'q' {
			a.app.Stop()
			return nil
		}
		if r >= '1' && r <= '9' {
			if idx := int(r - '1'); idx < len(pageOrder) {
				a.switchTo(pageOrder[idx])
				return nil
			}
		}
	}
	return ev
}

func (a *App) switchTo(page string) {
	a.pages.SwitchToPage(page)
	a.tabs.SetText(tabsText(page))
	a.hint.SetText(hintFor(page))
	a.app.SetFocus(a.focusFor(page))
}

func (a *App) focusFor(page string) tview.Primitive {
	switch page {
	case pageCatalog:
		return a.catalog
	case pageDetail:
		return a.detail
	case pageInstalled:
		return a.installed
	case pageNew:
		return a.newForm
	case pageConfig:
		return a.configForm
	}
	return a.pages
}

func (a *App) setStatus(format string, args ...any) {
	a.status.SetText(fmt.Sprintf(format, args...))
}

// --- Catalog screen ---

func (a *App) buildCatalog() {
	a.catalog = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	a.catalog.SetSelectedFunc(func(row, _ int) {
		if row >= 1 && row-1 < len(a.catalogApps) {
			a.openDetail(a.catalogApps[row-1].Repo)
		}
	})
	// `/` jumps to the search field (per spec); the table otherwise owns focus
	// so arrow keys and the 1-5 quick-switch work without stealing keystrokes.
	a.catalog.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyRune && ev.Rune() == '/' {
			a.app.SetFocus(a.catalogSearch)
			return nil
		}
		return ev
	})

	a.catalogSearch = tview.NewInputField().SetLabel("Search: ")
	a.catalogSearch.SetChangedFunc(func(string) { a.applyCatalogFilter() })
	// Enter/Esc hand focus back to the results table.
	a.catalogSearch.SetDoneFunc(func(tcell.Key) { a.app.SetFocus(a.catalog) })

	a.catalogCat = tview.NewDropDown().SetLabel("Category: ")
	a.catalogCat.SetOptions([]string{"(all)"}, nil)
	a.catalogCat.SetCurrentOption(0)
	a.catalogCat.SetSelectedFunc(func(string, int) { a.applyCatalogFilter() })
}

func (a *App) catalogPage() tview.Primitive {
	controls := tview.NewFlex().
		AddItem(a.catalogSearch, 0, 2, false).
		AddItem(a.catalogCat, 0, 1, false)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(controls, 1, 0, false).
		AddItem(a.catalog, 0, 1, true)
}

func (a *App) loadCatalog() {
	apps, err := a.svc.ListCatalog(context.Background())
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]catalog: %s", err.Error())
			return
		}
		a.allApps = apps
		cats := append([]string{"(all)"}, distinctCategories(apps)...)
		a.catalogCat.SetOptions(cats, nil)
		a.catalogCat.SetCurrentOption(0)
		a.applyCatalogFilter()
		a.setStatus("catalog: %d app(s)", len(apps))
	})
}

// applyCatalogFilter runs on the event loop (from change handlers) and filters
// the already-fetched catalog in memory — no network.
func (a *App) applyCatalogFilter() {
	category := ""
	if idx, opt := a.catalogCat.GetCurrentOption(); idx > 0 {
		category = opt
	}
	a.catalogApps = filterApps(a.allApps, a.catalogSearch.GetText(), category)
	a.renderCatalog()
}

func (a *App) renderCatalog() {
	renderTable(a.catalog, catalogHeader, len(a.catalogApps), func(i int) []string {
		return catalogRow(a.catalogApps[i])
	})
}

// --- Detail screen ---

func (a *App) buildDetail() {
	a.detail = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	a.detail.SetBorder(true).SetTitle(" Details ([i] install · [Esc] back) ")
	a.detail.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyEscape:
			a.switchTo(pageCatalog)
			return nil
		case ev.Key() == tcell.KeyRune && ev.Rune() == 'i' && a.detailRepo != "":
			a.doInstall(a.detailRepo, "", "", false)
			return nil
		}
		return ev
	})
}

func (a *App) detailPage() tview.Primitive { return a.detail }

func (a *App) openDetail(repo string) {
	a.detailRepo = repo
	a.detail.SetText("[gray]loading " + repo + "…[-]")
	a.switchTo(pageDetail)
	go func() {
		d, err := a.svc.AppDetails(context.Background(), repo)
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.detail.SetText("[red]" + err.Error())
				a.setStatus("[red]details: %s", err.Error())
				return
			}
			a.detail.SetText(detailText(d))
			a.setStatus("%s", repo)
		})
	}()
}

func (a *App) doInstall(repo, version, asset string, allowUnverified bool) {
	a.setStatus("installing %s…", repo)
	go func() {
		rec, err := a.svc.Install(context.Background(), repo, version, asset, allowUnverified)
		a.app.QueueUpdateDraw(func() {
			var sel *app.AssetSelectionError
			if errors.As(err, &sel) {
				a.showAssetPick(repo, sel.Assets)
				return
			}
			if err != nil {
				a.setStatus("[red]install: %s", err.Error())
				return
			}
			a.setStatus("installed %s %s", rec.Repo, rec.Version)
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
			a.switchTo(pageDetail)
			return nil
		}
		return ev
	})
	a.pages.AddPage(pageAssetPick, modalWrap(list, 70, 18), true, true)
	a.app.SetFocus(list)
}

// --- Installed screen ---

func (a *App) buildInstalled() {
	a.installed = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	a.installed.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() != tcell.KeyRune {
			return ev
		}
		row, _ := a.installed.GetSelection()
		if row < 1 || row-1 >= len(a.installedApps) {
			return ev
		}
		repo := a.installedApps[row-1].Repo
		switch ev.Rune() {
		case 'u':
			a.doUpdate(repo)
			return nil
		case 'x':
			a.confirmUninstall(repo)
			return nil
		case 'v':
			a.doVerify(repo)
			return nil
		}
		return ev
	})
}

func (a *App) installedPage() tview.Primitive {
	wrap := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(a.installed, 0, 1, true)
	wrap.SetBorder(true).SetTitle(" Installed ([u] update · [x] uninstall · [v] verify) ")
	return wrap
}

func (a *App) loadInstalled() {
	list, err := a.svc.ListInstalled()
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]installed: %s", err.Error())
			return
		}
		a.installedApps = list
		a.renderInstalled()
	})
}

func (a *App) renderInstalled() {
	renderTable(a.installed, installedHeader, len(a.installedApps), func(i int) []string {
		ia := a.installedApps[i]
		return installedRow(ia, a.verifyState[ia.Repo])
	})
}

func (a *App) doUpdate(repo string) {
	a.setStatus("updating %s…", repo)
	go func() {
		res, err := a.svc.Update(context.Background(), repo)
		a.app.QueueUpdateDraw(func() {
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
				a.setStatus("updated %s %s → %s", repo, res.From, res.To)
			} else {
				a.setStatus("%s already current (%s)", repo, res.To)
			}
			go a.loadInstalled()
		})
	}()
}

func (a *App) confirmUninstall(repo string) {
	modal := tview.NewModal().
		SetText("Uninstall " + repo + "?").
		AddButtons([]string{"Cancel", "Uninstall"}).
		SetDoneFunc(func(_ int, label string) {
			a.pages.RemovePage(pageConfirm)
			a.switchTo(pageInstalled)
			if label == "Uninstall" {
				a.doUninstall(repo)
			}
		})
	a.pages.AddPage(pageConfirm, modal, true, true)
	a.app.SetFocus(modal)
}

func (a *App) doUninstall(repo string) {
	a.setStatus("uninstalling %s…", repo)
	go func() {
		err := a.svc.Uninstall(repo)
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.setStatus("[red]uninstall: %s", err.Error())
				return
			}
			delete(a.verifyState, repo)
			a.setStatus("uninstalled %s", repo)
			go a.loadInstalled()
		})
	}()
}

func (a *App) doVerify(repo string) {
	a.setStatus("verifying %s…", repo)
	go func() {
		st, err := a.svc.Verify(repo)
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.setStatus("[red]verify: %s", err.Error())
				return
			}
			a.verifyState[repo] = string(st)
			a.renderInstalled()
			a.setStatus("%s: %s", repo, string(st))
		})
	}()
}

// --- New App screen ---

func (a *App) buildNew() {
	a.templateDrop = tview.NewDropDown().SetLabel("Template: ").SetOptions([]string{"(loading…)"}, nil)
	a.targetInput = tview.NewInputField().SetLabel("Target dir: ")
	a.newForm = tview.NewForm().
		AddFormItem(a.templateDrop).
		AddFormItem(a.targetInput).
		AddButton("Scaffold", a.doScaffold)
	a.newForm.SetBorder(true).SetTitle(" New App (scaffold → /product-idea) ")
}

func (a *App) newPage() tview.Primitive { return a.newForm }

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
	})
}

func (a *App) doScaffold() {
	idx, _ := a.templateDrop.GetCurrentOption()
	if idx < 0 || idx >= len(a.templates) {
		a.setStatus("[red]select a template first")
		return
	}
	tmpl := a.templates[idx]
	target := strings.TrimSpace(a.targetInput.GetText())
	if target == "" {
		a.setStatus("[red]enter a target directory")
		return
	}
	a.setStatus("scaffolding %s…", tmpl.Repo)
	go func() {
		res, err := a.svc.Scaffold(context.Background(), tmpl.Repo, target, tmpl.Ref, false)
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.setStatus("[red]scaffold: %s", err.Error())
				return
			}
			a.setStatus("scaffolded %d file(s) into %s", res.Files, res.TargetDir)
		})
		if err == nil {
			a.launchProductIdea(res.TargetDir)
		}
	}()
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
			a.app.SetFocus(a.focusFor(pageCatalog))
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
	a.installDirInput = tview.NewInputField().SetLabel("Install dir:  ").SetFieldWidth(60)
	a.configForm = tview.NewForm().
		AddFormItem(a.manifestInput).
		AddFormItem(a.installDirInput).
		AddButton("Save", a.doSaveConfig)
	a.configForm.SetBorder(true).SetTitle(" Config (manifest URL required for catalog actions) ")
}

func (a *App) configPage() tview.Primitive { return a.configForm }

func (a *App) loadConfig() {
	cfg, err := a.svc.GetConfig()
	a.app.QueueUpdateDraw(func() {
		if err != nil {
			a.setStatus("[red]config: %s", err.Error())
			return
		}
		a.manifestInput.SetText(cfg.ManifestURL)
		a.installDirInput.SetText(cfg.InstallDir)
	})
}

func (a *App) doSaveConfig() {
	cfg := models.Config{
		ManifestURL: strings.TrimSpace(a.manifestInput.GetText()),
		InstallDir:  strings.TrimSpace(a.installDirInput.GetText()),
	}
	a.setStatus("saving config…")
	go func() {
		err := a.svc.SetConfig(cfg)
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.setStatus("[red]config: %s", err.Error())
				return
			}
			a.setStatus("config saved")
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

// --- shared helpers ---

// renderTable rebuilds a table from a header and rowCount rows, freezing the
// header row and skipping it for selection.
func renderTable(table *tview.Table, header []string, rowCount int, row func(i int) []string) {
	table.Clear()
	for c, h := range header {
		table.SetCell(0, c, tview.NewTableCell(h).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false).
			SetExpansion(1))
	}
	for i := 0; i < rowCount; i++ {
		cells := row(i)
		for c, v := range cells {
			table.SetCell(i+1, c, tview.NewTableCell(v).SetExpansion(1))
		}
	}
	if rowCount > 0 {
		table.Select(1, 0)
	}
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
