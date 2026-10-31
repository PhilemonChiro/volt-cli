package main

import (
	"os"
	"path/filepath"
	"strings"
)

// removeInboxHook runs when Claude Code still calls the `volt inbox-hook`
// wake hook that volt 0.2.0–0.2.x installed in ~/.claude/settings.json. The
// hook is gone, so it deletes its own entries (other hooks stay) and exits 0
// so Claude Code reports no hook error.
func removeInboxHook() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	file := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), " inbox-hook") {
		return 0
	}
	editJSON(file, func(doc map[string]any) error {
		hooks, _ := doc["hooks"].(map[string]any)
		for _, ev := range []string{"SessionStart", "Stop"} {
			var keep []any
			groups, _ := hooks[ev].([]any)
			for _, g := range groups {
				if !isInboxHookGroup(g) {
					keep = append(keep, g)
				}
			}
			if len(keep) == 0 {
				delete(hooks, ev)
			} else {
				hooks[ev] = keep
			}
		}
		if hooks != nil && len(hooks) == 0 {
			delete(doc, "hooks")
		}
		return nil
	})
	return 0
}

func isInboxHookGroup(g any) bool {
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
