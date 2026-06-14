package app_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/db"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
)

type fakeGH struct {
	catalog  models.Catalog
	repoInfo models.RepoInfo
	releases []models.Release
	blobs    map[string][]byte
	tarball  []byte
}

func (f *fakeGH) FetchCatalog(_ context.Context, url string) (models.Catalog, error) {
	if strings.TrimSpace(url) == "" {
		return models.Catalog{}, fmt.Errorf("manifest URL not set")
	}
	return f.catalog, nil
}

func (f *fakeGH) RepoInfo(_ context.Context, _ string) (models.RepoInfo, error) {
	return f.repoInfo, nil
}

func (f *fakeGH) Releases(_ context.Context, _ string) ([]models.Release, error) {
	return f.releases, nil
}

func (f *fakeGH) LatestRelease(_ context.Context, _ string) (models.Release, error) {
	for _, r := range f.releases {
		if !r.Prerelease {
			return r, nil
		}
	}
	return models.Release{}, fmt.Errorf("no published release")
}

func (f *fakeGH) Download(_ context.Context, url string, w io.Writer) (int64, error) {
	b, ok := f.blobs[url]
	if !ok {
		return 0, fmt.Errorf("not found: %s", url)
	}
	n, err := w.Write(b)
	return int64(n), err
}

func (f *fakeGH) Tarball(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.tarball)), nil
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hostAssetName() string { return "app_" + install.HostOS() + "_" + install.HostArch() }

// release with one host-matching asset + checksums.
func verifiedRelease(tag string, bin []byte) (models.Release, map[string][]byte) {
	assetURL := "https://dl/" + tag + "/asset"
	sumURL := "https://dl/" + tag + "/checksums"
	name := hostAssetName()
	rel := models.Release{
		TagName: tag,
		Assets: []models.Asset{
			{Name: name, DownloadURL: assetURL, Size: int64(len(bin))},
			{Name: "checksums.txt", DownloadURL: sumURL},
		},
	}
	blobs := map[string][]byte{
		assetURL: bin,
		sumURL:   []byte(fmt.Sprintf("%s  %s\n", sha(bin), name)),
	}
	return rel, blobs
}

func newStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// configured returns a service whose store has a manifest URL and temp install dir.
func configured(t *testing.T, gh app.Cataloger) (*app.Service, string) {
	t.Helper()
	store := newStore(t)
	dir := t.TempDir()
	svc := app.New(gh, store)
	if err := svc.SetConfig(models.Config{ManifestURL: "https://manifest", InstallDir: dir}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	return svc, dir
}

func TestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	svc := app.New(&fakeGH{}, newStore(t))
	cfg := models.Config{ManifestURL: "https://m", InstallDir: "/d"}
	if err := svc.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	got, err := svc.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got != cfg {
		t.Errorf("config = %+v, want %+v", got, cfg)
	}
}

func TestPathStatus(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	store := newStore(t)
	svc := app.New(&fakeGH{}, store)
	bin := t.TempDir()
	if err := svc.SetConfig(models.Config{InstallDir: bin}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("not on path", func(t *testing.T) {
		t.Setenv("PATH", "/usr/bin")
		st, err := svc.PathStatus()
		if err != nil {
			t.Fatalf("PathStatus: %v", err)
		}
		if st.OnPath {
			t.Error("OnPath = true, want false")
		}
		if st.InstallDir != bin {
			t.Errorf("InstallDir = %q, want %q", st.InstallDir, bin)
		}
		if want := filepath.Join(home, ".zshrc"); st.ProfilePath != want {
			t.Errorf("ProfilePath = %q, want %q", st.ProfilePath, want)
		}
		if !strings.Contains(st.ExportLine, bin) {
			t.Errorf("ExportLine = %q, want it to mention %q", st.ExportLine, bin)
		}
	})

	t.Run("on path", func(t *testing.T) {
		t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+bin)
		st, err := svc.PathStatus()
		if err != nil {
			t.Fatalf("PathStatus: %v", err)
		}
		if !st.OnPath {
			t.Error("OnPath = false, want true")
		}
	})
}

