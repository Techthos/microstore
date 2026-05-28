package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"techthos.net/microstore/internal/install"
)

func TestOnPath(t *testing.T) {
	t.Parallel()
	const sep = string(os.PathListSeparator)
	dir := filepath.Join("home", "u", ".local", "share", "microstore", "bin")
	tests := []struct {
		name string
		dir  string
		path string
		want bool
	}{
		{name: "present", dir: dir, path: "/usr/bin" + sep + dir, want: true},
		{name: "present with trailing slash", dir: dir, path: dir + "/" + sep + "/usr/bin", want: true},
		{name: "absent", dir: dir, path: "/usr/bin" + sep + "/usr/local/bin", want: false},
		{name: "empty dir", dir: "", path: "/usr/bin", want: false},
		{name: "empty path", dir: dir, path: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := install.OnPath(tc.dir, tc.path); got != tc.want {
				t.Errorf("OnPath(%q, %q) = %v, want %v", tc.dir, tc.path, got, tc.want)
			}
		})
	}
}

func TestExportLine(t *testing.T) {
	t.Parallel()
	got := install.ExportLine("/home/u/bin")
	if want := `export PATH="$PATH:/home/u/bin"`; got != want {
		t.Errorf("ExportLine = %q, want %q", got, want)
	}
}

func TestProfilePath(t *testing.T) {
	t.Parallel()
	home := "/home/u"
	tests := []struct {
		name  string
		shell string
		want  string
	}{
		{name: "zsh", shell: "/bin/zsh", want: filepath.Join(home, ".zshrc")},
		{name: "bash", shell: "/bin/bash", want: filepath.Join(home, ".bashrc")},
		{name: "unknown falls back to bashrc", shell: "/usr/bin/fish", want: filepath.Join(home, ".bashrc")},
		{name: "empty falls back to bashrc", shell: "", want: filepath.Join(home, ".bashrc")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := install.ProfilePath(tc.shell, home); got != tc.want {
				t.Errorf("ProfilePath(%q) = %q, want %q", tc.shell, got, tc.want)
			}
		})
	}
}

func TestAppendExport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profile := filepath.Join(dir, ".bashrc")
	line := install.ExportLine(filepath.Join(dir, "bin"))

	// Creates the profile and writes the line.
	if err := install.AppendExport(profile, line); err != nil {
		t.Fatalf("AppendExport (create): %v", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !strings.Contains(string(data), line) {
		t.Fatalf("profile missing export line:\n%s", data)
	}

	// Idempotent: a second append leaves the file unchanged.
	if err := install.AppendExport(profile, line); err != nil {
		t.Fatalf("AppendExport (idempotent): %v", err)
	}
	data2, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("re-read profile: %v", err)
	}
	if string(data) != string(data2) {
		t.Errorf("second AppendExport changed the profile:\nbefore:\n%s\nafter:\n%s", data, data2)
	}
	if n := strings.Count(string(data2), line); n != 1 {
		t.Errorf("export line present %d times, want 1", n)
	}
}

func TestAppendExportPreservesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profile := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(profile, []byte("alias ll='ls -l'\n"), 0o644); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	line := install.ExportLine("/opt/bin")
	if err := install.AppendExport(profile, line); err != nil {
		t.Fatalf("AppendExport: %v", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !strings.Contains(string(data), "alias ll='ls -l'") {
		t.Errorf("existing content was clobbered:\n%s", data)
	}
	if !strings.Contains(string(data), line) {
		t.Errorf("export line not appended:\n%s", data)
	}
}
