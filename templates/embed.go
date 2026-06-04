// Package templates embeds the project templates that the microstore binary
// can place on disk. The canonical Claude Code bootstrap kit lives under
// templates/claude-code/ in this repo; `microstore init` copies its contents
// (the .claude directory) into the current working directory.
package templates

import (
	"embed"
	"io/fs"
)

// The all: prefix is required so files and directories whose names start with
// a dot (the .claude tree itself) are embedded.
//
//go:embed all:claude-code
var claudeCode embed.FS

// ClaudeCode returns the Claude Code bootstrap kit as a filesystem rooted at
// the template's top level, so copying it into a directory yields `.claude/…`.
func ClaudeCode() fs.FS {
	sub, err := fs.Sub(claudeCode, "claude-code")
	if err != nil {
		// The subdirectory is embedded at compile time; failure here is a
		// programmer error, not a runtime condition.
		panic(err)
	}
	return sub
}
