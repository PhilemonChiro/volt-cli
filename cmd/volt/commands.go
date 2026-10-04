package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-compiler"
	"github.com/PhilemonChiro/volt-compiler/worker"
)

func runCheck(args []string) int {
	fl, devices := newFlags("check", "[file|dir] [--json]")
	asJSON := fl.Bool("json", false, "print diagnostics as JSON")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	useDevices(*devices)
	path, err := findSource(first(pos))
	if err != nil {
		stderr.Printf("[red]volt check: %s[/red]\n", err)
		return 1
	}

	t0 := time.Now()
	_, errs, err := compileFile(path)
	elapsed := time.Since(t0)
	if err != nil {
		stderr.Printf("[red]volt check: %s[/red]\n", err)
		return 1
	}
	nerr, nwarn := counts(errs)

	if *asJSON {
		type fix struct {
			Title   string `json:"title"`
			Line    int    `json:"line"`
			Col     int    `json:"col"`
			EndLine int    `json:"end_line"`
			EndCol  int    `json:"end_col"`
			Text    string `json:"text"`
		}
		type diag struct {
			Line     int    `json:"line"`
			Col      int    `json:"col"`
			EndLine  int    `json:"end_line"`
			EndCol   int    `json:"end_col"`
			Severity string `json:"severity"`
			Category string `json:"category"`
			Message  string `json:"message"`
			Hint     string `json:"hint,omitempty"`
			Code     string `json:"code,omitempty"`
			Fixes    []fix  `json:"fixes,omitempty"`
		}
		out := struct {
			File        string `json:"file"`
			OK          bool   `json:"ok"`
			Errors      int    `json:"errors"`
			Warnings    int    `json:"warnings"`
			Diagnostics []diag `json:"diagnostics"`
		}{path, nerr == 0, nerr, nwarn, []diag{}}
		if errs != nil {
			for _, d := range errs.List {
				r := d.Region
				dd := diag{r.StartLine, r.StartCol, r.EndLine, r.EndCol, d.Severity, d.Category, d.Message, d.Hint, d.Code, nil}
				for _, f := range d.Fixes {
					fr := f.Region
					dd.Fixes = append(dd.Fixes, fix{f.Title, fr.StartLine, fr.StartCol, fr.EndLine, fr.EndCol, f.Text})
				}
				out.Diagnostics = append(out.Diagnostics, dd)
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		stdout.Printf("\n[bold cyan]→[/bold cyan] Checking [bold]%s[/bold]\n", path)
		if len(errs.List) > 0 {
			stdout.Println(errs.Report(path))
		}
		if nerr > 0 {
			stdout.Printf("\n[red]✗ %s found.[/red]\n\n", plural(nerr, "error"))
		} else {
			stdout.Printf("[bold green]✓[/bold green] Check passed in %s", fmtTime(elapsed))
			if nwarn > 0 {
				stdout.Printf(" [yellow](%s)[/yellow]", plural(nwarn, "warning"))
			}
			stdout.Printf("\n\n")
		}
	}
	if nerr > 0 {
		return 1
	}
	return 0
}

func runCompile(args []string) int {
	fl, devices := newFlags("compile", "[file|dir] [-o out.c]")
	out := fl.String("o", "", "write C to `file` instead of stdout")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	useDevices(*devices)
	path, err := findSource(first(pos))
	if err != nil {
		stderr.Printf("[red]volt compile: %s[/red]\n", err)
		return 1
	}

	code, errs, err := compileFile(path)
	if err != nil {
		stderr.Printf("[red]volt compile: %s[/red]\n", err)
		return 1
	}
	if len(errs.List) > 0 {
		stderr.Println(errs.Report(path))
	}
	if nerr, _ := counts(errs); nerr > 0 {
		stderr.Printf("\n[red]✗ %s found.[/red]\n\n", plural(nerr, "error"))
		return 1
	}

	if *out == "" {
		fmt.Print(code)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err == nil {
		err = os.WriteFile(*out, []byte(code), 0o644)
	}
	if err != nil {
		stderr.Printf("[red]volt compile: %s[/red]\n", err)
		return 1
	}
	stderr.Printf("[bold green]✓[/bold green] Wrote %s\n", *out)
	return 0
}

func runFmt(args []string) int {
	fl, _ := newFlags("fmt", "[--check] [paths...]")
	checkOnly := fl.Bool("check", false, "list files that need formatting; change nothing")
	pos, ok := parse(fl, args)
	if !ok {
		return 2
	}
	if len(pos) == 0 {
		pos = []string{"."}
	}

	var targets []string
	for _, p := range pos {
		if !isDir(p) {
			targets = append(targets, p)
			continue
		}
		filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && path != p && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".volt") {
				targets = append(targets, path)
			}
			return nil
		})
	}
	if len(targets) == 0 {
		stdout.Println("[yellow]No .volt files found.[/yellow]")
		return 0
	}

	changed, failed := 0, 0
	for _, path := range targets {
		src, err := os.ReadFile(path)
		var formatted string
		if err == nil {
			formatted, err = compiler.Format(string(src))
		}
		if err != nil {
			stderr.Printf("[red]%s: %s[/red]\n", path, err)
			failed++
			continue
		}
		if formatted == string(src) {
			continue
		}
		changed++
		if *checkOnly {
			stdout.Printf("[yellow]Would format:[/yellow] %s\n", path)
		} else if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
			stderr.Printf("[red]%s: %s[/red]\n", path, err)
			failed++
		} else {
			stdout.Printf("[green]Formatted:[/green] %s\n", path)
		}
	}

	switch {
	case *checkOnly && changed > 0:
		stdout.Printf("\n[yellow]%s need formatting.[/yellow]\n", plural(changed, "file"))
	case changed > 0:
		stdout.Printf("\n[green]✓[/green] Formatted %s.\n", plural(changed, "file"))
	case failed == 0:
		stdout.Printf("[green]✓[/green] All %s already formatted.\n", plural(len(targets), "file"))
	}
	if failed > 0 || (*checkOnly && changed > 0) {
		return 1
	}
	return 0
}

func runSpec(args []string) int {
	fl, devices := newFlags("spec", "")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 0 {
		return 2
	}
	useDevices(*devices)
	fmt.Print(compiler.LanguageSpec())
	return 0
}

func runWorker(args []string) int {
	fl, devices := newFlags("worker", "")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 0 {
		return 2
	}
	useDevices(*devices)
	if err := worker.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "volt worker:", err)
		return 1
	}
	return 0
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func fmtTime(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
