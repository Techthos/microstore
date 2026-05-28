// Package install resolves and installs release binaries: it matches a release
// asset to the host GOOS/GOARCH, downloads it, verifies its SHA-256 against the
// release's checksums file, and places it (executable) in a managed directory.
// It also re-verifies and removes installed binaries. It is a service consumed
// by both server and tui; it never touches bbolt.
package install

import (
	"runtime"
	"strings"

	"techthos.net/microstore/internal/models"
)

// HostOS / HostArch report the running host's GOOS / GOARCH.
func HostOS() string   { return runtime.GOOS }
func HostArch() string { return runtime.GOARCH }

type aliasGroup struct {
	canonical string
	tokens    []string
}

// Alias groups are ordered most-specific first so an asset declaring "x86_64"
// (amd64) is never mis-detected as "x86" (386), and "arm64" is never read as "arm".
var (
	osAliases = []aliasGroup{
		{"darwin", []string{"darwin", "macos", "osx"}},
		{"windows", []string{"windows", "win"}},
		{"linux", []string{"linux"}},
		{"freebsd", []string{"freebsd"}},
		{"openbsd", []string{"openbsd"}},
		{"netbsd", []string{"netbsd"}},
	}
	archAliases = []aliasGroup{
		{"arm64", []string{"aarch64", "arm64"}},
		{"amd64", []string{"x86_64", "amd64", "x64"}},
		{"386", []string{"i386", "x86", "386"}},
		{"arm", []string{"armv7", "armv6", "arm"}},
		{"riscv64", []string{"riscv64"}},
	}
)

// MatchAssets returns the assets whose names declare the given host OS and arch.
// Checksums files — aggregated ("checksums.txt") and per-asset sidecars
// ("<asset>.sha256") alike — are skipped, so a sidecar carrying the host's
// os/arch tokens is never mistaken for an installable binary. Zero matches means
// manual selection is needed; more than one means the match is ambiguous.
func MatchAssets(assets []models.Asset, goos, goarch string) []models.Asset {
	var out []models.Asset
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if isChecksumsName(name) || isChecksumSidecar(name) {
			continue
		}
		if detect(osAliases, name) == goos && detect(archAliases, name) == goarch {
			out = append(out, a)
		}
	}
	return out
}

func detect(groups []aliasGroup, name string) string {
	for _, g := range groups {
		for _, tok := range g.tokens {
			if hasToken(name, tok) {
				return g.canonical
			}
		}
	}
	return ""
}

// hasToken reports whether tok appears in s delimited by non-alphanumeric
// boundaries (so "x86" does not match inside "x86_64", and "win" does not match
// inside "windows"). s is assumed lower-cased.
func hasToken(s, tok string) bool {
	for i := 0; i+len(tok) <= len(s); {
		idx := strings.Index(s[i:], tok)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(tok)
		beforeOK := start == 0 || !isAlnum(s[start-1])
		afterOK := end == len(s) || !isAlnum(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
	return false
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
