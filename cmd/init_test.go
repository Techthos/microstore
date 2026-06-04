package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"techthos.net/microstore/templates"
)

func TestRunInit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out strings.Builder

	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Every file of the embedded kit must land on disk, byte for byte.
	kit := templates.ClaudeCode()
	files := 0
	err := fs.WalkDir(kit, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		want, err := fs.ReadFile(kit, path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			t.Errorf("placed %s differs from embedded kit", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("compare kit: %v", err)
	}
	if files == 0 {
		t.Fatal("embedded kit is empty")
	}

	// The kit places only the .claude tree, under the target directory.
	for _, sub := range []string{".claude/commands", ".claude/rules", ".claude/skills"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(sub))); err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
		}
	}

	// The closing message explains the phases in order.
	msg := out.String()
	for _, phrase := range []string{"/product-idea", "/app-init", "/app-spec-sync", "build-and-release"} {
		if !strings.Contains(msg, phrase) {
			t.Errorf("init output should mention %q\noutput:\n%s", phrase, msg)
		}
	}
	if i1, i2, i3 := strings.Index(msg, "/product-idea"), strings.Index(msg, "/app-init"), strings.Index(msg, "/app-spec-sync"); i1 >= i2 || i2 >= i3 {
		t.Errorf("phases should be listed in order product-idea → app-init → app-spec-sync\noutput:\n%s", msg)
	}
}

func TestRunInitRefusesExistingSetup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var out strings.Builder
	err := runInit(dir, &out)
	if err == nil || !strings.Contains(err.Error(), ".claude already exists") {
		t.Fatalf("err = %v, want refusal mentioning existing .claude", err)
	}
	if out.Len() != 0 {
		t.Errorf("no success output expected on refusal, got:\n%s", out.String())
	}
}

func TestParseArgsInitMode(t *testing.T) {
	t.Parallel()
	opt, err := parseArgs([]string{"init"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opt.mode != "init" {
		t.Errorf("mode = %q, want %q", opt.mode, "init")
	}
}
