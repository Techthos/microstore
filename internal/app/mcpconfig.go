package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"techthos.net/microstore/internal/db"
	"techthos.net/microstore/internal/models"
)

// ErrNoMCPSupport is returned by ConfigureMCP when the installed app advertises
// no MCP server, so callers can present a clear "nothing to wire up" message
// rather than treating it as a failure.
var ErrNoMCPSupport = errors.New("app advertises no MCP server")

// mcpConfigFile is the basename of the project-local MCP server config that
// ConfigureMCP creates or edits, matching the convention LLM clients read.
const mcpConfigFile = ".mcp.json"

// MCPConfigResult reports what ConfigureMCP did to a project's .mcp.json.
type MCPConfigResult struct {
	Path    string `json:"path"`    // absolute path to the .mcp.json written
	Server  string `json:"server"`  // server key added or updated under mcpServers
	Created bool   `json:"created"` // the file did not exist and was created
	Updated bool   `json:"updated"` // an existing server entry was replaced
}

// ConfigureMCP wires a tracked install into the .mcp.json under dir (its current
// working directory when empty), creating the file or editing it in place. It
// adds — or replaces — a single server entry built from the install's recorded
// MCP launch info, leaving every other server entry and top-level key untouched.
// It returns ErrNoMCPSupport when the app carries no MCP launch info.
func (s *Service) ConfigureMCP(repo, dir string) (MCPConfigResult, error) {
	existing, err := s.installs.Get(repo)
	if errors.Is(err, db.ErrNotFound) {
		return MCPConfigResult{}, fmt.Errorf("%s is not installed", repo)
	}
	if err != nil {
		return MCPConfigResult{}, err
	}
	if existing.MCP == nil || existing.MCP.Command == "" {
		return MCPConfigResult{}, fmt.Errorf("%s: %w", repo, ErrNoMCPSupport)
	}

	if dir == "" {
		dir = "."
	}
	path, err := filepath.Abs(filepath.Join(dir, mcpConfigFile))
	if err != nil {
		return MCPConfigResult{}, fmt.Errorf("resolve %s path: %w", mcpConfigFile, err)
	}

	doc, created, err := readMCPConfig(path)
	if err != nil {
		return MCPConfigResult{}, err
	}
	server := mcpServerKey(*existing)
	_, updated := doc.servers[server]
	doc.servers[server] = models.MCPLaunch{Command: existing.MCP.Command, Args: existing.MCP.Args}

	if err := writeMCPConfig(path, doc); err != nil {
		return MCPConfigResult{}, err
	}
	return MCPConfigResult{Path: path, Server: server, Created: created, Updated: updated && !created}, nil
}

// mcpServerKey derives a stable, simple server name for the .mcp.json entry from
// the install's display name (falling back to the repo's bare name), lowercased
// with separators collapsed to hyphens so it reads as a clean identifier.
func mcpServerKey(ia models.InstalledApp) string {
	name := ia.DisplayName
	if name == "" {
		name = ia.Repo
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "app"
	}
	return name
}

// mcpDoc is a parsed .mcp.json: the mcpServers map we edit, plus every other
// top-level key preserved verbatim so editing never drops unrelated config.
type mcpDoc struct {
	servers map[string]models.MCPLaunch
	extras  map[string]json.RawMessage
}

// readMCPConfig loads path into an mcpDoc, reporting whether the file was absent
// (so the caller knows it is creating versus editing). A missing file yields an
// empty doc; a present-but-malformed file is a hard error rather than silent
// overwrite, so we never clobber a user's hand-written config.
func readMCPConfig(path string) (mcpDoc, bool, error) {
	doc := mcpDoc{servers: map[string]models.MCPLaunch{}, extras: map[string]json.RawMessage{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, true, nil
	}
	if err != nil {
		return mcpDoc{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return doc, false, nil
	}
	if err := json.Unmarshal(raw, &doc.extras); err != nil {
		return mcpDoc{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if servers, ok := doc.extras["mcpServers"]; ok {
		if err := json.Unmarshal(servers, &doc.servers); err != nil {
			return mcpDoc{}, false, fmt.Errorf("parse mcpServers in %s: %w", path, err)
		}
		delete(doc.extras, "mcpServers")
	}
	return doc, false, nil
}

// writeMCPConfig serialises doc back to path with two-space indentation and a
// trailing newline, re-attaching mcpServers alongside the preserved extras.
func writeMCPConfig(path string, doc mcpDoc) error {
	out := make(map[string]json.RawMessage, len(doc.extras)+1)
	for k, v := range doc.extras {
		out[k] = v
	}
	servers, err := json.Marshal(doc.servers)
	if err != nil {
		return fmt.Errorf("encode mcpServers: %w", err)
	}
	out["mcpServers"] = servers

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