func TestAddToPath(t *testing.T) {
	store := newStore(t)
	svc := app.New(&fakeGH{}, store)
	bin := t.TempDir()
	if err := svc.SetConfig(models.Config{InstallDir: bin}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("PATH", "/usr/bin")

	st, err := svc.AddToPath()
	if err != nil {
		t.Fatalf("AddToPath: %v", err)
	}
	data, err := os.ReadFile(st.ProfilePath)
	if err != nil {
		t.Fatalf("read profile %q: %v", st.ProfilePath, err)
	}
	if !strings.Contains(string(data), st.ExportLine) {
		t.Errorf("profile missing export line %q:\n%s", st.ExportLine, data)
	}
	if want := filepath.Join(home, ".bashrc"); st.ProfilePath != want {
		t.Errorf("ProfilePath = %q, want %q", st.ProfilePath, want)
	}
}

func TestListCatalogEmptyManifestURL(t *testing.T) {
	t.Parallel()
	svc := app.New(&fakeGH{}, newStore(t))
	if err := svc.SetConfig(models.Config{ManifestURL: ""}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	_, err := svc.ListCatalog(context.Background())
	if err == nil || !strings.Contains(err.Error(), "manifest URL not set") {
		t.Fatalf("err = %v, want \"manifest URL not set\"", err)
	}
}

func TestSearchApps(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{catalog: models.Catalog{Apps: []models.ManifestEntry{
		{Repo: "o/alpha", Category: "tools", DisplayName: "Alpha"},
		{Repo: "o/beta", Category: "games", DisplayName: "Beta"},
		{Repo: "o/altimeter", Category: "tools", DisplayName: "Altimeter"},
	}}}
	svc, _ := configured(t, gh)
	ctx := context.Background()

	all, _ := svc.SearchApps(ctx, "", "")
	if len(all) != 3 {
		t.Errorf("no filter: got %d, want 3", len(all))
	}
	tools, _ := svc.SearchApps(ctx, "", "tools")
	if len(tools) != 2 {
		t.Errorf("category tools: got %d, want 2", len(tools))
	}
	alt, _ := svc.SearchApps(ctx, "alt", "")
	if len(alt) != 1 || alt[0].Repo != "o/altimeter" {
		t.Errorf("query alt: got %+v", alt)
	}
	combined, _ := svc.SearchApps(ctx, "al", "tools")
	if len(combined) != 2 {
		t.Errorf("combined: got %d, want 2 (Alpha, Altimeter)", len(combined))
	}
}

func TestInstallAndList(t *testing.T) {
	t.Parallel()
	bin := []byte("the-binary")
	rel, blobs := verifiedRelease("v1.0.0", bin)
	gh := &fakeGH{
		catalog:  models.Catalog{Apps: []models.ManifestEntry{{Repo: "o/app", Category: "tools", DisplayName: "App"}}},
		releases: []models.Release{rel},
		blobs:    blobs,
	}
	svc, dir := configured(t, gh)
	ctx := context.Background()

	rec, err := svc.Install(ctx, "o/app", "", "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rec.Version != "v1.0.0" || rec.SHA256 != sha(bin) || rec.DisplayName != "App" {
		t.Errorf("record = %+v", rec)
	}
	if filepath.Dir(rec.Path) != dir {
		t.Errorf("Path %q not under install dir %q", rec.Path, dir)
	}
	if _, err := os.Stat(rec.Path); err != nil {
		t.Errorf("placed binary missing: %v", err)
	}
	list, err := svc.ListInstalled()
	if err != nil || len(list) != 1 || list[0].Repo != "o/app" {
		t.Errorf("ListInstalled = %+v, err %v", list, err)
	}
}

// TestInstallAndUpdateBinOverride pins the self-hosting contract: a catalog
// entry with a "bin" override (microstore lists itself as bin "store") installs
// as microapp-<bin>, and an update re-places the binary at that same filename
// even though the entry is reconstructed from the persisted record.
func TestInstallAndUpdateBinOverride(t *testing.T) {
	t.Parallel()
	relV1, blobs := verifiedRelease("v1.0.0", []byte("v1"))
	gh := &fakeGH{
		catalog:  models.Catalog{Apps: []models.ManifestEntry{{Repo: "Techthos/microstore", Category: "tools", DisplayName: "microstore", Bin: "store"}}},
		releases: []models.Release{relV1},
		blobs:    blobs,
	}
	svc, dir := configured(t, gh)
	ctx := context.Background()

	rec, err := svc.Install(ctx, "Techthos/microstore", "", "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	wantPath := filepath.Join(dir, "microapp-store")
	if rec.Path != wantPath || rec.Bin != "store" {
		t.Errorf("record = Path %q Bin %q, want Path %q Bin \"store\"", rec.Path, rec.Bin, wantPath)
	}

	relV2, blobsV2 := verifiedRelease("v2.0.0", []byte("v2"))
	gh.releases = []models.Release{relV2, relV1}
	for k, v := range blobsV2 {
		gh.blobs[k] = v
	}
	res, err := svc.Update(ctx, "Techthos/microstore")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !res.Updated || res.Installed.Path != wantPath || res.Installed.Bin != "store" {
		t.Errorf("update result = %+v, want updated at %q with Bin \"store\"", res, wantPath)
	}
}

func TestInstallAmbiguousAsset(t *testing.T) {
	t.Parallel()
	name := hostAssetName()
	rel := models.Release{TagName: "v1.0.0", Assets: []models.Asset{
		{Name: name, DownloadURL: "u1"},
		{Name: "extra_" + name, DownloadURL: "u2"},
	}}
	gh := &fakeGH{releases: []models.Release{rel}}
	svc, _ := configured(t, gh)

	_, err := svc.Install(context.Background(), "o/app", "", "", false)
	var sel *app.AssetSelectionError
	if !errors.As(err, &sel) {
		t.Fatalf("err = %v, want *AssetSelectionError", err)
	}
	if len(sel.Assets) != 2 {
		t.Errorf("AssetSelectionError.Assets = %d, want 2", len(sel.Assets))
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	binV1 := []byte("v1")
	relV1, blobsV1 := verifiedRelease("v1.0.0", binV1)
	gh := &fakeGH{releases: []models.Release{relV1}, blobs: blobsV1}
	svc, _ := configured(t, gh)
	ctx := context.Background()

	if _, err := svc.Install(ctx, "o/app", "", "", false); err != nil {
		t.Fatalf("install v1: %v", err)
	}

	// No-op when already current.
	res, err := svc.Update(ctx, "o/app")
	if err != nil {
		t.Fatalf("Update (current): %v", err)
	}
	if res.Updated {
		t.Errorf("expected no-op, got %+v", res)
	}

	// Newer release available.
	binV2 := []byte("v2")
	relV2, blobsV2 := verifiedRelease("v2.0.0", binV2)
	gh.releases = []models.Release{relV2, relV1}
	for k, v := range blobsV2 {
		gh.blobs[k] = v
	}
	res, err = svc.Update(ctx, "o/app")
	if err != nil {
		t.Fatalf("Update (newer): %v", err)
	}
	if !res.Updated || res.From != "v1.0.0" || res.To != "v2.0.0" {
		t.Errorf("update result = %+v", res)
	}
}

func TestUpdateNotInstalled(t *testing.T) {
	t.Parallel()
	svc, _ := configured(t, &fakeGH{})
	_, err := svc.Update(context.Background(), "o/ghost")
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v, want \"not installed\"", err)
	}
}

func TestUninstall(t *testing.T) {
	t.Parallel()
	bin := []byte("bin")
	rel, blobs := verifiedRelease("v1.0.0", bin)
	gh := &fakeGH{releases: []models.Release{rel}, blobs: blobs}
	svc, _ := configured(t, gh)
	ctx := context.Background()

	rec, err := svc.Install(ctx, "o/app", "", "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := svc.Uninstall("o/app"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(rec.Path); !os.IsNotExist(err) {
		t.Errorf("binary still present after uninstall")
	}
	list, _ := svc.ListInstalled()
	if len(list) != 0 {
		t.Errorf("ListInstalled = %+v, want empty", list)
	}
	if err := svc.Uninstall("o/app"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("second Uninstall err = %v, want \"not installed\"", err)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	bin := []byte("bin")
	rel, blobs := verifiedRelease("v1.0.0", bin)
	gh := &fakeGH{releases: []models.Release{rel}, blobs: blobs}
	svc, _ := configured(t, gh)
	ctx := context.Background()

	rec, err := svc.Install(ctx, "o/app", "", "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	st, err := svc.Verify("o/app")
	if err != nil || st != install.VerifyOK {
		t.Fatalf("Verify = %s, err %v, want ok", st, err)
	}
	if err := os.WriteFile(rec.Path, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if st, _ := svc.Verify("o/app"); st != install.VerifyMismatch {
		t.Errorf("Verify after tamper = %s, want mismatch", st)
	}
}

func TestRunInstalled(t *testing.T) {
	t.Parallel()
	bin := []byte("bin")
	rel, blobs := verifiedRelease("v1.0.0", bin)
	gh := &fakeGH{releases: []models.Release{rel}, blobs: blobs}
	svc, _ := configured(t, gh)
	ctx := context.Background()

	rec, err := svc.Install(ctx, "o/app", "", "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Installed: resolves to the placed binary's absolute path.
	path, err := svc.RunInstalled("o/app")
	if err != nil {
		t.Fatalf("RunInstalled: %v", err)
	}
	if path != rec.Path {
		t.Errorf("RunInstalled path = %q, want %q", path, rec.Path)
	}

	// Not installed: a clear "not installed" error, no path.
	if _, err := svc.RunInstalled("o/missing"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("RunInstalled(unknown) err = %v, want \"not installed\"", err)
	}

	// Stale record (binary deleted out-of-band): fails loudly rather than
	// returning a path to nothing.
	if err := os.Remove(rec.Path); err != nil {
		t.Fatalf("remove binary: %v", err)
	}
	if _, err := svc.RunInstalled("o/app"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("RunInstalled(deleted) err = %v, want \"missing\"", err)
	}
}

func TestAppDetails(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{
		repoInfo: models.RepoInfo{FullName: "o/app", Description: "desc", Stars: 3},
		releases: []models.Release{
			{TagName: "v2.0.0-rc1", Prerelease: true},
			{TagName: "v1.0.0", Prerelease: false},
		},
	}
	svc, _ := configured(t, gh)
	d, err := svc.AppDetails(context.Background(), "o/app")
	if err != nil {
		t.Fatalf("AppDetails: %v", err)
	}
	if d.Repo.Stars != 3 || d.Latest.TagName != "v1.0.0" {
		t.Errorf("details = %+v", d)
	}
	if d.Installed != nil {
		t.Errorf("Installed = %+v, want nil", d.Installed)
	}
}

func TestScaffold(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "top/", Typeflag: tar.TypeDir, Mode: 0o755})
	body := "package main\n"
	_ = tw.WriteHeader(&tar.Header{Name: "top/main.go", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = io.WriteString(tw, body)
	_ = tw.Close()
	_ = gz.Close()

	gh := &fakeGH{
		catalog: models.Catalog{Templates: []models.Template{{Repo: "o/tmpl", Ref: "main", Name: "base"}}},
		tarball: buf.Bytes(),
	}
	svc, _ := configured(t, gh)
	target := filepath.Join(t.TempDir(), "newapp")

	res, err := svc.Scaffold(context.Background(), "o/tmpl", target, "", false) // ref resolved from catalog
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("Files = %d, want 1", res.Files)
	}
	if !strings.Contains(res.NextStep, "/product-idea") {
		t.Errorf("NextStep = %q, want mention of /product-idea", res.NextStep)
	}
	if _, err := os.Stat(filepath.Join(target, "main.go")); err != nil {
		t.Errorf("main.go not extracted: %v", err)
	}
}
