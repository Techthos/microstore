package install_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"techthos.net/microstore/internal/install"
	"techthos.net/microstore/internal/models"
)

// fakeDL serves canned bytes per URL.
type fakeDL struct{ files map[string][]byte }

func (f fakeDL) Download(_ context.Context, url string, w io.Writer) (int64, error) {
	b, ok := f.files[url]
	if !ok {
		return 0, fmt.Errorf("not found: %s", url)
	}
	n, err := w.Write(b)
	return int64(n), err
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

const (
	assetURL = "https://example.test/microstore_linux_amd64"
	sumURL   = "https://example.test/checksums.txt"
)

var binBytes = []byte("#!/bin/sh\necho hi\n")

func release(withChecksums bool) (models.Release, models.Asset) {
	asset := models.Asset{Name: "microstore_linux_amd64", DownloadURL: assetURL, Size: int64(len(binBytes))}
	assets := []models.Asset{asset}
	if withChecksums {
		assets = append(assets, models.Asset{Name: "checksums.txt", DownloadURL: sumURL})
	}
	return models.Release{TagName: "v1.0.0", Assets: assets}, asset
}

func entry() models.ManifestEntry {
	return models.ManifestEntry{Repo: "techthos/microstore", DisplayName: "microstore", Category: "tools"}
}

func TestMatchAssets(t *testing.T) {
	t.Parallel()
	assets := []models.Asset{
		{Name: "app_linux_amd64"},
		{Name: "app_linux_x86_64"},
		{Name: "app_linux_arm64"},
		{Name: "app_darwin_amd64"},
		{Name: "app_linux_386"},
		{Name: "checksums.txt"},
		// A per-asset sidecar carries host os/arch tokens but must never be
		// offered as an installable binary.
		{Name: "app_linux_amd64.sha256"},
	}
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   []string
	}{
		{"linux/amd64 incl x86_64 alias", "linux", "amd64", []string{"app_linux_amd64", "app_linux_x86_64"}},
		{"linux/arm64", "linux", "arm64", []string{"app_linux_arm64"}},
		{"linux/386 excludes x86_64", "linux", "386", []string{"app_linux_386"}},
		{"darwin/amd64", "darwin", "amd64", []string{"app_darwin_amd64"}},
		{"windows/amd64 none", "windows", "amd64", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := install.MatchAssets(assets, tc.goos, tc.goarch)
			var names []string
			for _, a := range got {
				names = append(names, a.Name)
			}
			if fmt.Sprint(names) != fmt.Sprint(tc.want) {
				t.Errorf("MatchAssets = %v, want %v", names, tc.want)
			}
		})
	}
}

