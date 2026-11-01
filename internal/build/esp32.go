package build

import (
	"bytes"
	"context"
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
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/esptool"
	"github.com/PhilemonChiro/volt-cli/internal/proc"
	"github.com/PhilemonChiro/volt-cli/internal/sdk"
)

// Project is one program to build for one chip.
type Project struct {
	Dir       string // project directory (holds volt.toml)
	Name      string // firmware name; the app image is <Name>.bin
	Chip      string
	Code      string // compiled C
	Framework string // SDK framework dir (holds esp32/, include/)
	Devices   string // device libraries (hold <prefix>/driver.c): the shared one, then the project's (see DeviceDirs)
	Env       []string
	Optimize  string // "debug", "size", "speed", or "" for the pack's default
}

// ESP32 builds the project with the installed ESP-IDF pack (see package
// sdk): it writes <project>/build/<chip>/main/main.c, compiles it with the
// runtime and the drivers the program uses, links against the pack and makes
// the app image, returning its path. Builds are incremental.
func ESP32(p Project, r *Reporter) (string, error) {
	fw := filepath.Join(p.Framework, "esp32")
	if _, err := os.Stat(filepath.Join(fw, "volt.cmake")); err != nil {
		return "", fmt.Errorf("ESP32 framework not found at %s", fw)
	}
	pack, err := sdk.Find(p.Chip)
	if err != nil {
		return "", err
	}
	spec, err := sdk.LoadSpec(pack)
	if err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(pack), err)
	}
	tools, err := esp32Tools(p.Env, spec)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(p.Dir, "build", p.Chip)
	out := filepath.Join(dir, "build")
	if err := dropIDFBuild(out); err != nil {
		return "", err
	}
	if err := writeIfChanged(filepath.Join(dir, "main", "main.c"), p.Code); err != nil {
		return "", err
	}
	srcs, err := VoltSources(dir, p.Framework, p.Devices, p.Code)
	if err != nil {
		return "", err
	}
	libHeaders, err := libraryHeaders(filepath.Join(dir, "main"), p.Devices, p.Code)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	logf, err := os.Create(filepath.Join(dir, "build.log"))
	if err != nil {
		return "", err
	}
	defer logf.Close()
	tools.Log = logf
	if err := checkCompiler(pack, tools.Bin, spec, r); err != nil {
		return "", err
	}

	r.Step("build", "Building firmware...", buildBand.start)
	res, err := sdk.Build(proc.Root, pack, spec, sdk.App{
		Name: p.Name, Project: dir, Sources: srcs,
		Framework: filepath.Join(p.Framework, "include"), Out: out,
		Optimize: sdk.OptimizeFlags[p.Optimize], Include: libHeaders,
	}, tools)
	if errors.Is(err, context.Canceled) {
		return "", err
	}
	if err != nil {
		lines := strings.Split(err.Error(), "\n")
		if len(lines) > 26 {
			lines = append(lines[:1], lines[len(lines)-25:]...)
		}
		for _, l := range lines {
			if l = strings.TrimSpace(ansiRe.ReplaceAllString(l, "")); l != "" {
				r.Message("error", l)
			}
		}
		return "", fmt.Errorf("build failed (full log: %s)", logf.Name())
	}
	for _, l := range res.Warnings {
		if l = ansiRe.ReplaceAllString(l, ""); strings.Contains(l, "warning:") && strings.Contains(l, ".c:") {
			r.Message("warn", strings.TrimSpace(l))
		}
	}
	if res.Linked {
		r.Step("build", fmt.Sprintf("Build complete (%d of %d files compiled)", res.Compiled, res.Units), buildBand.end)
	} else {
		r.Step("build", "Firmware up to date", buildBand.end)
	}
	return res.Bin, nil
}

// ansiRe matches the colour codes in compiler output (the pack's flags keep
// ESP-IDF's -fdiagnostics-color=always; build.log keeps them).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// exe adds Windows' .exe suffix to a toolchain command name for os.Stat;
// exec.Command resolves the suffix on its own.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// esp32Tools finds the pack's compiler in the toolchain.
func esp32Tools(env []string, spec *sdk.Spec) (sdk.Tools, error) {
	var path string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "PATH" {
			path = v
		}
	}
	gcc := exe(spec.ToolPrefix + "gcc")
	for _, d := range filepath.SplitList(path + string(filepath.ListSeparator) + os.Getenv("PATH")) {
		if fi, err := os.Stat(filepath.Join(d, gcc)); err == nil && !fi.IsDir() {
			return sdk.Tools{Bin: d}, nil
		}
	}
	return sdk.Tools{}, fmt.Errorf("%s not found in the toolchain", gcc)
}

