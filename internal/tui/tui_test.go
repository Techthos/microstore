package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
)

type fakeSvc struct {
	apps       []models.ManifestEntry
	installed  []models.InstalledApp
	pathStatus app.PathStatus
	ranRepo    chan string // when set, RunInstalled reports the repo it was asked to run
}

func (f fakeSvc) ListCatalog(context.Context) ([]models.ManifestEntry, error) { return f.apps, nil }
func (fakeSvc) AppDetails(context.Context, string) (app.AppDetails, error) {
	return app.AppDetails{}, nil
}
func (f fakeSvc) ListInstalled() ([]models.InstalledApp, error) { return f.installed, nil }
func (fakeSvc) Install(context.Context, string, string, string, bool) (models.InstalledApp, error) {
	return models.InstalledApp{}, nil
}

func (fakeSvc) Update(context.Context, string) (app.UpdateResult, error) {
	return app.UpdateResult{}, nil
}
func (fakeSvc) Uninstall(string) error { return nil }
func (fakeSvc) Verify(string) (install.VerifyStatus, error) {
	return install.VerifyOK, nil
}

// RunInstalled records the repo it was asked to launch and returns an error so
// the TUI's runInstalled stops before app.Suspend/exec — the wiring test only
// asserts that Enter reached the service, not that a real process is spawned.
func (f fakeSvc) RunInstalled(repo string) (string, error) {
	if f.ranRepo != nil {
		f.ranRepo <- repo
	}
	return "", errors.New("stub: not executed in test")
}
func (fakeSvc) ListTemplates(context.Context) ([]models.Template, error) { return nil, nil }
func (fakeSvc) Scaffold(context.Context, string, string, string, bool) (app.ScaffoldResult, error) {
	return app.ScaffoldResult{}, nil
}
func (fakeSvc) GetConfig() (models.Config, error) { return models.Config{}, nil }
func (fakeSvc) SetConfig(models.Config) error     { return nil }
func (fakeSvc) SaveUIPrefs(string, bool) error    { return nil }
func (f fakeSvc) PathStatus() (app.PathStatus, error) {
	return f.pathStatus, nil
}
func (fakeSvc) AddToPath() (app.PathStatus, error) { return app.PathStatus{}, nil }

func TestNewBuildsSections(t *testing.T) {
	t.Parallel()
	a := New(fakeSvc{})
	for _, p := range sectionOrder {
		if !a.pages.HasPage(p) {
			t.Errorf("missing section page %q", p)
		}
	}
	// Detail is now a master-detail pane, not a standalone section page.
	if a.pages.HasPage("detail") {
		t.Error("detail should not be a standalone section page")
	}
	if front, _ := a.pages.GetFrontPage(); front != pageCatalog {
		t.Errorf("front page = %q, want catalog", front)
	}
}

func TestSwitchToSection(t *testing.T) {
	t.Parallel()
	a := New(fakeSvc{})
	a.switchTo(pageInstalled)
	if front, _ := a.pages.GetFrontPage(); front != pageInstalled {
		t.Errorf("after switchTo(installed), front = %q", front)
	}
	if a.section != pageInstalled {
		t.Errorf("active section = %q, want installed", a.section)
	}
	// Switching to a non-section (overlay) id is a no-op.
	a.switchTo(pageHelp)
	if a.section != pageInstalled {
		t.Errorf("switchTo(non-section) changed section to %q", a.section)
	}
}

func TestShowHelpOverlay(t *testing.T) {
	t.Parallel()
	a := New(fakeSvc{})
	a.showHelp()
	if front, _ := a.pages.GetFrontPage(); front != pageHelp {
		t.Errorf("front page = %q, want help", front)
	}
	a.hideHelp()
	if a.pages.HasPage(pageHelp) {
		t.Error("help overlay not removed after hide")
	}
}

func TestShowPathWarningAddsOverlay(t *testing.T) {
	t.Parallel()
	a := New(fakeSvc{})
	a.showPathWarning(app.PathStatus{
		InstallDir:  "/home/u/bin",
		ProfilePath: "/home/u/.bashrc",
		ExportLine:  `export PATH="$PATH:/home/u/bin"`,
	})
	if !a.pages.HasPage(pageWarnPath) {
		t.Fatal("path-warning overlay not added")
	}
	if front, _ := a.pages.GetFrontPage(); front != pageWarnPath {
		t.Errorf("front page = %q, want %q", front, pageWarnPath)
	}
}

