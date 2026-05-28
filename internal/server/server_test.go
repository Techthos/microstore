package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/db"
	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
	"techthos.net/microstore/internal/server"
)

type fakeGH struct {
	catalog  models.Catalog
	releases []models.Release
	blobs    map[string][]byte
	tarball  []byte
}

func (f *fakeGH) FetchCatalog(_ context.Context, url string) (models.Catalog, error) {
	if url == "" {
		return models.Catalog{}, fmt.Errorf("manifest URL not set")
	}
	return f.catalog, nil
}

func (f *fakeGH) RepoInfo(_ context.Context, repo string) (models.RepoInfo, error) {
	return models.RepoInfo{FullName: repo}, nil
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

func newClient(t *testing.T, gh app.Cataloger, manifestURL string) *client.Client {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := app.New(gh, store)
	if err := svc.SetConfig(models.Config{ManifestURL: manifestURL, InstallDir: t.TempDir()}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	c, err := client.NewInProcessClient(server.New(svc, "microstore-test", "0.0.0"))
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      mcp.Implementation{Name: "test", Version: "1.0.0"},
	}}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func call(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty result content")
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("content[0] is not text: %T", res.Content[0])
	}
	return tc.Text
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode result: %v\nraw: %s", err, resultText(t, res))
	}
	return out
}

func hostAsset() string { return "app_" + install.HostOS() + "_" + install.HostArch() }

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := io.WriteString(tw, body); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func verifiedRelease(tag string, bin []byte) (models.Release, map[string][]byte) {
	assetURL := "https://dl/" + tag
	sumURL := "https://sum/" + tag
	name := hostAsset()
	rel := models.Release{TagName: tag, Assets: []models.Asset{
		{Name: name, DownloadURL: assetURL, Size: int64(len(bin))},
		{Name: "checksums.txt", DownloadURL: sumURL},
	}}
	return rel, map[string][]byte{assetURL: bin, sumURL: []byte(fmt.Sprintf("%s  %s\n", sha(bin), name))}
}

func TestListToolsExposesFullSurface(t *testing.T) {
	t.Parallel()
	c := newClient(t, &fakeGH{}, "https://manifest")
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	want := []string{
		"get_config", "set_config",
		"list_catalog", "search_apps", "app_details", "list_releases", "list_installed",
		"install_app", "update_app", "uninstall_app", "verify_app", "list_templates", "scaffold_app",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("tools/list missing %q (got %d tools: %v)", name, len(res.Tools), got)
		}
	}
	if len(res.Tools) != len(want) {
		t.Errorf("tools/list returned %d tools, want %d", len(res.Tools), len(want))
	}
}

func TestConfigTools(t *testing.T) {
	t.Parallel()
	// Start from an empty manifest URL so set_config has an observable effect.
	c := newClient(t, &fakeGH{}, "")

	set := decode[struct {
		Config models.Config `json:"config"`
	}](t, call(t, c, "set_config", map[string]any{"manifest_url": "https://m/catalog.json"}))
	if set.Config.ManifestURL != "https://m/catalog.json" {
		t.Errorf("set_config ManifestURL = %q", set.Config.ManifestURL)
	}
	if set.Config.InstallDir == "" {
		t.Error("set_config should preserve the existing (default) InstallDir, got empty")
	}

	got := decode[struct {
		Config models.Config `json:"config"`
	}](t, call(t, c, "get_config", nil))
	if got.Config.ManifestURL != "https://m/catalog.json" {
		t.Errorf("get_config ManifestURL = %q, want persisted value", got.Config.ManifestURL)
	}
}

func TestListCatalogTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{catalog: models.Catalog{Apps: []models.ManifestEntry{
		{Repo: "o/a", Category: "tools", DisplayName: "A"},
		{Repo: "o/b", Category: "games"},
	}}}
	c := newClient(t, gh, "https://manifest")
	out := decode[struct {
		Apps []models.ManifestEntry `json:"apps"`
	}](t, call(t, c, "list_catalog", nil))
	if len(out.Apps) != 2 {
		t.Errorf("apps = %d, want 2", len(out.Apps))
	}
}

func TestSearchAppsTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{catalog: models.Catalog{Apps: []models.ManifestEntry{
		{Repo: "o/alpha", Category: "tools", DisplayName: "Alpha"},
		{Repo: "o/beta", Category: "games", DisplayName: "Beta"},
	}}}
	c := newClient(t, gh, "https://manifest")
	out := decode[struct {
		Apps []models.ManifestEntry `json:"apps"`
	}](t, call(t, c, "search_apps", map[string]any{"category": "tools"}))
	if len(out.Apps) != 1 || out.Apps[0].Repo != "o/alpha" {
		t.Errorf("apps = %+v", out.Apps)
	}
}