// checkCompiler warns when the compiler is not the one the pack was built
// with: objects would still link, but the build is no longer the verified one.
func checkCompiler(pack, bin string, spec *sdk.Spec, r *Reporter) error {
	var prov sdk.Provenance
	b, err := os.ReadFile(filepath.Join(pack, "provenance.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &prov); err != nil {
		return fmt.Errorf("%s: provenance.json: %w", filepath.Base(pack), err)
	}
	v, err := exec.Command(filepath.Join(bin, spec.ToolPrefix+"gcc"), "--version").Output()
	if err != nil {
		return fmt.Errorf("%sgcc: %w", spec.ToolPrefix, err)
	}
	got, _, _ := strings.Cut(string(v), "\n")
	got = strings.Replace(strings.TrimSpace(got), "gcc.exe ", "gcc ", 1) // Windows
	if got != prov.Compiler {
		r.Message("warn", fmt.Sprintf("compiler %q differs from the pack's %q", got, prov.Compiler))
	}
	return nil
}

// dropIDFBuild removes what an ESP-IDF build left at out before packs: a
// link into ~/.volt/cache/firmware-builds, or a CMake build dir.
func dropIDFBuild(out string) error {
	fi, err := os.Lstat(out)
	if err != nil {
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return os.Remove(out)
	}
	if _, err := os.Stat(filepath.Join(out, "CMakeCache.txt")); err == nil {
		return os.RemoveAll(out)
	}
	return nil
}

// usedDrivers lists devices/*/driver.c whose prefix (the folder name) appears
// in the program as "<prefix>_": generated C calls <prefix>_init() and uses
// <prefix>_t for each device. Unused drivers are not compiled, so one broken
// installed driver cannot break every build.
func usedDrivers(devices, code string) ([]string, error) {
	var used []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(devices) {
		all, err := filepath.Glob(filepath.Join(dir, "*", "driver.c"))
		if err != nil {
			return nil, err
		}
		for _, d := range all {
			prefix := filepath.Base(filepath.Dir(d))
			// The shared library comes first: a project library can't replace it.
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			if strings.Contains(code, prefix+"_") {
				used = append(used, d)
			}
		}
	}
	return used, nil
}

// DeviceDirs joins the shared device library and a project's own devices
// folder (when it exists) into one Project.Devices value.
func DeviceDirs(shared, project string) string {
	if project == "" {
		return shared
	}
	if st, err := os.Stat(project); err != nil || !st.IsDir() {
		return shared
	}
	return shared + string(os.PathListSeparator) + project
}

// libraryHeaders gathers the header.h of each device library the program
// uses into <main>/volt_libs.h (after the framework headers it builds on) and
// returns it for force-inclusion, so a library declares its own struct and
// functions instead of editing the shared framework headers.
func libraryHeaders(mainDir, devices, code string) ([]string, error) {
	drivers, err := usedDrivers(devices, code)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, d := range drivers {
		h := filepath.Join(filepath.Dir(d), "header.h")
		if _, err := os.Stat(h); err == nil {
			fmt.Fprintf(&b, "#include %q\n", h)
		}
	}
	if b.Len() == 0 {
		return nil, nil
	}
	out := filepath.Join(mainDir, "volt_libs.h")
	text := "// Generated by volt build — device library headers. Do not edit.\n#pragma once\n#include \"volt/volt.h\"\n#include \"volt/sensor.h\"\n" + b.String()
	if err := writeIfChanged(out, text); err != nil {
		return nil, err
	}
	return []string{out}, nil
}

var coreSrcRe = regexp.MustCompile(`"\$\{_VOLT_FW\}/([^"]+\.c)"`)

// VoltSources lists the C files ESP-IDF compiles into the app component for
// a program, in its order: main.c (under project), the runtime from
// volt.cmake, then the drivers the program uses.
func VoltSources(project, framework, devices, code string) ([]string, error) {
	fw := filepath.Join(framework, "esp32")
	b, err := os.ReadFile(filepath.Join(fw, "volt.cmake"))
	if err != nil {
		return nil, err
	}
	srcs := []string{filepath.Join(project, "main", "main.c")}
	for _, m := range coreSrcRe.FindAllSubmatch(b, -1) {
		srcs = append(srcs, filepath.Join(fw, string(m[1])))
	}
	drivers, err := usedDrivers(devices, code)
	return append(srcs, drivers...), err
}

func writeIfChanged(path, text string) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(text)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

var nameRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// FirmwareName makes a CMake project name from a project name.
func FirmwareName(s string) string {
	if s = strings.Trim(nameRe.ReplaceAllString(s, "-"), "-"); s == "" {
		return "volt-app"
	}
	return s
}

// DefaultFlashBaud is the stub's write rate unless overridden; a failing
// write falls back to lower rates on its own.
const DefaultFlashBaud = 460800

// FlashESP32 writes the built project to the board on port: the pack's
// bootloader and partition table, then the app image. Images flash already
// holds are skipped unless full is set.
func FlashESP32(dir, chip, port string, baud int, full bool, env []string, r *Reporter) error {
	bdir := filepath.Join(dir, "build", chip)
	pack, err := sdk.Find(chip)
	if err != nil {
		return err
	}
	spec, err := sdk.LoadSpec(pack)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(pack), err)
	}
	c, err := esptool.ChipByName(spec.Chip)
	if err != nil {
		return err
	}
	bins, _ := filepath.Glob(filepath.Join(bdir, "build", "*.bin"))
	if len(bins) != 1 {
		return fmt.Errorf("expected one app image in %s, found %d; run volt build", filepath.Join(bdir, "build"), len(bins))
	}
	o := esptool.FlashOptions{
		Chip: c, Port: port, Baud: baud, Full: full,
		FlashMode: spec.FlashSettings["flash_mode"], FlashFreq: spec.FlashSettings["flash_freq"],
		FlashSize: spec.FlashSettings["flash_size"],
	}
	for _, part := range spec.Flash {
		file := strings.ReplaceAll(part.File, "{pack}", pack)
		if part.File == "" {
			file = bins[0]
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		off, err := strconv.ParseUint(part.Offset, 0, 32)
		if err != nil {
			return fmt.Errorf("%s offset %q: %w", part.Name, part.Offset, err)
		}
		o.Parts = append(o.Parts, esptool.FlashPart{Offset: uint32(off), Data: data, Name: filepath.Base(file)})
	}

	logf, err := os.Create(filepath.Join(bdir, "flash.log"))
	if err != nil {
		return err
	}
	defer logf.Close()
	o.Log = logf
	b := flashBand
	o.Progress = func(msg string, done, total int) {
		if done == 0 || done == total {
			r.Step("flash", msg, b.start+done*(b.end-1-b.start)/max(total, 1))
			return
		}
		pct := 100 * done / total
		r.Progress("flash", fmt.Sprintf("Flashing... %d%%", pct), b.start+done*(b.end-1-b.start)/total)
	}
	r.Step("flash", "Flashing to "+port+"...", b.start)
	start := time.Now()
	err = esptool.WriteFlash(proc.Root, o)
	if errors.Is(err, context.Canceled) {
		return err
	}
	if err != nil {
		fmt.Fprintln(logf, "error:", err)
		if errors.Is(err, esptool.ErrConnect) {
			r.Message("error", "The board's auto-reset didn't enter download mode. Hold BOOT, run the flash "+
				"again, and release BOOT when \"Connecting...\" appears. (Boards without RTS/DTR wired always need this.)")
		}
		return fmt.Errorf("flash failed: %w", err)
	}
	r.Step("flash", fmt.Sprintf("Flash complete (%.1fs)", time.Since(start).Seconds()), b.end)
	return nil
}

// Artifact returns the app image of a previous build, or "".
func Artifact(dir, chip, name string) string {
	p := filepath.Join(dir, "build", chip, "build", name+".bin")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// CrashTools returns the project's built ELF and the chip's addr2line, for
// decoding panic backtraces. Either is "" when not found.
func CrashTools(dir, chip string, env []string) (elf, addr2line string) {
	if els, _ := filepath.Glob(filepath.Join(dir, "build", chip, "build", "*.elf")); len(els) == 1 {
		elf = els[0]
	}
	pack, err := sdk.Find(chip)
	if err != nil {
		return elf, ""
	}
	spec, err := sdk.LoadSpec(pack)
	if err != nil {
		return elf, ""
	}
	if t, err := esp32Tools(env, spec); err == nil {
		addr2line = filepath.Join(t.Bin, exe(spec.ToolPrefix+"addr2line"))
	}
	return elf, addr2line
}
