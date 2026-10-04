package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/esptool"
)

// App is one firmware to build with a pack.
type App struct {
	Name      string   // app name; output is <Out>/<Name>.{elf,bin,map}
	Version   string   // PROJECT_VER in the app descriptor ("1" if empty)
	Project   string   // generated project dir, mapped to "." in __FILE__
	Sources   []string // Volt's C files in ESP-IDF's libmain.a order: main.c, runtime, drivers
	Framework string   // Volt framework include dir
	Out       string
	Optimize  string // replaces the pack's -O flag for Volt's sources ("" keeps it)
}

// OptimizeFlags maps volt.toml's optimize setting to GCC flags.
var OptimizeFlags = map[string]string{"debug": "-Og", "size": "-Os", "speed": "-O2"}

// Tools are the host programs a pack build runs.
type Tools struct {
	Bin string    // compiler bin dir (holds <tool_prefix>gcc)
	Log io.Writer // optional: every command and its output
}

// Result is a finished pack build.
type Result struct {
	ELF, Bin, Map        string
	Units, Compiled      int      // C files, and how many were out of date
	Linked               bool     // false: everything was up to date
	Warnings             []string // compiler output of successful compiles
	Compile, Link, Image time.Duration
}

// Build compiles app's sources with the pack's flags, links them against the
// pack and makes the app image. Work is incremental: an object is rebuilt
// only when its compiler, arguments, source or any header it included
// changed, and the ELF and image only when an object or the link line did.
func Build(ctx context.Context, pack string, s *Spec, app App, t Tools) (*Result, error) {
	pack, _ = filepath.Abs(pack)
	if app.Version == "" {
		app.Version = "1"
	}
	obj := filepath.Join(app.Out, "obj")
	if err := os.MkdirAll(obj, 0o755); err != nil {
		return nil, err
	}
	res := &Result{
		ELF: filepath.Join(app.Out, app.Name+".elf"),
		Bin: filepath.Join(app.Out, app.Name+".bin"),
		Map: filepath.Join(app.Out, app.Name+".map"),
	}
	appLib := filepath.Join(app.Out, "libmain.a")
	descLib := filepath.Join(app.Out, "libesp_app_format.a")
	rep := strings.NewReplacer("{pack}", pack, "{project}", app.Project, "{framework}", app.Framework,
		"{name}", truncate(app.Name, 31), "{version}", truncate(app.Version, 31),
		"{map}", res.Map, "{app}", appLib, "{app_desc}", descLib)
	tool := func(name string) string { return filepath.Join(t.Bin, s.ToolPrefix+name) }
	r := &runner{ctx: ctx, dir: app.Out, log: t.Log}

	// Compile: Volt's files plus the app descriptor, in parallel. Each object
	// gets its own dir, named after its source path, so archive members keep
	// ESP-IDF's names (drivers are all driver.c.obj).
	start := time.Now()
	cflags := expand(s.CFlags, rep)
	if app.Optimize != "" {
		cflags = withOptimize(cflags, app.Optimize)
	}
	for _, inc := range s.Includes {
		cflags = append(cflags, "-I"+rep.Replace(inc))
	}
	type unit struct {
		src, obj string
		args     []string
		built    bool
	}
	var units []*unit
	for _, src := range app.Sources {
		units = append(units, &unit{src: src, obj: objPath(obj, src), args: cflags})
	}
	desc := rep.Replace(s.AppDesc.Src)
	units = append(units, &unit{src: desc, obj: filepath.Join(obj, "desc", filepath.Base(desc)+".obj"), args: expand(s.AppDesc.Args, rep)})
	res.Units = len(units)

	errs := make([]error, len(units))
	var mu sync.Mutex
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, u := range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			args := append(append([]string{}, u.args...), "-MD", "-MF", u.obj+".d", "-o", u.obj, "-c", u.src)
			key, err := unitKey(tool("gcc"), args, u.src)
			if err != nil {
				errs[i] = err
				return
			}
			if upToDate(u.obj, key) {
				return
			}
			if err := os.MkdirAll(filepath.Dir(u.obj), 0o755); err != nil {
				errs[i] = err
				return
			}
			out, err := r.run(tool("gcc"), args...)
			if err != nil {
				errs[i] = err
				return
			}
			u.built = true
			mu.Lock()
			if out = strings.TrimSpace(out); out != "" {
				res.Warnings = append(res.Warnings, strings.Split(out, "\n")...)
			}
			mu.Unlock()
			errs[i] = writeStamp(u.obj, key)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	var objs []string
	appBuilt := false
	for _, u := range units[:len(units)-1] {
		objs = append(objs, u.obj)
		appBuilt = appBuilt || u.built
	}
	descUnit := units[len(units)-1]
	for _, u := range units {
		if u.built {
			res.Compiled++
		}
	}
	relink := false
	for _, a := range []struct {
		lib   string
		objs  []string
		built bool
	}{{appLib, objs, appBuilt}, {descLib, []string{descUnit.obj}, descUnit.built}} {
		if a.built || !exists(a.lib) {
			if err := archive(r, tool, a.lib, a.objs); err != nil {
				return nil, err
			}
			relink = true
		}
	}
	res.Compile = time.Since(start)

	// Link: ESP-IDF's own link line with the pack's paths.
	args := expand(s.Link.Flags, rep)
	args = append(args, expand(s.Link.LDFlags, rep)...)
	args = append(args, rep.Replace(s.Link.ElfSrc), "-o", res.ELF)
	args = append(args, expand(s.Link.LibPath, rep)...)
	args = append(args, expand(s.Link.Libs, rep)...)
	linkKey := hashStrings(append([]string{tool("g++")}, args...))
	imgOpts, err := esptool.ParseElf2ImageArgs(s.Elf2Image)
	if err != nil {
		return nil, err
	}
	imgKey := hashStrings(append([]string{"elf2image-go-1"}, s.Elf2Image...))
	if relink || !upToDate(res.ELF, linkKey) || !upToDate(res.Bin, imgKey) {
		start = time.Now()
		if _, err := r.run(tool("g++"), args...); err != nil {
			return nil, err
		}
		res.Link = time.Since(start)

		start = time.Now()
		elf, err := os.ReadFile(res.ELF)
		if err != nil {
			return nil, err
		}
		img, err := esptool.Elf2Image(elf, imgOpts)
		if err != nil {
			return nil, fmt.Errorf("elf2image: %w", err)
		}
		if err := os.WriteFile(res.Bin, img, 0o644); err != nil {
			return nil, err
		}
		res.Image = time.Since(start)
		res.Linked = true
		// The stamps record no deps: archives and the ELF change only through
		// this function, which then relinks anyway.
		if err := writeStamp(res.ELF, linkKey); err != nil {
			return nil, err
		}
		if err := writeStamp(res.Bin, imgKey); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// objPath names a source's object: <obj>/<hash of its path>/<base>.obj.
func objPath(obj, src string) string {
	sum := sha256.Sum256([]byte(src))
	return filepath.Join(obj, hex.EncodeToString(sum[:6]), filepath.Base(src)+".obj")
}

// stamp sits next to an output (<out>.stamp): the key it was made with, and
// the size and time of every file it was made from (from the compiler's
// <out>.d), checked on the next build.
type stamp struct {
	Key  string `json:"key"`
	Deps []dep  `json:"deps,omitempty"`
}

type dep struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

func unitKey(gcc string, args []string, src string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hashStrings(append(append([]string{gcc}, args...), hex.EncodeToString(sum[:]))), nil
}

func hashStrings(ss []string) string {
	sum := sha256.Sum256([]byte(strings.Join(ss, "\x00")))
	return hex.EncodeToString(sum[:])
}

func upToDate(out, key string) bool {
	if !exists(out) {
		return false
	}
	var st stamp
	if readJSON(out+".stamp", &st) != nil || st.Key != key {
		return false
	}
	for _, d := range st.Deps {
		fi, err := os.Stat(d.Path)
		if err != nil || fi.Size() != d.Size || fi.ModTime().UnixNano() != d.MTime {
			return false
		}
	}
	return true
}

func writeStamp(out, key string) error {
	st := stamp{Key: key}
	if b, err := os.ReadFile(out + ".d"); err == nil {
		for _, p := range depFiles(string(b)) {
			fi, err := os.Stat(p)
			if err != nil {
				return err
			}
			st.Deps = append(st.Deps, dep{p, fi.Size(), fi.ModTime().UnixNano()})
		}
	}
	return writeJSON(out+".stamp", st)
}

// depFiles returns the prerequisites in a compiler's make-style .d file.
func depFiles(d string) []string {
	d = strings.ReplaceAll(d, "\\\r\n", " ")
	d = strings.ReplaceAll(d, "\\\n", " ")
	var files []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			files = append(files, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(d); i++ {
		switch c := d[i]; {
		case c == '\\' && i+1 < len(d) && d[i+1] == ' ':
			cur.WriteByte(' ')
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	// Drop the target ("x.obj:"), which ends in a colon.
	for i, f := range files {
		if strings.HasSuffix(f, ":") {
			return files[i+1:]
		}
	}
	return files
}

// archive makes a fresh archive the way CMake does (ar qc, then ranlib).
func archive(r *runner, tool func(string) string, lib string, objs []string) error {
	if err := os.Remove(lib); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := r.run(tool("ar"), append([]string{"qc", lib}, objs...)...); err != nil {
		return err
	}
	_, err := r.run(tool("ranlib"), lib)
	return err
}

type runner struct {
	ctx context.Context
	dir string
	log io.Writer
	mu  sync.Mutex
}

// run runs a tool, returning its combined output; a failure's error holds
// the output too.
func (r *runner) run(name string, args ...string) (string, error) {
	cmdArgs := args
	if n := argsLen(args); n > maxCmdLine {
		rsp, err := responseFile(args)
		if err != nil {
			return "", err
		}
		defer os.Remove(rsp)
		cmdArgs = []string{"@" + rsp}
	}
	cmd := exec.CommandContext(r.ctx, name, cmdArgs...)
	cmd.Dir = r.dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if r.log != nil {
		r.mu.Lock()
		fmt.Fprintf(r.log, "$ %s %s\n%s", filepath.Base(name), strings.Join(args, " "), out.String())
		r.mu.Unlock()
	}
	if err != nil {
		if r.ctx.Err() != nil {
			return "", r.ctx.Err()
		}
		msg := strings.TrimSpace(out.String())
		if len(msg) > 4000 {
			msg = "…" + msg[len(msg)-4000:]
		}
		return "", fmt.Errorf("%s failed: %w\n%s", filepath.Base(name), err, msg)
	}
	return out.String(), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// withOptimize swaps every -O flag in flags for opt (appending it if none).
func withOptimize(flags []string, opt string) []string {
	out := make([]string, 0, len(flags)+1)
	for _, f := range flags {
		if len(f) >= 2 && f[:2] == "-O" {
			continue
		}
		out = append(out, f)
	}
	return append(out, opt)
}

// maxCmdLine keeps tool command lines under Windows' 32K CreateProcess limit;
// longer argument lists go through a GCC/binutils @response file.
const maxCmdLine = 24000

func argsLen(args []string) int {
	n := 0
	for _, a := range args {
		n += len(a) + 3
	}
	return n
}

// responseFile writes args one per line, quoted for libiberty's buildargv
// (backslash escapes, so Windows paths need theirs doubled).
func responseFile(args []string) (string, error) {
	f, err := os.CreateTemp("", "volt-*.rsp")
	if err != nil {
		return "", err
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for _, a := range args {
		fmt.Fprintf(f, "\"%s\"\n", q.Replace(a))
	}
	return f.Name(), f.Close()
}
