package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// repoArg is the shared "repo" required string parameter.
func repoArg() mcp.ToolOption {
	return mcp.WithString("repo", mcp.Required(), mcp.Description("Repository slug in owner/name form"))
}

func (h *handler) registerTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("get_config",
		mcp.WithDescription("Return the current store configuration (manifest URL and install directory).")),
		h.getConfig)

	s.AddTool(mcp.NewTool("set_config",
		mcp.WithDescription("Update the store configuration. Empty fields are left unchanged."),
		mcp.WithString("manifest_url", mcp.Description("Raw JSON URL of the catalog manifest")),
		mcp.WithString("install_dir", mcp.Description("Directory where installed binaries are placed"))),
		mcp.NewTypedToolHandler(h.setConfig))

	s.AddTool(mcp.NewTool("list_catalog",
		mcp.WithDescription("List the catalog's app entries, fetched live from the configured manifest.")),
		h.listCatalog)

	s.AddTool(mcp.NewTool("search_apps",
		mcp.WithDescription("Filter the catalog by free-text (name/repo) and/or category."),
		mcp.WithString("query", mcp.Description("Free-text filter on display name or repo (case-insensitive)")),
		mcp.WithString("category", mcp.Description("Exact category filter"))),
		mcp.NewTypedToolHandler(h.searchApps))

	s.AddTool(mcp.NewTool("app_details",
		mcp.WithDescription("Repository info, the latest non-prerelease release with its assets, and current install state."),
		repoArg()),
		mcp.NewTypedToolHandler(h.appDetails))

	s.AddTool(mcp.NewTool("list_releases",
		mcp.WithDescription("All releases for a repo, newest-first."),
		repoArg()),
		mcp.NewTypedToolHandler(h.listReleases))

	s.AddTool(mcp.NewTool("list_installed",
		mcp.WithDescription("List tracked installs, alphabetical by slug.")),
		h.listInstalled)

	s.AddTool(mcp.NewTool("install_app",
		mcp.WithDescription("Resolve the release, match the host arch, verify SHA-256, download and record the install. On zero or ambiguous asset match the error lists the available assets."),
		repoArg(),
		mcp.WithString("version", mcp.Description("Release tag to install; defaults to the latest non-prerelease")),
		mcp.WithString("asset", mcp.Description("Explicit asset name; defaults to host GOOS/GOARCH auto-match")),
		mcp.WithBoolean("allow_unverified", mcp.DefaultBool(false), mcp.Description("Install even when the release has no checksums file"))),
		mcp.NewTypedToolHandler(h.installApp))

	s.AddTool(mcp.NewTool("update_app",
		mcp.WithDescription("Upgrade a tracked install to the latest release, or report a no-op when current."),
		repoArg()),
		mcp.NewTypedToolHandler(h.updateApp))

	s.AddTool(mcp.NewTool("uninstall_app",
		mcp.WithDescription("Remove a tracked install's binary and record."),
		repoArg()),
		mcp.NewTypedToolHandler(h.uninstallApp))

	s.AddTool(mcp.NewTool("verify_app",
		mcp.WithDescription("Re-verify a tracked install's on-disk SHA-256 (ok | mismatch | missing)."),
		repoArg()),
		mcp.NewTypedToolHandler(h.verifyApp))

	s.AddTool(mcp.NewTool("configure_mcp",
		mcp.WithDescription("Add a tracked install's MCP server to a project's .mcp.json (created or edited in place), leaving other server entries untouched. Errors when the app advertises no MCP server."),
		repoArg(),
		mcp.WithString("dir", mcp.Description("Directory whose .mcp.json to write; defaults to the current working directory"))),
		mcp.NewTypedToolHandler(h.configureMCP))

	s.AddTool(mcp.NewTool("list_templates",
		mcp.WithDescription("List the manifest's templates, fetched live.")),
		h.listTemplates)

	s.AddTool(mcp.NewTool("scaffold_app",
		mcp.WithDescription("Extract a template into a target directory and return the next step (run /product-idea)."),
		mcp.WithString("template_repo", mcp.Required(), mcp.Description("Template repository slug in owner/name form")),
		mcp.WithString("target_dir", mcp.Required(), mcp.Description("Directory to scaffold the new app into")),
		mcp.WithString("ref", mcp.Description("Branch or tag; defaults to the catalog template's ref")),
		mcp.WithBoolean("force", mcp.DefaultBool(false), mcp.Description("Allow extracting into a non-empty directory"))),
		mcp.NewTypedToolHandler(h.scaffoldApp))
}

func (h *handler) getConfig(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := h.app.GetConfig()
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(configOutput{Config: cfg})
}

func (h *handler) setConfig(_ context.Context, _ mcp.CallToolRequest, in configInput) (*mcp.CallToolResult, error) {
	cfg, err := h.app.MergeConfig(in.ManifestURL, in.InstallDir)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(configOutput{Config: cfg})
}

func (h *handler) listCatalog(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	apps, err := h.app.ListCatalog(ctx)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(catalogOutput{Apps: nz(apps)})
}

func (h *handler) searchApps(ctx context.Context, _ mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, error) {
	apps, err := h.app.SearchApps(ctx, in.Query, in.Category)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(catalogOutput{Apps: nz(apps)})
}

func (h *handler) appDetails(ctx context.Context, _ mcp.CallToolRequest, in repoInput) (*mcp.CallToolResult, error) {
	d, err := h.app.AppDetails(ctx, in.Repo)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(d)
}

func (h *handler) listReleases(ctx context.Context, _ mcp.CallToolRequest, in repoInput) (*mcp.CallToolResult, error) {
	rels, err := h.app.ListReleases(ctx, in.Repo)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(releasesOutput{Releases: nz(rels)})
}

func (h *handler) listInstalled(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list, err := h.app.ListInstalled()
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(installedListOutput{Installed: nz(list)})
}

func (h *handler) installApp(ctx context.Context, _ mcp.CallToolRequest, in installInput) (*mcp.CallToolResult, error) {
	rec, err := h.app.Install(ctx, in.Repo, in.Version, in.Asset, in.AllowUnverified)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(installOutput{Installed: rec})
}

func (h *handler) updateApp(ctx context.Context, _ mcp.CallToolRequest, in repoInput) (*mcp.CallToolResult, error) {
	res, err := h.app.Update(ctx, in.Repo)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(res)
}

func (h *handler) uninstallApp(_ context.Context, _ mcp.CallToolRequest, in repoInput) (*mcp.CallToolResult, error) {
	if err := h.app.Uninstall(in.Repo); err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(removedOutput{Removed: true})
}

func (h *handler) verifyApp(_ context.Context, _ mcp.CallToolRequest, in repoInput) (*mcp.CallToolResult, error) {
	st, err := h.app.Verify(in.Repo)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(verifyOutput{Status: string(st)})
}

func (h *handler) configureMCP(_ context.Context, _ mcp.CallToolRequest, in configureMCPInput) (*mcp.CallToolResult, error) {
	res, err := h.app.ConfigureMCP(in.Repo, in.Dir)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(configureMCPOutput{Result: res})
}

func (h *handler) listTemplates(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tmpls, err := h.app.ListTemplates(ctx)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(templatesOutput{Templates: nz(tmpls)})
}

func (h *handler) scaffoldApp(ctx context.Context, _ mcp.CallToolRequest, in scaffoldInput) (*mcp.CallToolResult, error) {
	res, err := h.app.Scaffold(ctx, in.TemplateRepo, in.TargetDir, in.Ref, in.Force)
	if err != nil {
		return toolErr(err)
	}
	return mcp.NewToolResultJSON(res)
}
