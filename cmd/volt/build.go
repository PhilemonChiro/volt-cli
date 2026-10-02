package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PhilemonChiro/volt-cli/internal/build"
	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

// project is the program a build, flash or run command works on.
type project struct {
	src, dir, chip, name string
}

// loadProject resolves the project from a file/dir argument; chip overrides
// volt.toml's chip.
func loadProject(arg, chip string) (project, error) {
	src, err := findSource(arg)
	if err != nil {
		return project{}, err
	}
	src, _ = filepath.Abs(src)
	pr := project{src: src, dir: filepath.Dir(src)}
	pr.chip, pr.name = projectInfo(pr.dir)
	if chip != "" {
		pr.chip = chip
	}
	pr.chip = strings.ToLower(pr.chip)
	if pr.chip == "" {
		return pr, fmt.Errorf("no chip: pass --chip or set chip in %s", filepath.Join(pr.dir, "volt.toml"))
	}
	if pr.name == "" {
		pr.name = filepath.Base(pr.dir)
	}
	pr.name = build.FirmwareName(pr.name)
	return pr, nil
}

// chipEnv returns the build environment for chip, reporting what is missing
// when its toolchain is not ready.
func chipEnv(chip string, r *build.Reporter) ([]string, error) {
	tc, err := toolchain.For(chip, false)
	if err != nil {
		return nil, err
	}
	if all, ready := tc.Status(); !ready {
		for _, c := range all {
			if !c.OK {
				r.Message("error", c.Name+": "+c.Detail)
			}
		}
		return nil, fmt.Errorf("%s toolchain not ready. Run: volt toolchain install %s", tc.Name, chip)
	}
	return tc.BuildEnv()
}

// buildProject compiles pr and builds its firmware, returning the app image.
func buildProject(pr project, devicesFlag string, env []string, asJSON bool, r *build.Reporter) (string, error) {
	devs := useDevices(devicesFlag)
	if devs == "" {
		return "", errors.New("no device library found: pass --devices or set $VOLT_DEVICES")
	}
	framework := filepath.Join(filepath.Dir(devs), "framework")
	if !isDir(framework) {
		return "", fmt.Errorf("no framework next to the device library (%s)", toolchain.Tilde(framework))
	}

	r.Step("compile", "Compiling "+filepath.Base(pr.src)+"...", 5)
	code, errs, err := compileFile(pr.src)
	if err != nil {
		return "", fmt.Errorf("Compile failed: %w", err)
	}
	nerr, _ := counts(errs)
	if len(errs.List) > 0 {
		if asJSON {
			for _, d := range errs.List {
				sev := "warn"
				if d.Severity == "error" {
					sev = "error"
				}
				r.Message(sev, fmt.Sprintf("%s:%d:%d: %s", filepath.Base(pr.src), d.Region.StartLine, d.Region.StartCol, d.Message))
			}
		} else {
			stderr.Println(errs.Report(pr.src))
		}
	}
	if nerr > 0 {
		return "", fmt.Errorf("Compile failed: %s", plural(nerr, "error"))
	}
	r.Step("compile", "Compiled successfully", 25)

	return build.ESP32(build.Project{
		Dir: pr.dir, Name: pr.name, Chip: pr.chip, Code: code,
		Framework: framework, Devices: devs, Env: env,
	}, r)
}

func runBuild(args []string) int {
	fl, devices := newFlags("build", "[dir] [--chip c] [--json] [-v]")
	chip := fl.String("chip", "", "target `chip` (default: volt.toml's chip)")
	asJSON := fl.Bool("json", false, "print progress as JSON lines")
	verbose := fl.Bool("v", false, "show all build tool output")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	r := build.NewReporter(*asJSON, *verbose)

	pr, err := loadProject(first(pos), *chip)
	if err != nil {
		return failed(r, err)
	}
	env, err := chipEnv(pr.chip, r)
	if err != nil {
		return failed(r, err)
	}
	app, err := buildProject(pr, *devices, env, *asJSON, r)
	if err != nil {
		return failed(r, err)
	}
	if !*asJSON {
		app = toolchain.Tilde(app)
	}
	r.Done("Build successful", app)
	return 0
}

func failed(r *build.Reporter, err error) int {
	if errors.Is(err, context.Canceled) {
		r.Fail("Cancelled")
		return 130
	}
	r.Fail(err.Error())
	return 1
}
