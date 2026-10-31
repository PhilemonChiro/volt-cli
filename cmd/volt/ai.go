package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// `volt ai` connects AI apps to Volt: it finds the apps on this computer and
// registers `volt mcp` (this binary, by absolute path) in each one, the way
// that app prefers — an install link the app confirms itself, the app's own
// `mcp add` command, or an edit of its settings file (with a backup).

type aiClient struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	Configured bool   `json:"configured"`      // volt is registered
	Stale      bool   `json:"stale,omitempty"` // registered, but with another volt binary
	Method     string `json:"method"`          // link | command | file
	Config     string `json:"config,omitempty"`

	bin     string        // command: the app's CLI
	app     string        // macOS app name, for detection and restart
	key     string        // file: top-level key that holds servers
	current func() string // registered volt command, "" if none
	setup   func() (string, error)
}

type aiResult struct {
	OK      bool   `json:"ok"`
	Client  string `json:"client"`
	Message string `json:"message"`
	Next    string `json:"next,omitempty"`
	Backup  string `json:"backup,omitempty"`
	Restart bool   `json:"restart,omitempty"` // the app must be restarted to pick volt up
}

func runAI(args []string) int {
	fl, _ := newFlags("ai", "list|setup <app>|restart <app> [--json]")
	asJSON := fl.Bool("json", false, "machine-readable output")
	pos, ok := parse(fl, args)
	if !ok || len(pos) == 0 {
		fl.Usage()
		return 2
	}
	volt, err := voltExecutable()
	if err != nil {
		stderr.Printf("[red]volt ai: %s[/red]\n", err)
		return 1
	}
	clients := aiClients(volt)
	find := func() *aiClient {
		if len(pos) < 2 {
			return nil
		}
		for _, c := range clients {
			if c.ID == pos[1] {
				return c
			}
		}
		return nil
	}
	emit := func(v any) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		enc.Encode(v)
	}

	switch pos[0] {
	case "list":
		if *asJSON {
			emit(map[string]any{"volt": volt, "clients": clients})
			return 0
		}
		for _, c := range clients {
			state := "not installed"
			switch {
			case c.Stale:
				state = "connected to another volt — run setup again"
			case c.Configured:
				state = "connected"
			case c.Installed:
				state = "installed"
			}
			fmt.Printf("  %-15s %-15s %s\n", c.ID, c.Name, state)
		}
		return 0

	case "setup", "restart":
		c := find()
		if c == nil {
			ids := make([]string, len(clients))
			for i, c := range clients {
				ids[i] = c.ID
			}
			stderr.Printf("[red]volt ai %s: name one of: %s[/red]\n", pos[0], strings.Join(ids, ", "))
			return 2
		}
		var res aiResult
		if pos[0] == "setup" {
			res = aiSetup(c)
		} else {
			res = aiRestart(c)
		}
		if *asJSON {
			emit(res)
		} else if res.OK {
			stdout.Printf("[green]✓[/green] %s\n", res.Message)
			if res.Next != "" {
				fmt.Println("  " + res.Next)
			}
		} else {
			stderr.Printf("[red]✗ %s[/red]\n", res.Message)
		}
		if !res.OK {
			return 1
		}
		return 0
	}
	fl.Usage()
	return 2
}

// voltExecutable is the absolute path AI apps should start: this binary.
func voltExecutable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

func aiSetup(c *aiClient) aiResult {
	res := aiResult{Client: c.ID}
	next, err := c.setup()
	if err != nil {
		res.Message = err.Error()
		return res
	}
	res.OK = true
	switch c.Method {
	case "link":
		res.Message = c.Name + " opened"
		res.Next = "Click Install in " + c.Name + "'s dialog."
	case "command":
		res.Message = "Volt added to " + c.Name
		res.Next = "Start a new " + c.Name + " session in your project folder."
	case "file":
		res.Message = "Volt added to " + c.Name + " (" + c.Config + ")"
		res.Backup = next
		res.Restart = true
		res.Next = "Restart " + c.Name + " so it loads Volt."
	}
	return res
}