func TestPathWarningOnLaunchHeadless(t *testing.T) {
	a := New(fakeSvc{
		apps:       []models.ManifestEntry{{Repo: "o/a", Category: "tools", DisplayName: "Alpha"}},
		pathStatus: app.PathStatus{InstallDir: "/home/u/bin", ProfilePath: "/home/u/.bashrc", ExportLine: `export PATH="$PATH:/home/u/bin"`},
	})

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim init: %v", err)
	}
	sim.SetSize(120, 40)
	a.Application().SetScreen(sim)

	frames := make(chan string, 64)
	a.Application().SetAfterDrawFunc(func(tcell.Screen) {
		select {
		case frames <- screenText(sim):
		default:
		}
	})

	done := make(chan error, 1)
	go func() { done <- a.Run() }()

	// Generous deadline: standalone this renders in ~1s, but under the full
	// `-race` suite the event loop competes for CPU with every other package.
	deadline := time.After(15 * time.Second)
	// The launch burst (four loaders + the path check) can overflow the 64-frame
	// buffer, dropping frames; once the modal is up the screen is static, so the
	// modal's frame could be lost forever. Force a periodic redraw to regenerate
	// a fresh, observable frame regardless of any earlier drop.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for found := false; !found; {
		select {
		case txt := <-frames:
			// /home/u/bin is a single unbreakable token, so it survives the
			// modal's word-wrap (a multi-word phrase could split across rows).
			if strings.Contains(txt, "/home/u/bin") {
				found = true
			}
		case <-tick.C:
			a.Application().QueueUpdateDraw(func() {})
		case <-deadline:
			a.Application().Stop()
			t.Fatal("PATH warning not rendered within 15s")
		}
	}

	a.Application().Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

// TestInstalledEnterRunsHighlightedApp drives the real event loop: jump to the
// Installed section, wait for the row to render (so a row is selected), then
// press Enter and assert the TUI asked the service to run that exact repo (UC 13).
func TestInstalledEnterRunsHighlightedApp(t *testing.T) {
	ran := make(chan string, 1)
	a := New(fakeSvc{
		installed: []models.InstalledApp{{Repo: "o/app", Version: "v1.0.0"}},
		ranRepo:   ran,
	})

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim init: %v", err)
	}
	sim.SetSize(120, 40)
	a.Application().SetScreen(sim)

	frames := make(chan string, 64)
	a.Application().SetAfterDrawFunc(func(tcell.Screen) {
		select {
		case frames <- screenText(sim):
		default:
		}
	})

	done := make(chan error, 1)
	go func() { done <- a.Run() }()

	// Once the UI is up, jump to the Installed section (sidebar shortcut '2') and
	// wait until the installed row is on screen — that means the table is loaded
	// and its first data row is selected (renderTable selects row 1).
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	switched := false
	for ready := false; !ready; {
		select {
		case txt := <-frames:
			if !switched {
				a.Application().QueueEvent(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
				switched = true
			}
			if strings.Contains(txt, "o/app") {
				ready = true
			}
		case <-tick.C:
			a.Application().QueueUpdateDraw(func() {})
		case <-deadline:
			a.Application().Stop()
			t.Fatal("installed row not rendered within 15s")
		}
	}

	a.Application().QueueEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	select {
	case repo := <-ran:
		if repo != "o/app" {
			t.Errorf("RunInstalled repo = %q, want o/app", repo)
		}
	case <-time.After(15 * time.Second):
		a.Application().Stop()
		t.Fatal("Enter on the installed row did not trigger RunInstalled within 15s")
	}

	a.Application().Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

func screenText(sim tcell.SimulationScreen) string {
	cells, w, h := sim.GetContents()
	var b strings.Builder
	for i := 0; i < w*h; i++ {
		for _, r := range cells[i].Runes {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestRendersAndQuitsHeadless(t *testing.T) {
	a := New(fakeSvc{apps: []models.ManifestEntry{{Repo: "o/a", Category: "tools", DisplayName: "Alpha"}}})

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim init: %v", err)
	}
	sim.SetSize(120, 40)
	a.Application().SetScreen(sim)

	// Capture each rendered frame on the event-loop goroutine (inside the
	// after-draw hook); never read the screen from the test goroutine. The
	// first frames can be blank (pre-layout), so drain until content appears.
	frames := make(chan string, 64)
	a.Application().SetAfterDrawFunc(func(tcell.Screen) {
		select {
		case frames <- screenText(sim):
		default:
		}
	})

	done := make(chan error, 1)
	go func() { done <- a.Run() }()

	deadline := time.After(5 * time.Second)
	for found := false; !found; {
		select {
		case txt := <-frames:
			// The sidebar/header/border all carry the "Catalog" section label.
			if strings.Contains(txt, "Catalog") {
				found = true
			}
		case <-deadline:
			a.Application().Stop()
			t.Fatal("no catalog 'Search' label rendered within 5s")
		}
	}

	a.Application().Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}
