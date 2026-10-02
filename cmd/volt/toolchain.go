package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

func runToolchain(args []string) int {
	sub := first(args)
	switch sub {
	case "status", "install", "repair":
	default:
		stderr.Println("Usage: volt toolchain status|install|repair [chip] [--json]")
		if sub == "" || sub == "-h" || sub == "--help" {
			return 0
		}
		return 2
	}
	fl, _ := newFlags("toolchain "+sub, "[chip] [--json]")
	asJSON := fl.Bool("json", false, "print JSON (install/repair: one event per line)")
	pos, ok := parse(fl, args[1:])
	if !ok || len(pos) > 1 {
		return 2
	}
	chip := first(pos)
	if chip == "" {
		chip, _ = projectInfo(".")
	}
	if chip == "" {
		stderr.Println("[red]volt toolchain: no chip given and no volt.toml with a chip here[/red]")
		return 2
	}

	tc, err := toolchain.For(chip, sub == "install")
	if err != nil {
		stderr.Printf("[red]volt toolchain: %s[/red]\n", err)
		return 1
	}
	switch sub {
	case "status":
		return toolchainStatus(tc, *asJSON)
	case "install":
		if tc.Install(toolchain.NewEmitter(*asJSON)) {
			return 0
		}
		return 1
	default:
		return toolchainRepair(tc, *asJSON)
	}
}

var tomlKeyRe = regexp.MustCompile(`(?m)^\s*(chip|name)\s*=\s*"([^"]+)"`)

// projectInfo reads `chip = "…"` and `name = "…"` from dir/volt.toml.
func projectInfo(dir string) (chip, name string) {
	b, _ := os.ReadFile(filepath.Join(dir, "volt.toml"))
	for _, m := range tomlKeyRe.FindAllSubmatch(b, -1) {
		if string(m[1]) == "chip" {
			chip = string(m[2])
		} else {
			name = string(m[2])
		}
	}
	return chip, name
}

func toolchainStatus(tc *toolchain.Toolchain, asJSON bool) int {
	all, ready := tc.Status()
	code := 0
	if !ready {
		code = 1
	}
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"chip": tc.Chip, "platform": toolchain.Platform(), "ready": ready,
			"components": all, "notes": tc.Notes,
		})
		return code
	}

	stdout.Printf("\n[bold]%s toolchain[/bold] [dim]· %s[/dim]\n", tc.Name, toolchain.Platform())
	width, dwidth := 0, 0
	for _, c := range all {
		width, dwidth = max(width, len(c.Name)), max(dwidth, len(c.Detail))
	}
	for _, c := range all {
		mark, color := "✓", "green"
		if !c.OK {
			mark, color = "✗", "red"
		}
		stdout.Printf("  [%s]%s[/%s] %-*s  %-*s", color, mark, color, width, c.Name, dwidth, c.Detail)
		if c.Path != "" {
			stdout.Printf("  [dim]%s[/dim]", toolchain.Tilde(c.Path))
		}
		stdout.Printf("\n")
	}
	for _, n := range tc.Notes {
		stdout.Printf("  [dim]%s[/dim]\n", n)
	}
	if ready {
		stdout.Printf("\n[bold green]✓[/bold green] Ready\n\n")
	} else {
		stdout.Printf("\n[yellow]Not ready.[/yellow] Run: [cyan]volt toolchain install %s[/cyan]\n\n", tc.Chip)
	}
	return code
}

func toolchainRepair(tc *toolchain.Toolchain, asJSON bool) int {
	removed, err := tc.Repair()
	if asJSON {
		out := map[string]any{"removed": removed, "ok": err == nil}
		if err != nil {
			out["error"] = err.Error()
		}
		json.NewEncoder(os.Stdout).Encode(out)
	} else {
		for _, p := range removed {
			stdout.Printf("[green]Removed[/green] %s\n", toolchain.Tilde(p))
		}
		if len(removed) == 0 && err == nil {
			stdout.Println("Nothing to remove.")
		}
		for _, n := range tc.Notes {
			stdout.Printf("[dim]%s[/dim]\n", n)
		}
		if err != nil {
			stderr.Printf("[red]volt toolchain: %s[/red]\n", err)
		} else {
			stdout.Printf("\nRun [cyan]volt toolchain install %s[/cyan] to reinstall.\n", tc.Chip)
		}
	}
	if err != nil {
		return 1
	}
	return 0
}
