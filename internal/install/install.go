package install

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"techthos.net/microstore/internal/models"
)

// Downloader streams the content at a URL into w. github.Client satisfies it.
type Downloader interface {
	Download(ctx context.Context, url string, w io.Writer) (int64, error)
}

// Installer downloads, verifies, and places release binaries under destDir.
type Installer struct {
	dl      Downloader
	destDir string
}

// New builds an Installer writing into destDir.
func New(dl Downloader, destDir string) *Installer {
	return &Installer{dl: dl, destDir: destDir}
}

// Options tunes a single install.
type Options struct {
	// AllowUnverified permits installing when the release has no checksums file
	// (or no entry for the chosen asset). Off by default — installs are refused.
	AllowUnverified bool
}

// Install downloads the chosen asset of rel, verifies its SHA-256 against the
// release's checksums file, places it executable under destDir, and returns the
// resulting record. On a checksum mismatch it writes no binary and no record.
// DisplayName/Category are carried from entry.
func (in *Installer) Install(ctx context.Context, entry models.ManifestEntry, rel models.Release, asset models.Asset, opts Options) (models.InstalledApp, error) {
	expected, err := in.expectedSum(ctx, rel, asset, opts)
	if err != nil {
		return models.InstalledApp{}, err
	}

	if err := os.MkdirAll(in.destDir, 0o755); err != nil {
		return models.InstalledApp{}, fmt.Errorf("create install dir %q: %w", in.destDir, err)
	}
	tmp, err := os.CreateTemp(in.destDir, ".microstore-*")
	if err != nil {
		return models.InstalledApp{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	n, err := in.dl.Download(ctx, asset.DownloadURL, io.MultiWriter(tmp, hasher))
	if err != nil {
		return models.InstalledApp{}, fmt.Errorf("download asset %q: %w", asset.Name, err)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if expected != "" && sum != expected {
		return models.InstalledApp{}, fmt.Errorf("checksum mismatch for %q: got %s, want %s", asset.Name, sum, expected)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return models.InstalledApp{}, fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return models.InstalledApp{}, fmt.Errorf("close temp file: %w", err)
	}
	dest := filepath.Join(in.destDir, placedName(entry, asset.Name))
	if err := os.Rename(tmpName, dest); err != nil {
		return models.InstalledApp{}, fmt.Errorf("place binary at %q: %w", dest, err)
	}
	committed = true

	return models.InstalledApp{
		Repo:        entry.Repo,
		DisplayName: entry.DisplayName,
		Category:    entry.Category,
		Bin:         entry.Bin,
		Version:     rel.TagName,
		AssetName:   asset.Name,
		Path:        dest,
		SHA256:      sum,
		Size:        n,
		InstalledAt: time.Now().UTC(),
		SourceURL:   asset.DownloadURL,
		MCP:         entry.MCP,
	}, nil
}

// expectedSum returns the expected hex SHA-256 for asset, or "" when unverified
// installs are explicitly allowed. It returns an error when verification is
// required but impossible.
func (in *Installer) expectedSum(ctx context.Context, rel models.Release, asset models.Asset, opts Options) (string, error) {
	src, ok := findChecksumSource(rel.Assets, asset.Name)
	if !ok {
		if opts.AllowUnverified {
			return "", nil
		}
		return "", fmt.Errorf("release %s has no checksums file; refusing install (allow_unverified to override)", rel.TagName)
	}
	var buf bytes.Buffer
	if _, err := in.dl.Download(ctx, src.DownloadURL, &buf); err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	if h := sumFor(buf.Bytes(), asset.Name); h != "" {
		return h, nil
	}
	if opts.AllowUnverified {
		return "", nil
	}
	return "", fmt.Errorf("no checksum entry for %q; refusing install (allow_unverified to override)", asset.Name)
}

// VerifyStatus is the outcome of re-verifying an installed binary.
type VerifyStatus string

const (
	VerifyOK       VerifyStatus = "ok"
	VerifyMismatch VerifyStatus = "mismatch"
	VerifyMissing  VerifyStatus = "missing"
)

// Verify recomputes the SHA-256 of the file at path and compares it to wantSHA.
func Verify(path, wantSHA string) (VerifyStatus, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return VerifyMissing, nil
		}
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	if hex.EncodeToString(h.Sum(nil)) == strings.ToLower(strings.TrimSpace(wantSHA)) {
		return VerifyOK, nil
	}
	return VerifyMismatch, nil
}

// Remove deletes the binary at path; a missing file is not an error.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}

func isChecksumsName(lowerName string) bool {
	switch lowerName {
	case "checksums.txt", "sha256sums", "sha256sums.txt", "checksums":
		return true
	}
	return false
}

// isChecksumSidecar reports whether name is a per-asset checksum sidecar such as
// "app-v1-linux-amd64.sha256" — the form `sha256sum bin > bin.sha256` produces,
// which is exactly what the build-and-release workflow uploads.
func isChecksumSidecar(lowerName string) bool {
	return strings.HasSuffix(lowerName, ".sha256") || strings.HasSuffix(lowerName, ".sha256sum")
}

// findChecksumSource returns the asset carrying the expected SHA-256 for
// assetName: a per-asset sidecar "<assetName>.sha256" when present, otherwise an
// aggregated checksums file (goreleaser-style).
func findChecksumSource(assets []models.Asset, assetName string) (models.Asset, bool) {
	if side, ok := findSidecarAsset(assets, assetName); ok {
		return side, true
	}
	return findChecksumsAsset(assets)
}

func findSidecarAsset(assets []models.Asset, assetName string) (models.Asset, bool) {
	want := strings.ToLower(assetName) + ".sha256"
	for _, a := range assets {
		if strings.ToLower(a.Name) == want {
			return a, true
		}
	}
	return models.Asset{}, false
}

func findChecksumsAsset(assets []models.Asset) (models.Asset, bool) {
	for _, a := range assets {
		if isChecksumsName(strings.ToLower(a.Name)) {
			return a, true
		}
	}
	return models.Asset{}, false
}

// sumFor extracts the hex SHA-256 for assetName from sha256sum-format data
// ("<hex>  <name>" lines). A single-asset sidecar may record a different inner
// name (e.g. a path), so fall back to its sole entry when there is exactly one.
func sumFor(data []byte, assetName string) string {
	sums := parseChecksums(data)
	if h, ok := sums[assetName]; ok {
		return h
	}
	if len(sums) == 1 {
		for _, h := range sums {
			return h
		}
	}
	return ""
}

// parseChecksums parses "<hex>  <filename>" lines (filename may carry a leading
// '*' for binary mode). Keys are the bare filenames, values lower-cased hex.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

// placedName is the filename an installed binary takes under InstallDir: the
// manifest entry's Bin override when set, otherwise the repo's bare name (the
// segment after "owner/"), prefixed with "microapp-" either way, so every
// installed micro-app is recognisable and grouped on a shared PATH. A ".exe"
// suffix is preserved for Windows assets.
func placedName(entry models.ManifestEntry, assetName string) string {
	name := entry.Bin
	if name == "" {
		name = entry.Repo
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	name = "microapp-" + name
	if strings.HasSuffix(strings.ToLower(assetName), ".exe") && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	return name
}
