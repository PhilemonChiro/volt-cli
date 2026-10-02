package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/build"
	"github.com/PhilemonChiro/volt-cli/internal/sdk"
	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

// sdkEpoch fixes __DATE__/__TIME__ (SOURCE_DATE_EPOCH) in the reference and
// verification builds, so their images can be compared byte for byte.
const sdkEpoch = "1767225600" // 2026-01-01T00:00:00Z

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func runSdk(args []string) int {
	switch first(args) {
	case "build":
	case "install":
		return runSdkInstall(args[1:])
	default:
		stderr.Println("Usage: volt sdk install <pack.tar.gz>\n       volt sdk build <chip> [-o dir] [--work dir] [--also project]... [--no-verify] [-v]")
		if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
			return 0
		}
		return 2
	}
	fl, devices := newFlags("sdk build", "<chip> [-o dir] [--work dir] [--also project]... [--no-verify] [-v]")
	out := fl.String("o", ".", "output `dir` for the pack, its .tar.gz and .sha256")
	work := fl.String("work", "", "work `dir` for reference and verification builds (default ~/.volt/cache/sdk-build/<chip>)")
	var also stringList
	fl.Var(&also, "also", "also verify this project `dir` (repeatable; examples are always verified)")
	noVerify := fl.Bool("no-verify", false, "skip verification (the pack is not archived)")
	verbose := fl.Bool("v", false, "show all build tool output")
	pos, ok := parse(fl, args[1:])
	if !ok || len(pos) != 1 {
		return 2
	}
	chip := strings.ToLower(pos[0])
	if chip != "esp32" {
		stderr.Printf("[red]volt sdk: only esp32 packs are supported, not %s[/red]\n", chip)
		return 2
	}
	if *work == "" {
		*work = filepath.Join(toolchain.CacheRoot(), "sdk-build", chip)
	}
	r := build.NewReporter(false, *verbose)
	if err := sdkBuild(chip, *out, *work, *devices, also, !*noVerify, r); err != nil {
		return failed(r, err)
	}
	return 0
}

func sdkBuild(chip, outDir, work, devicesFlag string, also []string, verify bool, r *build.Reporter) error {
	env, err := idfEnv(chip, r)
	if err != nil {
		return err
	}
	devs := useDevices(devicesFlag)
	if devs == "" {
		return errors.New("no device library found: pass --devices or set $VOLT_DEVICES")
	}
	framework := filepath.Join(filepath.Dir(devs), "framework")
	if !isDir(framework) {
		return fmt.Errorf("no framework next to the device library (%s)", toolchain.Tilde(framework))
	}
	work, _ = filepath.Abs(work)
	outDir, _ = filepath.Abs(outDir)
	// Dates are fixed.
	if os.Getenv("SOURCE_DATE_EPOCH") == "" {
		os.Setenv("SOURCE_DATE_EPOCH", sdkEpoch)
	}

	// 1. Reference project: a stub main, the whole runtime, the profile. It
	// sits at the same depth as the verification projects: ESP-IDF's assert
	// messages embed source paths relative to the build dir.
	ref := filepath.Join(work, "projects", "_reference")
	gen := filepath.Join(ref, "build", chip)
	profile := filepath.Join(framework, chip, "sdkconfig")
	stdout.Printf("[bold]Reference build[/bold] %s\n", toolchain.Tilde(ref))
	if _, err := build.ESP32IDF(build.Project{
		Dir: ref, Name: "volt-sdk-ref", Chip: chip, Code: "void app_main(void) {}\n",
		Framework: framework, Devices: devs, Env: env,
	}, profile, r); err != nil {
		return fmt.Errorf("reference build: %w", err)
	}

	// 2. Harvest into a staging dir, then name the pack after its profile.
	refBuild := filepath.Join(gen, "build")
	staging := filepath.Join(work, "pack")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	spec, err := sdk.Harvest(sdk.Source{
		BuildDir: refBuild, IDF: envValue(env, "IDF_PATH"),
		Framework: filepath.Join(framework, "include"), Profile: profile,
	}, staging)
	if err != nil {
		return fmt.Errorf("harvest: %w", err)
	}
	hash, err := sdk.ProfileHash(staging)
	if err != nil {
		return err
	}
	bin, err := sdk.CompilerBin(refBuild)
	if err != nil {
		return err
	}
	if err := writeProvenance(staging, spec, hash, bin, env); err != nil {
		return err
	}
	name := fmt.Sprintf("volt-sdk-%s-idf%s-%s", chip, strings.TrimPrefix(spec.IDFVersion, "v"), hash[:12])
	pack := filepath.Join(outDir, name)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(pack); err != nil {
		return err
	}
	if err := os.Rename(staging, pack); err != nil {
		return err
	}
	stdout.Printf("[green]Pack[/green] %s (%s)\n", toolchain.Tilde(pack), dirSize(pack))

	// 3. Verify: every example and --also project, pack build vs ESP-IDF build.
	if !verify {
		stdout.Println("[yellow]Not verified (--no-verify): no archive written[/yellow]")
		return nil
	}
	tools := sdk.Tools{Bin: bin}
	examples, _ := filepath.Glob(filepath.Join(filepath.Dir(framework), "examples", "*", "volt.toml"))
	var projects []string
	for _, e := range examples {
		projects = append(projects, filepath.Dir(e))
	}
	projects = append(projects, also...)
	failures := 0
	for _, dir := range projects {
		if err := verifyProject(dir, chip, framework, devs, work, pack, profile, spec, tools, env, r); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			var skip skipError
			if errors.As(err, &skip) {
				stdout.Printf("  [yellow]skip[/yellow] %-20s %s\n", filepath.Base(dir), err)
				continue
			}
			failures++
			stdout.Printf("  [red]FAIL[/red] %-20s %s\n", filepath.Base(dir), err)
		}
	}
	if failures > 0 {
		return fmt.Errorf("pack verification failed for %d project(s); no archive written", failures)
	}

	// 4. Archive.
	tgz := pack + ".tar.gz"
	sum, err := sdk.Archive(pack, tgz)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tgz+".sha256", []byte(sum+"  "+filepath.Base(tgz)+"\n"), 0o644); err != nil {
		return err
	}
	st, _ := os.Stat(tgz)
	stdout.Printf("[green]Archived[/green] %s (%.1f MB, sha256 %s)\n", toolchain.Tilde(tgz), float64(st.Size())/1e6, sum)
	return nil
}

