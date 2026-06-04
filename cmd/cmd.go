// Package cmd selects the run mode, owns process lifecycle (open the single
// bbolt store, wire dependencies), and dispatches to either the TUI or the MCP
// stdio server. main stays thin and calls Run.
//
// Modes: default (or "tui") launches the terminal UI; "serve"/"mcp" runs the MCP
// stdio server; "init" places the embedded Claude Code bootstrap kit (.claude)
// into the current directory and touches no database. A shared --db flag
// overrides the database location. Because bbolt takes a process-wide write
// lock, the modes are alternatives, not concurrent against one file.
package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"techthos.net/microstore/internal/app"
	"techthos.net/microstore/internal/db"
	"techthos.net/microstore/internal/github"
	"techthos.net/microstore/internal/server"
	"techthos.net/microstore/internal/tui"
)

const appName = "microstore"

// version is overridable at build time via -ldflags.
var version = "dev"

type options struct {
	dbPath string
	mode   string
}

// Run parses args, opens the store once, and dispatches to the selected mode.
func Run(args []string) error {
	opt, err := parseArgs(args)
	if err != nil {
		return err
	}
	switch opt.mode {
	case "", "tui", "serve", "mcp", "init":
	default:
		return fmt.Errorf("unknown mode %q (use \"\", \"tui\", \"serve\", \"mcp\", or \"init\")", opt.mode)
	}

	// init writes the bootstrap kit into the working directory and never
	// touches the database, so it dispatches before the store opens.
	if opt.mode == "init" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine working directory: %w", err)
		}
		return runInit(wd, os.Stdout)
	}

	if err := os.MkdirAll(filepath.Dir(opt.dbPath), 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	store, err := db.Open(opt.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	svc := app.New(github.New(), store)

	switch opt.mode {
	case "serve", "mcp":
		// stdout is the MCP protocol channel; never log to it here.
		return mcpserver.ServeStdio(server.New(svc, appName, version))
	default:
		return tui.New(svc).Run()
	}
}

// parseArgs accepts the mode either as the leading positional token
// (e.g. "serve --db x") or after the flags (e.g. "--db x serve").
func parseArgs(args []string) (options, error) {
	mode := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "path to the bbolt database file")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if mode == "" && fs.NArg() > 0 {
		mode = fs.Arg(0)
	}
	if *dbPath == "" {
		*dbPath = defaultDBPath()
	}
	return options{dbPath: *dbPath, mode: mode}, nil
}

func defaultDBPath() string {
	const rel = "microstore.db"
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".local", "share", appName, rel)
	}
	return filepath.Join(home, ".local", "share", appName, rel)
}