func aiRestart(c *aiClient) aiResult {
	res := aiResult{Client: c.ID}
	if runtime.GOOS != "darwin" || c.app == "" {
		res.Message = "Restart " + c.Name + " yourself: quit it fully, then open it again."
		return res
	}
	running := func() bool { return exec.Command("pgrep", "-x", c.app).Run() == nil }
	waitQuit := func() bool {
		for i := 0; i < 40 && running(); i++ {
			time.Sleep(250 * time.Millisecond)
		}
		return !running()
	}
	// A polite quit first; macOS may refuse it (Automation permission), so
	// fall back to SIGTERM, which apps treat as a normal quit.
	exec.Command("osascript", "-e", fmt.Sprintf("quit app %q", c.app)).Run()
	if !waitQuit() {
		exec.Command("pkill", "-TERM", "-x", c.app).Run()
		if !waitQuit() {
			res.Message = "Couldn't quit " + c.Name + ". Quit it yourself (" + c.app + " menu → Quit " + c.app + "), then open it again."
			return res
		}
	}
	if err := exec.Command("open", "-a", c.app).Run(); err != nil {
		res.Message = "Couldn't reopen " + c.Name + ": " + err.Error()
		return res
	}
	res.OK = true
	res.Message = c.Name + " restarted"
	return res
}