func TestInstallToolSuccess(t *testing.T) {
	t.Parallel()
	bin := []byte("the-binary")
	rel, blobs := verifiedRelease("v1.0.0", bin)
	gh := &fakeGH{
		catalog:  models.Catalog{Apps: []models.ManifestEntry{{Repo: "o/app", Category: "tools", DisplayName: "App"}}},
		releases: []models.Release{rel},
		blobs:    blobs,
	}
	c := newClient(t, gh, "https://manifest")

	res := call(t, c, "install_app", map[string]any{"repo": "o/app"})
	if res.IsError {
		t.Fatalf("install errored: %s", resultText(t, res))
	}
	out := decode[struct {
		Installed models.InstalledApp `json:"installed"`
	}](t, res)
	if out.Installed.Version != "v1.0.0" || out.Installed.SHA256 != sha(bin) {
		t.Errorf("installed = %+v", out.Installed)
	}

	// Now visible via list_installed.
	listed := decode[struct {
		Installed []models.InstalledApp `json:"installed"`
	}](t, call(t, c, "list_installed", nil))
	if len(listed.Installed) != 1 || listed.Installed[0].Repo != "o/app" {
		t.Errorf("list_installed = %+v", listed.Installed)
	}

	// verify_app reports ok.
	vres := decode[struct {
		Status string `json:"status"`
	}](t, call(t, c, "verify_app", map[string]any{"repo": "o/app"}))
	if vres.Status != "ok" {
		t.Errorf("verify status = %q, want ok", vres.Status)
	}

	// uninstall_app removes it.
	ures := decode[struct {
		Removed bool `json:"removed"`
	}](t, call(t, c, "uninstall_app", map[string]any{"repo": "o/app"}))
	if !ures.Removed {
		t.Errorf("removed = false, want true")
	}
}

func TestInstallToolAmbiguousIsError(t *testing.T) {
	t.Parallel()
	name := hostAsset()
	rel := models.Release{TagName: "v1.0.0", Assets: []models.Asset{
		{Name: name, DownloadURL: "u1"},
		{Name: "extra_" + name, DownloadURL: "u2"},
	}}
	gh := &fakeGH{releases: []models.Release{rel}}
	c := newClient(t, gh, "https://manifest")

	res := call(t, c, "install_app", map[string]any{"repo": "o/app"})
	if !res.IsError {
		t.Fatalf("expected error result, got: %s", resultText(t, res))
	}
	msg := resultText(t, res)
	if !bytes.Contains([]byte(msg), []byte(name)) {
		t.Errorf("error message should enumerate assets, got: %s", msg)
	}
}

func TestAppDetailsTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{releases: []models.Release{
		{TagName: "v2.0.0-rc1", Prerelease: true},
		{TagName: "v1.0.0", Prerelease: false},
	}}
	c := newClient(t, gh, "https://manifest")
	out := decode[app.AppDetails](t, call(t, c, "app_details", map[string]any{"repo": "o/app"}))
	if out.Latest.TagName != "v1.0.0" {
		t.Errorf("latest = %q, want v1.0.0", out.Latest.TagName)
	}
	if out.Installed != nil {
		t.Errorf("installed = %+v, want nil", out.Installed)
	}
}

func TestListReleasesTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{releases: []models.Release{{TagName: "v2.0.0"}, {TagName: "v1.0.0"}}}
	c := newClient(t, gh, "https://manifest")
	out := decode[struct {
		Releases []models.Release `json:"releases"`
	}](t, call(t, c, "list_releases", map[string]any{"repo": "o/app"}))
	if len(out.Releases) != 2 || out.Releases[0].TagName != "v2.0.0" {
		t.Errorf("releases = %+v", out.Releases)
	}
}

func TestUpdateTool(t *testing.T) {
	t.Parallel()
	binV1 := []byte("v1")
	relV1, blobs := verifiedRelease("v1.0.0", binV1)
	gh := &fakeGH{releases: []models.Release{relV1}, blobs: blobs}
	c := newClient(t, gh, "https://manifest")

	if res := call(t, c, "install_app", map[string]any{"repo": "o/app"}); res.IsError {
		t.Fatalf("install: %s", resultText(t, res))
	}
	out := decode[app.UpdateResult](t, call(t, c, "update_app", map[string]any{"repo": "o/app"}))
	if out.Updated {
		t.Errorf("expected no-op update, got %+v", out)
	}
}

func TestListTemplatesTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{catalog: models.Catalog{Templates: []models.Template{{Repo: "o/t", Ref: "main", Name: "base"}}}}
	c := newClient(t, gh, "https://manifest")
	out := decode[struct {
		Templates []models.Template `json:"templates"`
	}](t, call(t, c, "list_templates", nil))
	if len(out.Templates) != 1 || out.Templates[0].Ref != "main" {
		t.Errorf("templates = %+v", out.Templates)
	}
}

func TestScaffoldTool(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{
		catalog: models.Catalog{Templates: []models.Template{{Repo: "o/t", Ref: "main"}}},
		tarball: tarGz(t, "top/main.go", "package main\n"),
	}
	c := newClient(t, gh, "https://manifest")
	target := filepath.Join(t.TempDir(), "newapp")
	out := decode[app.ScaffoldResult](t, call(t, c, "scaffold_app", map[string]any{
		"template_repo": "o/t", "target_dir": target,
	}))
	if out.Files != 1 {
		t.Errorf("files = %d, want 1", out.Files)
	}
}

func TestCatalogResource(t *testing.T) {
	t.Parallel()
	gh := &fakeGH{catalog: models.Catalog{Apps: []models.ManifestEntry{{Repo: "o/a", Category: "tools"}}}}
	c := newClient(t, gh, "https://manifest")
	res, err := c.ReadResource(context.Background(), mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "catalog://list"},
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) == 0 {
		t.Fatal("empty resource contents")
	}
	tc, ok := res.Contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("content is not text: %T", res.Contents[0])
	}
	var out struct {
		Apps []models.ManifestEntry `json:"apps"`
	}
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Apps) != 1 {
		t.Errorf("apps = %d, want 1", len(out.Apps))
	}
}
