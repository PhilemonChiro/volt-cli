// Command volt is the Volt command-line tool: check, compile and format Volt
// programs, manage build toolchains, and serve the IDE worker protocol.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/PhilemonChiro/volt-cli/internal/proc"
	"github.com/PhilemonChiro/volt-cli/internal/term"
	"github.com/PhilemonChiro/volt-compiler"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

type command struct {
	name, args, summary string
	run                 func(args []string) int
}

var commands []command

func init() {
	commands = []command{
		{"check", "[file|dir]", "Type-check and validate hardware without building", runCheck},
		{"compile", "[file|dir] [-o out.c]", "Compile to C (stdout unless -o)", runCompile},
		{"fmt", "[--check] [paths...]", "Format .volt files in place", runFmt},
		{"build", "[dir] [--chip c] [--json] [-v]", "Compile and build firmware into <project>/build/<chip>/", runBuild},
		{"flash", "[dir] [-p port] [--json]", "Flash the last build to a connected board", runFlash},
		{"run", "[dir] [-p port] [--json]", "Build, flash, then monitor the board", runRun},
		{"monitor", "[-p port] [-b baud]", "Show a board's serial output", runMonitor},
		{"ports", "[--json] [--probe]", "List connected boards", runPorts},
		{"cache", "package [chip] -o file.tar.gz", "Package a warmed firmware compiler cache for publishing", runCache},
		{"toolchain", "status|install|repair [chip]", "Check, install or repair a chip's build toolchain", runToolchain},
		{"worker", "", "Serve the IDE worker protocol on stdin/stdout", runWorker},
		{"version", "", "Print the volt version", func([]string) int { fmt.Println("volt", version); return 0 }},
	}
}

var (
	stdout = term.New(os.Stdout)
	stderr = term.New(os.Stderr)
)

func main() {
	proc.HandleSignals()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]
	switch name {
	case "-h", "--help", "help":
		usage()
		return
	case "--version":
		name = "version"
	}
	for _, c := range commands {
		if c.name == name {
			os.Exit(c.run(args))
		}
	}
	stderr.Printf("[red]volt: unknown command %q[/red]\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: volt <command> [options]\n\nCommands:")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'volt <command> -h' for a command's options.")
}

// newFlags returns a FlagSet with the options every compiler command shares.
func newFlags(name, args string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	devices := fs.String("devices", "", "device library `dir` (default $VOLT_DEVICES, then ~/.volt/sdk/devices)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: volt %s %s\n\nOptions:\n", name, args)
		fs.PrintDefaults()
	}
	return fs, devices
}

// parse parses flags that may appear before or after positional arguments.
// It returns the positionals, or ok=false after reporting a usage error.
func parse(fs *flag.FlagSet, args []string) (pos []string, ok bool) {
	for {
		if err := fs.Parse(args); err != nil {
			return nil, false
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, true
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

// useDevices points the compiler at the device library, returning its
// absolute path ("" = the compiler's own default).
func useDevices(dir string) string {
	if dir == "" {
		dir = os.Getenv("VOLT_DEVICES")
	}
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if d := filepath.Join(home, ".volt", "sdk", "devices"); isDir(d) {
				dir = d
			}
		}
	}
	if dir != "" {
		compiler.DevicesDir, _ = filepath.Abs(dir)
	}
	return compiler.DevicesDir
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// findSource resolves a check/compile argument: a .volt file, or a project
// directory holding main.volt (else its first .volt file). Empty means ".".
func findSource(arg string) (string, error) {
	if arg == "" {
		arg = "."
	}
	if !isDir(arg) {
		if _, err := os.Stat(arg); err != nil {
			return "", err
		}
		return arg, nil
	}
	main := filepath.Join(arg, "main.volt")
	if _, err := os.Stat(main); err == nil {
		return main, nil
	}
	found, _ := filepath.Glob(filepath.Join(arg, "*.volt"))
	if len(found) == 0 {
		return "", errors.New("no .volt file in " + arg)
	}
	sort.Strings(found)
	return found[0], nil
}

// compileFile reads and compiles path. err is an I/O or internal compiler
// failure; diagnostics come back in errs.
func compileFile(path string) (code string, errs *compiler.Errors, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	code, errs, err = compiler.Compile(string(src), path)
	if errs == nil {
		errs = &compiler.Errors{}
	}
	return code, errs, err
}

func counts(errs *compiler.Errors) (nerr, nwarn int) {
	if errs == nil {
		return 0, 0
	}
	for _, d := range errs.List {
		if d.Severity == "error" {
			nerr++
		} else {
			nwarn++
		}
	}
	return
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