func aiClients(volt string) []*aiClient {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")       // Windows roaming
	localApp := os.Getenv("LOCALAPPDATA") // Windows local
	userConfig := func(mac, win, linux string) string {
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library", "Application Support", mac)
		case "windows":
			return filepath.Join(appData, win)
		}
		return filepath.Join(home, ".config", linux)
	}
	serverJSON := func(withName bool) []byte {
		m := map[string]any{"command": volt, "args": []string{"mcp"}}
		if withName {
			m["name"] = "volt"
		}
		b, _ := json.Marshal(m)
		return b
	}

	cursorCfg := filepath.Join(home, ".cursor", "mcp.json")
	vscodeCfg := filepath.Join(userConfig("Code", "Code", "Code"), "User", "mcp.json")
	desktopCfg := filepath.Join(userConfig("Claude", "Claude", "Claude"), "claude_desktop_config.json")
	windsurfCfg := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	geminiCfg := filepath.Join(home, ".gemini", "settings.json")
	codexCfg := filepath.Join(home, ".codex", "config.toml")

	cs := []*aiClient{
		{ID: "claude-code", Name: "Claude Code", Method: "command", bin: findTool("claude"),
			Config: filepath.Join(home, ".claude.json"), key: "mcpServers"},
		{ID: "cursor", Name: "Cursor", Method: "link", app: "Cursor", Config: cursorCfg, key: "mcpServers"},
		{ID: "vscode", Name: "VS Code", Method: "link", app: "Visual Studio Code", Config: vscodeCfg, key: "servers"},
		{ID: "claude-desktop", Name: "Claude Desktop", Method: "file", app: "Claude", Config: desktopCfg, key: "mcpServers"},
		{ID: "windsurf", Name: "Windsurf", Method: "file", app: "Windsurf", Config: windsurfCfg, key: "mcpServers"},
		{ID: "codex", Name: "Codex CLI", Method: "command", bin: findTool("codex"), Config: codexCfg},
		{ID: "codex-desktop", Name: "Codex Desktop", Method: "file", app: "Codex", Config: codexCfg},
		{ID: "gemini", Name: "Gemini CLI", Method: "file", bin: findTool("gemini"), Config: geminiCfg, key: "mcpServers"},
	}
	for _, c := range cs {
		c := c
		if c.key != "" {
			c.current = func() string { return jsonServerCommand(c.Config, c.key) }
		}
		switch c.ID {
		case "claude-code":
			c.Installed = c.bin != ""
			c.setup = func() (string, error) {
				if c.bin == "" {
					return "", errors.New("Claude Code isn't installed (no `claude` command found)")
				}
				runTool(c.bin, "mcp", "remove", "--scope", "user", "volt") // replace an older entry
				return "", runTool(c.bin, "mcp", "add", "--scope", "user", "volt", "--", volt, "mcp")
			}
		case "codex":
			c.Installed = c.bin != "" // ~/.codex alone may be Codex Desktop's
			c.current = func() string { return tomlServerCommand(codexCfg) }
			c.setup = func() (string, error) {
				if c.bin == "" { // same config file: write it directly
					return writeServerTOML(codexCfg, volt)
				}
				runTool(c.bin, "mcp", "remove", "volt")
				return "", runTool(c.bin, "mcp", "add", "volt", "--", volt, "mcp")
			}
		case "codex-desktop": // shares ~/.codex/config.toml with Codex CLI
			c.Installed = appInstalled("Codex", filepath.Join(localApp, "Programs", "Codex"), "")
			c.current = func() string { return tomlServerCommand(codexCfg) }
			c.setup = func() (string, error) { return writeServerTOML(codexCfg, volt) }
		case "cursor":
			c.Installed = appInstalled("Cursor", filepath.Join(localApp, "Programs", "cursor"), "cursor") || exists(filepath.Dir(cursorCfg))
			c.setup = func() (string, error) {
				return "", openURL("cursor://anysphere.cursor-deeplink/mcp/install?name=volt&config=" +
					url.QueryEscape(base64.StdEncoding.EncodeToString(serverJSON(false))))
			}
		case "vscode":
			c.Installed = appInstalled("Visual Studio Code", filepath.Join(localApp, "Programs", "Microsoft VS Code"), "code") || exists(filepath.Dir(vscodeCfg))
			c.setup = func() (string, error) {
				return "", openURL("vscode:mcp/install?" + url.PathEscape(string(serverJSON(true))))
			}
		default: // file
			switch c.ID {
			case "claude-desktop":
				c.Installed = appInstalled("Claude", filepath.Join(localApp, "AnthropicClaude"), "") || exists(filepath.Dir(desktopCfg))
			case "windsurf":
				c.Installed = appInstalled("Windsurf", filepath.Join(localApp, "Programs", "Windsurf"), "windsurf") || exists(filepath.Dir(windsurfCfg))
			case "gemini":
				c.Installed = c.bin != "" || exists(filepath.Dir(geminiCfg))
			}
			c.setup = func() (string, error) {
				return writeServerJSON(c.Config, c.key, map[string]any{"command": volt, "args": []string{"mcp"}})
			}
		}
		if cur := c.current(); cur != "" {
			c.Configured = true
			c.Stale = cur != volt
		}
	}
	return cs
}

// ---- detection ----

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// findTool finds a command-line tool even when the IDE was started from the
// Dock with a minimal PATH.
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range toolDirs() {
		for _, n := range []string{name, name + ".cmd", name + ".exe"} {
			if p := filepath.Join(dir, n); exists(p) {
				return p
			}
		}
	}
	return ""
}

func toolDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".claude", "local"),
		"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".bun", "bin"), filepath.Join(home, ".volta", "bin"),
		filepath.Join(os.Getenv("APPDATA"), "npm"),
	}
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
	return append(dirs, nvm...)
}

func appInstalled(macApp, winDir, linuxCmd string) bool {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return exists("/Applications/"+macApp+".app") || exists(filepath.Join(home, "Applications", macApp+".app"))
	case "windows":
		return exists(winDir)
	}
	return linuxCmd != "" && findTool(linuxCmd) != ""
}

// jsonServerCommand returns the command registered for "volt" under key.
func jsonServerCommand(file, key string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var doc map[string]json.RawMessage
	var servers map[string]struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(data, &doc) != nil || json.Unmarshal(doc[key], &servers) != nil {
		return ""
	}
	return servers["volt"].Command
}