func TestInstallVerifiedSuccess(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	sumFile := fmt.Sprintf("%s  microstore_linux_amd64\n", sha(binBytes))
	dl := fakeDL{files: map[string][]byte{assetURL: binBytes, sumURL: []byte(sumFile)}}
	rel, asset := release(true)

	got, err := install.New(dl, dest).Install(context.Background(), entry(), rel, asset, install.Options{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.SHA256 != sha(binBytes) {
		t.Errorf("SHA256 = %s, want %s", got.SHA256, sha(binBytes))
	}
	if got.Version != "v1.0.0" || got.Repo != "techthos/microstore" || got.DisplayName != "microstore" {
		t.Errorf("record = %+v", got)
	}
	wantPath := filepath.Join(dest, "microapp-microstore")
	if got.Path != wantPath {
		t.Errorf("Path = %s, want %s", got.Path, wantPath)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat placed binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

// TestInstallPlacedNamePrefix pins the focus contract: installed binaries land
// under InstallDir as "microapp-<repo bare name>", with any ".exe" preserved.
func TestInstallPlacedNamePrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		repo      string
		assetName string
		want      string
	}{
		{name: "posix binary", repo: "techthos/microstore", assetName: "microstore_linux_amd64", want: "microapp-microstore"},
		{name: "windows exe preserves suffix", repo: "acme/tool", assetName: "tool_windows_amd64.exe", want: "microapp-tool.exe"},
		{name: "repo without owner segment", repo: "solo", assetName: "solo_linux_arm64", want: "microapp-solo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dest := t.TempDir()
			const url = "https://example.test/asset"
			asset := models.Asset{Name: tc.assetName, DownloadURL: url, Size: int64(len(binBytes))}
			rel := models.Release{TagName: "v1.0.0", Assets: []models.Asset{asset}}
			dl := fakeDL{files: map[string][]byte{url: binBytes}}
			ent := models.ManifestEntry{Repo: tc.repo, DisplayName: tc.repo, Category: "tools"}

			// AllowUnverified isolates this test to placement, not checksums.
			got, err := install.New(dl, dest).Install(context.Background(), ent, rel, asset, install.Options{AllowUnverified: true})
			if err != nil {
				t.Fatalf("Install: %v", err)
			}
			wantPath := filepath.Join(dest, tc.want)
			if got.Path != wantPath {
				t.Errorf("Path = %s, want %s", got.Path, wantPath)
			}
			if _, err := os.Stat(wantPath); err != nil {
				t.Errorf("placed binary missing at %s: %v", wantPath, err)
			}
		})
	}
}

// TestInstallVerifiedSidecar covers the build-and-release workflow's artifact
// shape: a per-asset "<asset>.sha256" sidecar instead of an aggregated file.
func TestInstallVerifiedSidecar(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	const sideURL = "https://example.test/microstore_linux_amd64.sha256"
	asset := models.Asset{Name: "microstore_linux_amd64", DownloadURL: assetURL, Size: int64(len(binBytes))}
	rel := models.Release{TagName: "v1.4.0", Assets: []models.Asset{
		asset,
		{Name: "microstore_linux_amd64.sha256", DownloadURL: sideURL},
	}}
	sideFile := fmt.Sprintf("%s  microstore_linux_amd64\n", sha(binBytes))
	dl := fakeDL{files: map[string][]byte{assetURL: binBytes, sideURL: []byte(sideFile)}}

	got, err := install.New(dl, dest).Install(context.Background(), entry(), rel, asset, install.Options{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.SHA256 != sha(binBytes) {
		t.Errorf("SHA256 = %s, want %s", got.SHA256, sha(binBytes))
	}
}

func TestInstallChecksumMismatch(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	sumFile := "00deadbeef  microstore_linux_amd64\n"
	dl := fakeDL{files: map[string][]byte{assetURL: binBytes, sumURL: []byte(sumFile)}}
	rel, asset := release(true)

	_, err := install.New(dl, dest).Install(context.Background(), entry(), rel, asset, install.Options{})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	// No partial binary left behind.
	if _, statErr := os.Stat(filepath.Join(dest, "microstore")); !os.IsNotExist(statErr) {
		t.Errorf("binary should not exist after mismatch, stat err = %v", statErr)
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("install dir not clean after mismatch: %v", entries)
	}
}

func TestInstallNoChecksumsRefused(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	dl := fakeDL{files: map[string][]byte{assetURL: binBytes}}
	rel, asset := release(false)

	_, err := install.New(dl, dest).Install(context.Background(), entry(), rel, asset, install.Options{})
	if err == nil {
		t.Fatal("expected refusal when no checksums file present")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "microstore")); !os.IsNotExist(statErr) {
		t.Errorf("binary should not exist when refused")
	}
}

func TestInstallNoChecksumsAllowed(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	dl := fakeDL{files: map[string][]byte{assetURL: binBytes}}
	rel, asset := release(false)

	got, err := install.New(dl, dest).Install(context.Background(), entry(), rel, asset, install.Options{AllowUnverified: true})
	if err != nil {
		t.Fatalf("Install with AllowUnverified: %v", err)
	}
	if got.SHA256 != sha(binBytes) {
		t.Errorf("SHA256 = %s, want %s", got.SHA256, sha(binBytes))
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, binBytes, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	tests := []struct {
		name string
		path string
		want string
		st   install.VerifyStatus
	}{
		{"ok", path, sha(binBytes), install.VerifyOK},
		{"mismatch", path, "00deadbeef", install.VerifyMismatch},
		{"missing", filepath.Join(dir, "nope"), sha(binBytes), install.VerifyMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := install.Verify(tc.path, tc.want)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got != tc.st {
				t.Errorf("status = %s, want %s", got, tc.st)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, binBytes, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := install.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present: %v", err)
	}
	// Removing a missing file is not an error.
	if err := install.Remove(path); err != nil {
		t.Errorf("Remove(missing) = %v, want nil", err)
	}
}
