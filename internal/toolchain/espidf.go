package toolchain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ESP-IDF, pinned to an exact commit (see cloneAtSHA). To upgrade, bump sha
// from the trusted release tag in ref.
var espIDF = pinnedRepo{
	url: "https://github.com/espressif/esp-idf.git",
	sha: "67c1de1eebe095d554d281952fde63c16ee2dca0",
	ref: "v5.4",
}

// ForIDF is the full ESP-IDF toolchain `volt sdk build` makes SDK packs
// with: an ESP-IDF tree and its tools (install.sh), with CMake, Ninja and
// Python from Volt's earlier managed installs or PATH. Users never need it.
// The IDF target equals the chip name.
func ForIDF(chip string) (*Toolchain, error) {
	h := &Host{Platform: Platform()}
	if h.Platform == "" {
		return nil, fmt.Errorf("toolchains are not provided for this OS/architecture yet")
	}
	h.Manifest, h.ManifestSource = LoadManifest(false)
	return newESPIDF(chip, h)
}

// legacyBinDirs are the CMake, Ninja and Python dirs earlier manifests
// installed, when present.
func legacyBinDirs() []string {
	var dirs []string
	for _, d := range []string{"cmake/CMake.app/Contents/bin", "ninja", "python/bin"} {
		if p := filepath.Join(Root(), d); exists(p) {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// idfCompilerPrefix is the ~/.espressif/tools folder prefix of a target's GCC.
func idfCompilerPrefix(target string) string {
	if strings.HasPrefix(target, "esp32c") || strings.HasPrefix(target, "esp32h") || target == "esp32p4" {
		return "riscv32-esp-elf"
	}
	return "xtensa-esp"
}

// IDF locates the ESP-IDF in use and where its tools live.
type IDF struct {
	Path, ToolsPath string
	Managed         bool   // Volt's own clone under Root()
	Source          string // where Path came from, for messages
}

// FindIDF resolves ESP-IDF: build.espIdfPath setting, then $IDF_PATH, then
// ~/esp/esp-idf, then Volt's managed clone. A managed clone keeps its tools
// under Root() too, so nothing Volt deletes is shared with a user's own IDF.
func FindIDF() IDF {
	home, _ := os.UserHomeDir()
	userTools := os.Getenv("IDF_TOOLS_PATH")
	if userTools == "" {
		userTools = filepath.Join(home, ".espressif")
	}
	if p := setting("build.espIdfPath"); p != "" {
		return IDF{Path: p, ToolsPath: userTools, Source: "build.espIdfPath setting"}
	}
	if p := os.Getenv("IDF_PATH"); p != "" {
		return IDF{Path: p, ToolsPath: userTools, Source: "$IDF_PATH"}
	}
	if p := filepath.Join(home, "esp", "esp-idf"); exists(p) {
		return IDF{Path: p, ToolsPath: userTools, Source: "~/esp/esp-idf"}
	}
	return IDF{Path: filepath.Join(Root(), "esp-idf"), ToolsPath: filepath.Join(Root(), "espressif"), Managed: true}
}

func newESPIDF(chip string, h *Host) (*Toolchain, error) {
	idf := FindIDF()
	t := &Toolchain{Chip: chip, Name: "ESP-IDF (" + chip + ")"}
	if h.Platform == "linux-x64" {
		t.Components = append(t.Components, linuxPackages())
	}
	t.Components = append(t.Components, idfSource(idf), idfTools(idf, chip))
	t.BuildEnv = func() ([]string, error) { return idfEnv(idf, chip, legacyBinDirs()) }

	if idf.Managed {
		t.Managed = append(t.Managed, idf.Path, idf.ToolsPath)
		if home, _ := os.UserHomeDir(); exists(filepath.Join(home, ".espressif")) {
			t.Notes = append(t.Notes, "~/.espressif is no longer used by Volt's ESP-IDF; delete it yourself if nothing else needs it.")
		}
	} else {
		t.Notes = append(t.Notes, fmt.Sprintf("Using your ESP-IDF at %s (from %s); repair never touches it.", Tilde(idf.Path), idf.Source))
	}
	return t, nil
}

// linuxPackages covers the system packages ESP-IDF needs, via apt.
func linuxPackages() Component {
	cmds := []string{"git", "wget", "flex", "bison", "gperf", "cmake", "ninja"}
	pkgs := []string{"git", "wget", "flex", "bison", "gperf", "cmake", "ninja-build", "ccache",
		"libffi-dev", "libssl-dev", "dfu-util", "libusb-1.0-0"}
	c := Component{ID: "system-packages", Name: "System packages"}
	c.Check = func() Status {
		var missing []string
		for _, cmd := range cmds {
			if !hasCommand(cmd) {
				missing = append(missing, cmd)
			}
		}
		if len(missing) > 0 {
			return Status{Detail: "missing: " + strings.Join(missing, ", ")}
		}
		return Status{OK: true, Detail: "present"}
	}
	if hasCommand("apt-get") {
		c.Install = func(e *Emitter) error {
			return run(e, "", nil, "sudo", append([]string{"apt-get", "install", "-y"}, pkgs...)...)
		}
	}
	return c
}

// idfVersion reads major.minor.patch from tools/cmake/version.cmake.
func idfVersion(idf string) string {
	b, err := os.ReadFile(filepath.Join(idf, "tools", "cmake", "version.cmake"))
	if err != nil {
		return ""
	}
	var v []string
	for _, part := range []string{"MAJOR", "MINOR", "PATCH"} {
		m := regexp.MustCompile(`IDF_VERSION_` + part + `\s+(\d+)`).FindSubmatch(b)
		if m == nil {
			return ""
		}
		v = append(v, string(m[1]))
	}
	return strings.Join(v, ".")
}

// idfComplete reports what is missing from an ESP-IDF tree ("" = complete).
func idfComplete(idf string) string {
	for _, f := range []string{"export.sh", "install.sh", "tools/idf.py"} {
		if !exists(filepath.Join(idf, f)) {
			return "missing " + f
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(idf, "components")); len(ents) < 20 {
		return "components/ incomplete"
	}
	return ""
}

func idfSource(idf IDF) Component {
	c := Component{ID: "esp-idf", Name: "ESP-IDF " + espIDF.ref}
	c.Check = func() Status {
		st := Status{Path: idf.Path}
		if !exists(idf.Path) {
			st.Detail = "not installed"
		} else if miss := idfComplete(idf.Path); miss != "" {
			st.Detail = "incomplete (" + miss + ")"
		} else if idf.Managed {
			if m := readAssetMarker(idf.Path); m != nil {
				st.OK, st.Detail = true, m.Version
			} else if head := gitHead(idf.Path); head != espIDF.sha {
				st.Detail = fmt.Sprintf("wrong commit %.12s (pinned %.12s)", head, espIDF.sha)
			} else {
				st.OK, st.Detail = true, fmt.Sprintf("%s (%.12s)", espIDF.ref, espIDF.sha)
			}
		} else {
			st.OK, st.Detail = true, "v"+idfVersion(idf.Path)+" (yours)"
		}
		if !st.OK && !idf.Managed {
			st.Detail += fmt.Sprintf("; fix it, or clear %s so Volt manages its own", idf.Source)
		}
		return st
	}
	if idf.Managed {
		c.Install = func(e *Emitter) error {
			if err := needSpace(4 << 30); err != nil {
				return err
			}
			os.RemoveAll(idf.Path) // managed and not OK: start clean
			return cloneAtSHA(e, espIDF, idf.Path, true)
		}
	}
	return c
}

func readAssetMarker(dir string) *marker {
	var m marker
	b, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		return nil
	}
	if jsonErr := json.Unmarshal(b, &m); jsonErr != nil || m.SHA256 == "" {
		return nil
	}
	return &m
}

func idfTools(idf IDF, target string) Component {
	prefix := idfCompilerPrefix(target)
	c := Component{ID: "esp-idf-tools", Name: "ESP-IDF tools (" + target + ")"}
	c.Check = func() Status {
		st := Status{Path: idf.ToolsPath}
		gcc := findGCC(filepath.Join(idf.ToolsPath, "tools"), prefix)
		env := findPythonEnv(filepath.Join(idf.ToolsPath, "python_env"), idfVersion(idf.Path))
		switch {
		case gcc == "":
			st.Detail = prefix + " compiler not installed"
		case env == "":
			st.Detail = "Python environment (py>=3.10) not installed"
		default:
			m := pyEnvRe.FindStringSubmatch(filepath.Base(env))
			st.OK, st.Detail = true, gcc+", python "+m[2]+"."+m[3]
		}
		return st
	}
	c.Install = func(e *Emitter) error {
		if miss := idfComplete(idf.Path); miss != "" {
			return errors.New("ESP-IDF is " + miss)
		}
		if err := needSpace(2 << 30); err != nil {
			return err
		}
		path := strings.Join(append(legacyBinDirs(), os.Getenv("PATH")), string(os.PathListSeparator))
		env := []string{"PATH=" + path}
		if idf.Managed {
			env = append(env, "IDF_TOOLS_PATH="+idf.ToolsPath)
		}
		return run(e, idf.Path, env, filepath.Join(idf.Path, "install.sh"), target)
	}
	return c
}

type idfEnvFile struct {
	IDFInstalled map[string]idfEnvInstall `json:"idfInstalled"`
}

type idfEnvInstall struct {
	Version  string   `json:"version"`
	Path     string   `json:"path"`
	Features []string `json:"features"`
	Targets  []string `json:"targets"`
}

func refreshIDFEnvFile(idf IDF, target string) error {
	if !idf.Managed {
		return nil
	}
	version := idfVersion(idf.Path)
	if version == "" {
		return nil
	}
	recordVersion := strings.TrimSuffix(version, ".0")
	envPath := filepath.Join(idf.ToolsPath, "idf-env.json")
	key := idf.Path + "-v" + recordVersion
	env := idfEnvFile{IDFInstalled: map[string]idfEnvInstall{
		key: {
			Version:  recordVersion,
			Path:     idf.Path,
			Features: []string{"core"},
			Targets:  []string{target},
		},
	}}
	b, err := json.MarshalIndent(env, "", "    ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(envPath, b, 0o644)
}

// idfEnv is the environment export.sh would set up, taken straight from
// idf_tools.py (which export.sh wraps) so any failure is reported verbatim.
// binDirs (Volt's cmake, ninja, …) go ahead of the inherited PATH.
func idfEnv(idf IDF, target string, binDirs []string) ([]string, error) {
	pyEnv := findPythonEnv(filepath.Join(idf.ToolsPath, "python_env"), idfVersion(idf.Path))
	if pyEnv == "" {
		return nil, fmt.Errorf("ESP-IDF Python environment not found in %s; run: volt toolchain install %s", Tilde(idf.ToolsPath), target)
	}
	if err := refreshIDFEnvFile(idf, target); err != nil {
		return nil, fmt.Errorf("refresh ESP-IDF environment metadata: %w", err)
	}
	basePath := strings.Join(append(binDirs, os.Getenv("PATH")), string(os.PathListSeparator))
	env := []string{"IDF_PATH=" + idf.Path, "IDF_TOOLS_PATH=" + idf.ToolsPath, "IDF_TARGET=" + target}

	cmd := exec.Command(filepath.Join(pyEnv, "bin", "python"), filepath.Join(idf.Path, "tools", "idf_tools.py"),
		"export", "--format", "key-value")
	cmd.Env = append(cmd.Environ(), append(env, "PATH="+basePath)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("idf_tools.py export failed (%v): %s", err, strings.TrimSpace(stderr.String()))
	}
	path := basePath
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		switch {
		case !ok:
		case k == "PATH":
			path = strings.ReplaceAll(v, "$PATH", basePath)
		case k == "IDF_DEACTIVATE_FILE_PATH":
			os.Remove(v) // only export.sh's deactivate uses it
		default:
			env = append(env, line)
		}
	}
	env = append(env, "PATH="+path)
	if findOnPath(path, "ccache") != "" {
		env = append(env,
			"IDF_CCACHE_ENABLE=1",
			"CCACHE_DIR="+filepath.Join(CacheRoot(), "ccache", target),
			"CCACHE_COMPILERCHECK=content",
			"CCACHE_NOHASHDIR=true",
			"CCACHE_BASEDIR="+string(filepath.Separator),
		)
	}
	return env, nil
}

func findOnPath(pathValue, name string) string {
	for _, dir := range filepath.SplitList(pathValue) {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
		if runtime.GOOS == "windows" {
			exe := p + ".exe"
			if st, err := os.Stat(exe); err == nil && !st.IsDir() {
				return exe
			}
		}
	}
	return ""
}

// findGCC finds tools/<name>/<version>/<name>/bin/*-gcc for a tool folder
// starting with prefix, returning "<name> <version>".
func findGCC(tools, prefix string) string {
	names, _ := os.ReadDir(tools)
	for _, n := range names {
		if !strings.HasPrefix(n.Name(), prefix) {
			continue
		}
		vers, _ := os.ReadDir(filepath.Join(tools, n.Name()))
		for _, v := range vers {
			bins, _ := os.ReadDir(filepath.Join(tools, n.Name(), v.Name(), n.Name(), "bin"))
			for _, b := range bins {
				if strings.HasSuffix(b.Name(), "-gcc") || strings.HasSuffix(b.Name(), "-gcc.exe") {
					return n.Name() + " " + v.Name()
				}
			}
		}
	}
	return ""
}

var pyEnvRe = regexp.MustCompile(`^idf(\d+\.\d+)_py(\d+)\.(\d+)_env$`)

// findPythonEnv finds idf<X.Y>_py<A.B>_env with Python >= 3.10 for the IDF
// version (any IDF version when unknown), returning its path.
func findPythonEnv(dir, idfVer string) string {
	ents, _ := os.ReadDir(dir)
	for _, ent := range ents {
		m := pyEnvRe.FindStringSubmatch(ent.Name())
		if m == nil || (idfVer != "" && !strings.HasPrefix(idfVer+".", m[1]+".")) {
			continue
		}
		major, _ := strconv.Atoi(m[2])
		minor, _ := strconv.Atoi(m[3])
		if (major > 3 || major == 3 && minor >= 10) && exists(filepath.Join(dir, ent.Name(), "bin", "python")) {
			return filepath.Join(dir, ent.Name())
		}
	}
	return ""
}

func needSpace(bytes int64) error {
	os.MkdirAll(Root(), 0o755)
	if free := freeBytes(Root()); free >= 0 && free < bytes {
		return fmt.Errorf("not enough disk space: %s free, need %s", humanSize(free), humanSize(bytes))
	}
	return nil
}
