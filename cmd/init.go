package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"techthos.net/microstore/templates"
)

// runInit places the embedded Claude Code bootstrap kit (the .claude
// directory) into dir, then prints how the spec-first phases are used. It
// refuses to touch a directory that already has a .claude entry so an existing
// setup is never overwritten.
func runInit(dir string, out io.Writer) error {
	kit := templates.ClaudeCode()

	if _, err := os.Lstat(filepath.Join(dir, ".claude")); err == nil {
		return errors.New(".claude already exists here — remove it (or move it aside) and rerun \"microstore init\"")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", filepath.Join(dir, ".claude"), err)
	}

	if err := os.CopyFS(dir, kit); err != nil {
		return fmt.Errorf("place Claude Code setup: %w", err)
	}

	files := 0
	if err := fs.WalkDir(kit, ".", func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return err
	}); err != nil {
		return fmt.Errorf("count placed files: %w", err)
	}

	_, err := fmt.Fprintf(out, `Initialized Claude Code micro-app setup in .claude/ (%d files).

The setup drives spec-first development in three phases:

  1. /product-idea           turn your idea into docs/SPECIFICATIONS.md — the contract
  2. /app-init <module-path> scaffold the Go codebase against that spec
  3. /app-spec-sync          audit and reconcile code vs. spec as the app evolves

Layer rules under .claude/rules/ load automatically while you edit matching
paths, and the build-and-release skill generates a tag-triggered
cross-platform release workflow when you are ready to ship.

Open this directory with Claude Code and start with /product-idea.
`, files)
	return err
}