func runSdkInstall(args []string) int {
	fl, _ := newFlags("sdk install", "<pack.tar.gz>")
	pos, ok := parse(fl, args)
	if !ok || len(pos) != 1 {
		return 2
	}
	r := build.NewReporter(false, false)
	dir, err := sdk.Install(pos[0])
	if err != nil {
		return failed(r, err)
	}
	stdout.Printf("[green]Installed[/green] %s\n", toolchain.Tilde(dir))
	return 0
}

// idfEnv is the full ESP-IDF environment the reference and verification
// builds run in.
func idfEnv(chip string, r *build.Reporter) ([]string, error) {
	tc, err := toolchain.ForIDF(chip)
	if err != nil {
		return nil, err
	}
	if all, ready := tc.Status(); !ready {
		for _, c := range all {
			if !c.OK {
				r.Message("error", c.Name+": "+c.Detail)
			}
		}
		return nil, fmt.Errorf("%s toolchain not ready (needed only to build SDK packs; see ESP-IDF install docs or set $IDF_PATH)", tc.Name)
	}
	return tc.BuildEnv()
}

type skipError struct{ error }

func (e skipError) Unwrap() error { return e.error }

// verifyProject builds one project both ways and compares the results: ELF
// text/data/bss must be equal and the app images equal in every loaded byte.
func verifyProject(dir, chip, framework, devs, work, pack, profile string, spec *sdk.Spec, tools sdk.Tools, env []string, r *build.Reporter) error {
	src, err := findSource(dir)
	if err != nil {
		return skipError{err}
	}
	pchip, pname := projectInfo(dir)
	if pchip != "" && !strings.EqualFold(pchip, chip) {
		return skipError{fmt.Errorf("chip %s", pchip)}
	}
	if pname == "" {
		pname = filepath.Base(dir)
	}
	name := build.FirmwareName(pname)
	code, errs, err := compileFile(src)
	if err != nil {
		return skipError{fmt.Errorf("does not compile: %w", err)}
	}
	if n, _ := counts(errs); n > 0 {
		return skipError{fmt.Errorf("does not compile: %s", plural(n, "error"))}
	}

	vdir := filepath.Join(work, "projects", filepath.Base(dir))
	gen := filepath.Join(vdir, "build", chip)
	stdout.Printf("[bold]Verify[/bold] %s\n", filepath.Base(dir))
	start := time.Now()
	if _, err := build.ESP32IDF(build.Project{
		Dir: vdir, Name: name, Chip: chip, Code: code, Framework: framework, Devices: devs, Env: env,
	}, profile, r); err != nil {
		return skipError{fmt.Errorf("ESP-IDF build fails: %w", err)}
	}
	tIDF := time.Since(start)
	idf := filepath.Join(gen, "build")
	if err := sameConfig(filepath.Join(idf, "config", "sdkconfig.h"), filepath.Join(pack, "config", "sdkconfig.h")); err != nil {
		return fmt.Errorf("profile drift: %w", err)
	}

	srcs, err := build.VoltSources(gen, framework, devs, code)
	if err != nil {
		return err
	}
	out := filepath.Join(vdir, "pack") // fresh: the timings are of a clean build
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	res, err := sdk.Build(context.Background(), pack, spec, sdk.App{
		Name: name, Project: gen, Sources: srcs,
		Framework: filepath.Join(framework, "include"), Out: out,
	}, tools)
	if err != nil {
		return err
	}

	size := filepath.Join(tools.Bin, spec.ToolPrefix+"size")
	a, err := elfSize(size, filepath.Join(idf, name+".elf"))
	if err != nil {
		return err
	}
	b, err := elfSize(size, res.ELF)
	if err != nil {
		return err
	}
	ia, err := os.ReadFile(filepath.Join(idf, name+".bin"))
	if err != nil {
		return err
	}
	ib, err := os.ReadFile(res.Bin)
	if err != nil {
		return err
	}
	d := sdk.CompareImages(ia, ib)
	img := "identical"
	if !d.Same {
		img = "loaded bytes identical"
	}
	stdout.Printf("  %s files: compile %.2fs  link %.2fs  image %.2fs  (ESP-IDF %.1fs)\n",
		fmt.Sprint(len(srcs)), res.Compile.Seconds(), res.Link.Seconds(), res.Image.Seconds(), tIDF.Seconds())
	if a != b {
		return fmt.Errorf("text/data/bss: ESP-IDF %s, pack %s", a, b)
	}
	if !d.Loaded {
		return fmt.Errorf("app image differs at 0x%x (ESP-IDF %d B, pack %d B)", d.First, len(ia), len(ib))
	}
	stdout.Printf("  [green]ok[/green] text/data/bss %s, image %d B %s\n", a, len(ib), img)
	return nil
}

