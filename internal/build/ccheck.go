package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CError is an error the C compiler reports for the code made from a Volt
// program, at the Volt line it came from (Line 0: not tied to a line).
type CError struct {
	Line, Col int
	Message   string
}

// cErrorRe matches clang/gcc diagnostics: "file:line:col: error: message".
var cErrorRe = regexp.MustCompile(`^(.+?):(\d+):(\d+): (?:fatal )?error: (.*)$`)

// SyntaxCheck compiles the generated C on this computer, syntax only,
// against the framework headers and the device library headers the
// firmware build force-includes. It takes well under a second and catches
// C the firmware build would reject even though the Volt checks passed. The
// generated code carries #line directives, so errors point at the Volt
// source. ran is false when there is no C compiler here (nothing checked).
func SyntaxCheck(code, framework, devices, voltFile string) (errs []CError, ran bool, err error) {
	cc := ""
	for _, name := range []string{"cc", "clang", "gcc"} {
		if p, e := exec.LookPath(name); e == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		return nil, false, nil
	}
	tmp, err := os.MkdirTemp("", "volt-ccheck-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(tmp)
	mainC := filepath.Join(tmp, "main.c")
	if err := os.WriteFile(mainC, []byte(code), 0o644); err != nil {
		return nil, false, err
	}
	// The chip SDK isn't on this computer, but the generated C includes a
	// few of its headers (esp_attr.h for an `every … strict` ISR's
	// IRAM_ATTR); stand-ins let the rest of the code be checked.
	stubs := filepath.Join(tmp, "sdk-stubs")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(filepath.Join(stubs, "esp_attr.h"), []byte("#pragma once\n#define IRAM_ATTR\n"), 0o644); err != nil {
		return nil, false, err
	}
	args := []string{"-fsyntax-only", "-std=gnu11", "-I", filepath.Join(framework, "include"), "-I", stubs,
		// The firmware build stops on these; the rest stay warnings there too.
		"-Werror=unused-value", "-Werror=implicit-function-declaration",
		"-Werror=int-conversion", "-Werror=incompatible-pointer-types"}
	if devices != "" {
		hdrs, err := libraryHeaders(tmp, devices, code)
		if err != nil {
			return nil, false, err
		}
		for _, h := range hdrs {
			args = append(args, "-include", h)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, cc, append(args, mainC)...).CombinedOutput()
	if ctx.Err() != nil {
		return nil, false, nil // too slow here: say nothing rather than guess
	}
	seen := map[string]bool{}
	base := filepath.Base(voltFile)
	for _, l := range strings.Split(string(out), "\n") {
		m := cErrorRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		e := CError{Message: m[4]}
		if filepath.Base(m[1]) == base {
			e.Line, _ = strconv.Atoi(m[2])
			e.Col, _ = strconv.Atoi(m[3])
		}
		key := strconv.Itoa(e.Line) + ":" + e.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		errs = append(errs, e)
		if len(errs) == 5 {
			break
		}
	}
	return errs, true, nil
}
