package main

import (
	"os"
	"path/filepath"
	"strings"
)

// `volt ai chat on|off|status`: lets the IDE's Assistant panel chat with
// Claude Code — in a terminal or in Claude Desktop's Code tab, which uses the
// same user settings. On adds volt to Claude Code's MCP servers (~/.claude.json)
// and the `volt inbox-hook` wake hook (~/.claude/settings.json); off removes
// the hook. Nothing needs the `claude` command, so it works with Desktop alone.

var chatHookEvents = []string{"SessionStart", "Stop"}

func claudeSettingsFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func aiChat(action, volt string) aiResult {
	res := aiResult{Client: "claude-code"}
	file := claudeSettingsFile()
	switch action {
	case "status":
		res.OK = true
		if chatHookInstalled(file) {
			res.Message = "on"
		} else {
			res.Message = "off"
		}
		return res
	case "off":
		backup, err := editJSON(file, func(doc map[string]any) error { setChatHooks(doc, ""); return nil })
		if err != nil {
			res.Message = err.Error()
			return res
		}
		res.OK, res.Backup, res.Message = true, backup, "Volt messages are off in Claude"
		return res
	}
	home, _ := os.UserHomeDir()
	if _, err := writeServerJSON(filepath.Join(home, ".claude.json"), "mcpServers",
		map[string]any{"type": "stdio", "command": volt, "args": []string{"mcp"}}); err != nil {
		res.Message = err.Error()
		return res
	}
	cmd := volt
	if strings.ContainsAny(cmd, " \t") {
		cmd = `"` + cmd + `"`
	}
	backup, err := editJSON(file, func(doc map[string]any) error { setChatHooks(doc, cmd+" inbox-hook"); return nil })
	if err != nil {
		res.Message = err.Error()
		return res
	}
	res.OK, res.Backup = true, backup
	res.Message = "Claude will get your messages from the Volt panel"
	res.Next = "Open Claude (the Code tab, or Claude Code in a terminal) and send any message once to start the session."
	return res
}

// setChatHooks removes Volt's wake hook from every event and, unless command
// is "", adds it back with command. Other hooks are left alone.
func setChatHooks(doc map[string]any, command string) {
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		if command == "" {
			return
		}
		hooks = map[string]any{}
	}
	for _, ev := range chatHookEvents {
		var keep []any
		groups, _ := hooks[ev].([]any)
		for _, g := range groups {
			if !isChatHookGroup(g) {
				keep = append(keep, g)
			}
		}
		if command != "" {
			keep = append(keep, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": command, "async": true, "asyncRewake": true}}})
		}
		if len(keep) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = keep
		}
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		doc["hooks"] = hooks
	}
}

func isChatHookGroup(g any) bool {
	m, _ := g.(map[string]any)
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); strings.HasSuffix(strings.TrimSpace(c), " inbox-hook") {
			return true
		}
	}
	return false
}

func chatHookInstalled(file string) bool {
	data, err := os.ReadFile(file)
	return err == nil && strings.Contains(string(data), " inbox-hook")
}
