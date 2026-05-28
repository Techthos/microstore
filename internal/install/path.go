package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// This file is about making installed binaries *runnable*: microstore places
// them under a managed InstallDir, but they are only executable from a shell if
// that directory is on $PATH. These helpers detect that and produce the shell
// profile edit a user can apply — microstore advises and (on request) appends
// the line, but never silently rewrites PATH or symlinks system-wide.

// pathMarker tags the block microstore appends to a shell profile so the line is
// recognisable and AppendExport stays idempotent.
const pathMarker = "# Added by microstore — installed app binaries on PATH"

// OnPath reports whether dir is one of the entries in pathEnv, an
// os.PathListSeparator-separated PATH value. Comparison is on cleaned paths so
// trailing slashes and "." segments don't cause false negatives. An empty dir is
// never considered on PATH.
func OnPath(dir, pathEnv string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	want := filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		if p == "" {
			continue
		}
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// ExportLine returns the POSIX shell line that appends dir to PATH. bash and zsh
// share this syntax; ProfilePath only ever targets profiles for those shells.
func ExportLine(dir string) string {
	return fmt.Sprintf("export PATH=\"$PATH:%s\"", dir)
}

// ProfilePath resolves the shell rc file to advise editing, from the login shell
// ($SHELL) and the home directory: zsh → ~/.zshrc, everything else (bash and
// unknown shells) → ~/.bashrc. Only POSIX `export`-syntax shells are targeted;
// the fallback keeps ExportLine valid even for an unrecognised shell.
func ProfilePath(shell, home string) string {
	if strings.Contains(filepath.Base(shell), "zsh") {
		return filepath.Join(home, ".zshrc")
	}
	return filepath.Join(home, ".bashrc")
}

// AppendExport appends line to the shell profile at profilePath, creating the
// file if it does not exist. It is idempotent: if the profile already contains
// the exact line, the file is left untouched.
func AppendExport(profilePath, line string) (err error) {
	existing, err := os.ReadFile(profilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read profile %q: %w", profilePath, err)
	}
	if containsLine(existing, line) {
		return nil
	}
	f, err := os.OpenFile(profilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open profile %q: %w", profilePath, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close profile %q: %w", profilePath, cerr)
		}
	}()
	if _, werr := fmt.Fprintf(f, "\n%s\n%s\n", pathMarker, line); werr != nil {
		return fmt.Errorf("write profile %q: %w", profilePath, werr)
	}
	return nil
}

func containsLine(data []byte, line string) bool {
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