var tomlVolt = regexp.MustCompile(`(?ms)^\[mcp_servers\.volt\]\s*$.*?^command\s*=\s*"((?:[^"\\]|\\.)*)"`)

func tomlServerCommand(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if m := tomlVolt.FindSubmatch(data); m != nil {
		var s string
		if json.Unmarshal([]byte(`"`+string(m[1])+`"`), &s) == nil {
			return s
		}
		return string(m[1])
	}
	return ""
}

// ---- setup ----

// writeServerJSON sets <key>.volt in a JSON settings file, keeping every other
// setting. The old file is copied to <file>.volt-backup first; that path is
// returned.
// tomlSection matches a [mcp_servers.volt] table up to the next table header.
var tomlSection = regexp.MustCompile(`(?ms)^\[mcp_servers\.volt\]\s*$.*?(?:^\[|\z)`)

// writeServerTOML adds (or replaces) the volt server in a Codex config.toml,
// keeping everything else as written. It returns the backup's path, if any.
func writeServerTOML(file, volt string) (string, error) {
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	q, _ := json.Marshal(volt) // a TOML basic string uses JSON's escapes
	block := "[mcp_servers.volt]\ncommand = " + string(q) + "\nargs = [\"mcp\"]\n"
	text := string(old)
	if loc := tomlSection.FindStringIndex(text); loc != nil {
		end := loc[1]
		if end > loc[0] && end <= len(text) && end > 0 && text[end-1] == '[' {
			end-- // keep the next table's header
		}
		text = text[:loc[0]] + block + "\n" + text[end:]
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" {
			text += "\n"
		}
		text += block
	}
	if text == string(old) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old != nil {
		backup = file + ".volt-backup"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", err
		}
	}
	tmp := file + ".volt-tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return "", err
	}
	return backup, os.Rename(tmp, file)
}

func writeServerJSON(file, key string, server map[string]any) (string, error) {
	return editJSON(file, func(doc map[string]any) error {
		servers, _ := doc[key].(map[string]any)
		if servers == nil {
			if doc[key] != nil {
				return fmt.Errorf("%s: %q isn't an object, so Volt won't edit it", file, key)
			}
			servers = map[string]any{}
		}
		servers["volt"] = server
		doc[key] = servers
		return nil
	})
}

// editJSON changes a JSON settings file in place: the old file is kept as
// <file>.volt-backup and the new one written atomically. It returns the
// backup path ("" when nothing changed or there was no file).
func editJSON(file string, change func(doc map[string]any) error) (string, error) {
	doc := map[string]any{}
	old, err := os.ReadFile(file)
	switch {
	case err == nil && len(bytes.TrimSpace(old)) > 0:
		if err := json.Unmarshal(old, &doc); err != nil {
			return "", fmt.Errorf("%s isn't plain JSON (comments?), so Volt won't edit it. Add Volt by hand (Manual setup)", file)
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	if err := change(doc); err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	out = append(out, '\n')
	if bytes.Equal(out, old) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old != nil {
		backup = file + ".volt-backup"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", err
		}
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(file); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := file + ".volt-tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return "", err
	}
	return backup, os.Rename(tmp, file)
}

// runTool runs an AI app's CLI. Those are often Node scripts, so the PATH it
// gets includes the usual install dirs (the IDE may have a minimal PATH).
func runTool(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	path := append([]string{filepath.Dir(bin)}, toolDirs()...)
	cmd.Env = append(os.Environ(), "PATH="+strings.Join(append(path, os.Getenv("PATH")), string(os.PathListSeparator)))
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s %s: %s", filepath.Base(bin), strings.Join(args[:2], " "), msg)
	}
	return nil
}

func openURL(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("couldn't open the install link: %w", err)
	}
	return nil
}
