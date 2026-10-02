package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Project is one program to build for one chip.
type Project struct {
	Dir       string // project directory (holds volt.toml)
	Name      string // firmware name; the app image is <Name>.bin
	Chip      string
	Code      string // compiled C
	Framework string // SDK framework dir (holds esp32/, include/)
	Devices   string // device library (holds <prefix>/driver.c)
	Env       []string
}

// staleCache matches output from a build dir CMake can no longer use: moved
// or copied projects, a replaced toolchain, a half-written cache.
var staleCache = regexp.MustCompile(`(?i)CMake Error.*configure_file|CMakeFiles/CMakeTmp|` +
	`Error in dependency provider|does not match the source .* used to generate cache|` +
	`is different than the directory .* where CMakeCache\.txt was created|` +
	`loading 'build\.ninja'|is not a full path to an existing compiler|` +
	`Run 'idf\.py fullclean' to start again`)

// ESP32 generates <project>/build/<chip>/ as an ESP-IDF project over the
// read-only framework and builds it, returning the app image path. A build
// that fails on a stale CMake cache is retried once from a clean cache.
func ESP32(p Project, r *Reporter) (string, error) {
	fw := filepath.Join(p.Framework, "esp32")
	if _, err := os.Stat(filepath.Join(fw, "volt.cmake")); err != nil {
		return "", fmt.Errorf("ESP32 framework not found at %s", fw)
	}
	dir := filepath.Join(p.Dir, "build", p.Chip)
	if err := generateESP32(dir, fw, p); err != nil {
		return "", err
	}
	if err := ensureESP32BuildCache(dir, p); err != nil {
		r.Message("warn", "Firmware build cache unavailable: "+err.Error())
	}

	logf, err := os.Create(filepath.Join(dir, "build.log"))
	if err != nil {
		return "", err
	}
	defer logf.Close()

	py, idfPy, err := idfCommand(p.Env)
	if err != nil {
		return "", err
	}
	t := &tool{r: r, stage: "build", band: buildBand, log: logf}
	r.Step("build", "Building firmware...", buildBand.start)
	out, err := t.run(dir, p.Env, py, idfPy, "build")
	cache := filepath.Join(dir, "build")
	if errors.Is(err, context.Canceled) {
		return "", err
	}
	if err != nil && staleCache.MatchString(out) {
		r.Step("build", "Build cache looks stale — clearing it and retrying once...", buildBand.start)
		if rmErr := removeBuildCache(cache); rmErr != nil {
			return "", fmt.Errorf("clearing %s: %w", cache, rmErr)
		}
		if cacheErr := ensureESP32BuildCache(dir, p); cacheErr != nil {
			r.Message("warn", "Firmware build cache unavailable: "+cacheErr.Error())
		}
		out, err = t.run(dir, p.Env, py, idfPy, "build")
	}
	if err != nil {
		if t.errors == 0 { // nothing recognisable shown: don't hide why
			for _, l := range tail(out, 25) {
				r.Message("error", l)
			}
		}
		return "", fmt.Errorf("build failed (full log: %s)", logf.Name())
	}
	r.Step("build", "Build complete", buildBand.end)

	app := Artifact(p.Dir, p.Chip, p.Name)
	if app == "" {
		return "", fmt.Errorf("build succeeded but %s.bin is missing", p.Name)
	}
	return app, nil
}

// ensureESP32BuildCache keeps ESP-IDF's expensive CMake/Ninja build directory
// outside the project tree. Removing <project>/build then drops only generated
// wrapper files; the compiled framework/object cache survives in ~/.volt/cache.
func ensureESP32BuildCache(dir string, p Project) error {
	if os.Getenv("VOLT_DISABLE_GLOBAL_BUILD_CACHE") != "" {
		return nil
	}
	link := filepath.Join(dir, "build")
	target, err := esp32BuildCacheTarget(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	if st, err := os.Lstat(link); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			dst, err := os.Readlink(link)
			if err == nil {
				if !filepath.IsAbs(dst) {
					dst = filepath.Join(filepath.Dir(link), dst)
				}
				if filepath.Clean(dst) == filepath.Clean(target) {
					return os.MkdirAll(target, 0o755)
				}
			}
			if err := os.Remove(link); err != nil {
				return err
			}
		} else if st.IsDir() {
			if _, err := os.Stat(target); os.IsNotExist(err) {
				if err := os.Rename(link, target); err != nil {
					return err
				}
			} else {
				return nil
			}
		} else {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}

func esp32BuildCacheTarget(p Project) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir, _ := filepath.Abs(p.Dir)
	framework, _ := filepath.Abs(p.Framework)
	devices, _ := filepath.Abs(p.Devices)
	sum := sha256.Sum256([]byte(strings.Join([]string{dir, p.Name, p.Chip, framework, devices}, "\x00")))
	return filepath.Join(home, ".volt", "cache", "firmware-builds", p.Chip+"-"+hex.EncodeToString(sum[:])[:16]), nil
}

