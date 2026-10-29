package main

import (
	"os"
	"path/filepath"

	"github.com/PhilemonChiro/volt-compiler/harness"
)

// agentFiles are what `volt init` writes: agent rules and the MCP configs
// that make Claude Code, Cursor and VS Code start `volt mcp` in the project.
func agentFiles() map[string]string {
	rules := harness.AgentRules()
	return map[string]string{
		"AGENTS.md":              rules,
		"CLAUDE.md":              rules,
		".cursor/rules/volt.mdc": "---\ndescription: Writing Volt firmware\nglobs: **/*.volt\nalwaysApply: true\n---\n\n" + rules,
		".mcp.json":              `{"mcpServers": {"volt": {"command": "volt", "args": ["mcp"]}}}` + "\n",
		".cursor/mcp.json":       `{"mcpServers": {"volt": {"command": "volt", "args": ["mcp"]}}}` + "\n",
		".vscode/mcp.json":       `{"servers": {"volt": {"type": "stdio", "command": "volt", "args": ["mcp"]}}}` + "\n",
	}
}

// runInit sets a project up for AI coding agents. Existing files are kept
// unless --force.
func runInit(args []string) int {
	fl, _ := newFlags("init", "[dir] [--force]")
	force := fl.Bool("force", false, "overwrite existing files")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	dir := first(pos)
	if dir == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, "volt.toml")); err != nil {
		stderr.Printf("[yellow]volt init: no volt.toml in %s — is this a Volt project?[/yellow]\n", dir)
	}
	files := agentFiles()
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".cursor/rules/volt.mdc", ".mcp.json", ".cursor/mcp.json", ".vscode/mcp.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil && !*force {
			stderr.Printf("  [dim]kept    %s (exists; --force to overwrite)[/dim]\n", name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			stderr.Printf("[red]volt init: %s[/red]\n", err)
			return 1
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			stderr.Printf("[red]volt init: %s[/red]\n", err)
			return 1
		}
		stderr.Printf("  [green]wrote[/green]   %s\n", name)
	}
	return 0
}
