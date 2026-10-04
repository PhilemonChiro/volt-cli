package toolchain

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func voltHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".volt")
}

// Root is where Volt-managed toolchains live. Repair never deletes outside it.
func Root() string { return filepath.Join(voltHome(), "toolchains") }

func CacheRoot() string { return filepath.Join(voltHome(), "cache") }

// Platform is the manifest's platform key for this machine, or "" when
// toolchains are not provided for it.
func Platform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "darwin-arm64"
	case "darwin/amd64":
		return "darwin-x86_64"
	case "linux/amd64":
		return "linux-x64"
	case "windows/amd64":
		return "win32-x64"
	}
	return ""
}

// setting reads a string from ~/.volt/settings.json (flat dotted keys, shared
// with the IDE). Missing or non-string values read as "".
func setting(key string) string {
	b, err := os.ReadFile(filepath.Join(voltHome(), "settings.json"))
	if err != nil {
		return ""
	}
	var s map[string]any
	json.Unmarshal(b, &s)
	v, _ := s[key].(string)
	return strings.TrimSpace(v)
}

// sourcePreference is build.toolchainSource: auto, mirror or upstream.
func sourcePreference() string {
	if v := setting("build.toolchainSource"); v == "mirror" || v == "upstream" {
		return v
	}
	return "auto"
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Tilde shortens a path under the home directory for display.
func Tilde(p string) string {
	home, _ := os.UserHomeDir()
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok && home != "" {
		return "~/" + rest
	}
	return p
}

// SettingInt reads a number from ~/.volt/settings.json (0 when unset).
func SettingInt(key string) int {
	b, err := os.ReadFile(filepath.Join(voltHome(), "settings.json"))
	if err != nil {
		return 0
	}
	var s map[string]any
	json.Unmarshal(b, &s)
	v, _ := s[key].(float64)
	return int(v)
}