// elfSize returns an ELF's "text data bss" from binutils size.
func elfSize(size, elf string) (string, error) {
	out, err := exec.Command(size, elf).Output()
	if err != nil {
		return "", fmt.Errorf("size %s: %w", elf, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 3 {
		return "", fmt.Errorf("size %s: unexpected output", elf)
	}
	return strings.Join(f[:3], "/"), nil
}

func writeProvenance(pack string, spec *sdk.Spec, hash, bin string, env []string) error {
	gcc, _ := exec.Command(filepath.Join(bin, spec.ToolPrefix+"gcc"), "--version").Output()
	gccVer, _, _ := strings.Cut(string(gcc), "\n")
	p := map[string]string{
		"chip":           spec.Chip,
		"idf_version":    spec.IDFVersion,
		"compiler":       gccVer,
		"profile_sha256": hash,
		"volt":           version,
		"built_on":       runtime.GOOS + "-" + runtime.GOARCH,
		"built_at":       time.Now().UTC().Format(time.RFC3339),
	}
	if c, err := exec.Command("git", "-C", envValue(env, "IDF_PATH"), "rev-parse", "HEAD").Output(); err == nil {
		p["idf_commit"] = strings.TrimSpace(string(c))
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(pack, "provenance.json"), append(b, '\n'), 0o644)
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return os.Getenv(key)
}

// sameConfig compares two sdkconfig.h files' settings.
func sameConfig(a, b string) error {
	x, err := sdk.ConfigDefines(a)
	if err != nil {
		return err
	}
	y, err := sdk.ConfigDefines(b)
	if err != nil {
		return err
	}
	if strings.Join(x, "\n") != strings.Join(y, "\n") {
		return fmt.Errorf("%s settings differ from the pack's", filepath.Base(a))
	}
	return nil
}

func dirSize(dir string) string {
	var n int64
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}