func removeBuildCache(path string) error {
	target := path
	if dst, err := os.Readlink(path); err == nil {
		if filepath.IsAbs(dst) {
			target = dst
		} else {
			target = filepath.Join(filepath.Dir(path), dst)
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if target != path {
		return os.Remove(path)
	}
	return nil
}

// idfCommand returns the IDF Python and idf.py from the build environment.
func idfCommand(env []string) (python, idfPy string, err error) {
	var idf, pyEnv string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "IDF_PATH":
			idf = v
		case "IDF_PYTHON_ENV_PATH":
			pyEnv = v
		}
	}
	if idf == "" || pyEnv == "" {
		return "", "", errors.New("ESP-IDF environment incomplete (no IDF_PATH or IDF_PYTHON_ENV_PATH)")
	}
	return filepath.Join(pyEnv, "bin", "python"), filepath.Join(idf, "tools", "idf.py"), nil
}

// generateESP32 writes the IDF project. Files are only rewritten when their
// content changes, so ninja rebuilds just what the new program needs.
func generateESP32(dir, fw string, p Project) error {
	drivers, err := usedDrivers(p.Devices, p.Code)
	if err != nil {
		return err
	}
	var srcs strings.Builder
	for _, d := range drivers {
		fmt.Fprintf(&srcs, "\n    %s", cmakeQuote(d))
	}

	top := fmt.Sprintf(`# Generated by volt build — do not edit.
cmake_minimum_required(VERSION 3.16)
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(%s)
`, p.Name)
	main := fmt.Sprintf(`# Generated by volt build — do not edit.
include(%s)
idf_component_register(
    SRCS "main.c" ${VOLT_CORE_SRCS}%s
    INCLUDE_DIRS ${VOLT_INCLUDE_DIRS}
)
`, cmakeQuote(filepath.Join(fw, "volt.cmake")), srcs.String())

	for _, f := range []struct{ path, text string }{
		{filepath.Join(dir, "CMakeLists.txt"), top},
		{filepath.Join(dir, "main", "CMakeLists.txt"), main},
		{filepath.Join(dir, "main", "main.c"), p.Code},
	} {
		if err := writeIfChanged(f.path, f.text); err != nil {
			return err
		}
	}
	return nil
}

// usedDrivers lists devices/*/driver.c whose prefix (the folder name) appears
// in the program as "<prefix>_": generated C calls <prefix>_init() and uses
// <prefix>_t for each device. Unused drivers are not compiled, so one broken
// installed driver cannot break every build.
func usedDrivers(devices, code string) ([]string, error) {
	all, err := filepath.Glob(filepath.Join(devices, "*", "driver.c"))
	if err != nil {
		return nil, err
	}
	var used []string
	for _, d := range all {
		if strings.Contains(code, filepath.Base(filepath.Dir(d))+"_") {
			used = append(used, d)
		}
	}
	return used, nil
}

func cmakeQuote(p string) string {
	p = filepath.ToSlash(p)
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(p) + `"`
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

// noDownloadMode matches esptool failing to get the chip into download mode.
var noDownloadMode = regexp.MustCompile(`No serial data received|Failed to connect to`)

// FlashESP32 writes the built project (bootloader, partition table, app) to
// the board on port.
func FlashESP32(dir, chip, port string, env []string, r *Reporter) error {
	bdir := filepath.Join(dir, "build", chip)
	logf, err := os.Create(filepath.Join(bdir, "flash.log"))
	if err != nil {
		return err
	}
	defer logf.Close()
	py, idfPy, err := idfCommand(env)
	if err != nil {
		return err
	}
	t := &tool{r: r, stage: "flash", band: flashBand, log: logf}
	r.Step("flash", "Flashing to "+port+"...", flashBand.start)
	out, err := t.run(bdir, env, py, idfPy, "-p", port, "flash")
	if errors.Is(err, context.Canceled) {
		return err
	}
	if err != nil {
		if noDownloadMode.MatchString(out) {
			r.Message("error", "The board's auto-reset didn't enter download mode. Hold BOOT, run the flash "+
				"again, and release BOOT when \"Connecting...\" appears. (Boards without RTS/DTR wired always need this.)")
		} else if t.errors == 0 {
			for _, l := range tail(out, 25) {
				r.Message("error", l)
			}
		}
		return fmt.Errorf("flash failed (full log: %s)", logf.Name())
	}
	r.Step("flash", "Flash complete", flashBand.end)
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
