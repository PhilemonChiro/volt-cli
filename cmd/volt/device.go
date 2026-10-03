package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/build"
	"github.com/PhilemonChiro/volt-cli/internal/device"
	"github.com/PhilemonChiro/volt-cli/internal/proc"
	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

func runPorts(args []string) int {
	fl, _ := newFlags("ports", "[--json] [--probe]")
	asJSON := fl.Bool("json", false, "print JSON")
	probe := fl.Bool("probe", false, "check each ESP board answers esptool (2 s per board)")
	if pos, ok := parse(fl, args); !ok || len(pos) > 0 {
		return 2
	}
	ports, err := device.List()
	if err != nil {
		stderr.Printf("[red]volt ports: %s[/red]\n", err)
		return 1
	}
	if *probe {
		for i, p := range ports {
			if p.State != "running" || !strings.HasPrefix(p.Chip, "ESP") {
				continue
			}
			ports[i].Probe = device.ProbeESP(p.Port)
		}
	}

	if *asJSON {
		if ports == nil {
			ports = []device.Port{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(ports)
		return 0
	}
	if len(ports) == 0 {
		stdout.Println("No boards connected.")
		return 0
	}
	for _, p := range ports {
		stdout.Printf("  [bold]%-28s[/bold] %-9s [dim]%s:%s  %s[/dim]", p.Port, p.Chip, p.VendorID, p.ProductID, p.Desc)
		if p.State == "bootsel" {
			stdout.Printf(" [yellow](BOOTSEL)[/yellow]")
		}
		switch p.Probe {
		case "verified":
			stdout.Printf(" [green]✓ answers[/green]")
		case "silent":
			stdout.Printf(" [yellow]silent[/yellow]")
		}
		stdout.Printf("\n")
	}
	return 0
}

// selectPort picks the board to use: the -p port, else the only connected
// board for chip ("" = any chip). It never guesses between several.
func selectPort(flag, chip string) (device.Port, error) {
	ports, err := device.List()
	if err != nil {
		return device.Port{}, err
	}
	if flag != "" {
		for _, p := range ports {
			if p.Port == flag {
				return p, nil
			}
		}
		return device.Port{Port: flag, State: "running"}, nil
	}
	var found []device.Port
	for _, p := range ports {
		if p.State == "running" && (chip == "" || p.Matches(chip)) {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	what := "board"
	if chip != "" {
		what = strings.ToUpper(chip) + " board"
	}
	var b strings.Builder
	if len(found) == 0 {
		fmt.Fprintf(&b, "no %s connected", what)
		if len(ports) > 0 {
			b.WriteString("; connected:")
			for _, p := range ports {
				fmt.Fprintf(&b, "\n  %s (%s)", p.Port, p.Chip)
			}
			b.WriteString("\npass -p <port> to use one of these")
		}
	} else {
		fmt.Fprintf(&b, "%d %ss connected; pass -p with one of:", len(found), what)
		for _, p := range found {
			fmt.Fprintf(&b, "\n  %s (%s)", p.Port, p.Desc)
		}
	}
	return device.Port{}, fmt.Errorf("%s", b.String())
}

func runFlash(args []string) int {
	fl, _ := newFlags("flash", "[dir] [-p port] [--chip c] [--full] [--flash-baud n] [--json]")
	full := fl.Bool("full", false, "write every image, even ones the board already has")
	flashBaud := fl.Int("flash-baud", build.DefaultFlashBaud, "flashing `baud` rate (lower rates are tried if writing fails)")
	portFlag := fl.String("p", "", "serial `port` (default: the only connected board for the chip)")
	chip := fl.String("chip", "", "target `chip` (default: volt.toml's chip)")
	asJSON := fl.Bool("json", false, "print progress as JSON lines")
	verbose := fl.Bool("v", false, "show all flash tool output")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	r := build.NewReporter(*asJSON, *verbose)
	pr, err := loadProject(first(pos), *chip)
	if err != nil {
		return failed(r, err)
	}
	app := build.Artifact(pr.dir, pr.chip, pr.name)
	if app == "" {
		return failed(r, fmt.Errorf("nothing built for %s yet; run volt build (or volt run)", pr.chip))
	}
	if src, bin := modTime(pr.src), modTime(app); src.After(bin) {
		r.Message("warn", "main.volt changed since the last build; flashing the old firmware (volt run builds first)")
	}
	env, err := chipEnv(pr.chip, r)
	if err != nil {
		return failed(r, err)
	}
	board, err := selectPort(*portFlag, pr.chip)
	if err != nil {
		return failed(r, err)
	}
	if err := build.FlashESP32(pr.dir, pr.chip, board.Port, *flashBaud, *full, env, r); err != nil {
		return failed(r, err)
	}
	r.Done("Flashed "+board.Port, app)
	return 0
}

func runMonitor(args []string) int {
	fl, _ := newFlags("monitor", "[-p port] [-b baud] [--chip c] [--elf file] [--no-decode] [--json]")
	portFlag := fl.String("p", "", "serial `port` (default: the only connected board)")
	baud := fl.Int("b", 0, "`baud` rate (default: serial.defaultBaud setting, else 115200)")
	chip := fl.String("chip", "", "only consider boards of this `chip` (default: volt.toml's chip)")
	asJSON := fl.Bool("json", false, `print {"line": …} JSON lines`)
	elfFlag := fl.String("elf", "", "decode backtraces with this `ELF` (default: the project's last build)")
	noDecode := fl.Bool("no-decode", false, "don't decode panic backtraces")
	if pos, ok := parse(fl, args); !ok || len(pos) > 0 {
		return 2
	}
	c := *chip
	if c == "" {
		c, _ = projectInfo(".")
	}
	board, err := selectPort(*portFlag, strings.ToLower(c))
	if err != nil {
		stderr.Printf("[red]volt monitor: %s[/red]\n", err)
		return 1
	}
	var dec *device.Decoder
	if !*noDecode {
		dir := "."
		if *elfFlag != "" {
			dir = ""
		}
		var env []string
		if tc, err := toolchain.For(strings.ToLower(c), false); err == nil {
			env, _ = tc.BuildEnv()
		}
		elf, a2l := build.CrashTools(dir, strings.ToLower(c), env)
		if *elfFlag != "" {
			elf = *elfFlag
		}
		dec = device.NewDecoder(elf, a2l)
	}
	if err := monitor(board.Port, baudFor(*baud, c), *asJSON, dec); err != nil {
		stderr.Printf("[red]volt monitor: %s[/red]\n", err)
		return 1
	}
	return 0
}

func baudFor(flag int, chip string) int {
	switch {
	case flag > 0:
		return flag
	case toolchain.SettingInt("serial.defaultBaud") > 0:
		return toolchain.SettingInt("serial.defaultBaud")
	case strings.Contains(strings.ToLower(chip), "8266"):
		return 74880
	}
	return 115200
}

// monitor streams port to stdout until Ctrl-C/SIGTERM. With asJSON it
// prints {"line"} objects between {"event":"connected"} and
// {"event":"disconnected"}, with {"event":"error"} before the latter if the
// port went away or could not be opened.
//
// dec, when set, decodes panic backtraces: each code address is followed by
// its function and source line (in JSON, a {"backtrace": [...]} object).
func monitor(port string, baud int, asJSON bool, dec *device.Decoder) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	event := func(name, msg string) {
		if !asJSON {
			return
		}
		e := map[string]any{"event": name, "port": port}
		if msg != "" {
			e["message"] = msg
		}
		enc.Encode(e)
	}
	err := device.Monitor(proc.Root, port, baud, func() {
		event("connected", "")
		if !asJSON {
			stderr.Printf("[dim]Monitoring %s at %d baud — Ctrl-C to exit[/dim]\n", port, baud)
		}
	}, func(line string) {
		if asJSON {
			enc.Encode(map[string]string{"line": line})
		} else {
			fmt.Println(line)
		}
		frames, warning := dec.Line(line)
		if warning != "" {
			if asJSON {
				enc.Encode(map[string]string{"event": "warning", "message": warning})
			} else {
				stderr.Printf("[yellow]%s[/yellow]\n", warning)
			}
		}
		if len(frames) == 0 {
			return
		}
		if asJSON {
			enc.Encode(map[string]any{"backtrace": frames})
			return
		}
		stdout.Println("[yellow]Decoded backtrace:[/yellow]")
		for i, f := range frames {
			where := "[dim]??[/dim]"
			if f.Resolved {
				where = fmt.Sprintf("%s [dim]at[/dim] %s:%d", f.Func, filepath.Base(f.File), f.Line)
			}
			if f.Inlined {
				stdout.Printf("[yellow]      (inlined by) %s[/yellow]\n", where)
			} else {
				stdout.Printf("[yellow]  #%-2d %s %s[/yellow]\n", i, f.PC, where)
			}
		}
	})
	if err != nil {
		msg := err.Error()
		if errors.Is(err, device.ErrLost) {
			msg = "Connection lost"
		}
		event("error", msg)
	}
	event("disconnected", "")
	return err
}

func runRun(args []string) int {
	fl, devices := newFlags("run", "[dir] [-p port] [--chip c] [--optimize o] [--full] [--flash-baud n] [--json] [-v]")
	full := fl.Bool("full", false, "write every image, even ones the board already has")
	flashBaud := fl.Int("flash-baud", build.DefaultFlashBaud, "flashing `baud` rate (lower rates are tried if writing fails)")
	portFlag := fl.String("p", "", "serial `port` (default: the only connected board for the chip)")
	chip := fl.String("chip", "", "target `chip` (default: volt.toml's chip)")
	baud := fl.Int("b", 0, "monitor `baud` rate (default: serial.defaultBaud setting, else 115200)")
	asJSON := fl.Bool("json", false, "print progress (then monitor lines) as JSON lines")
	verbose := fl.Bool("v", false, "show all build and flash tool output")
	optimize := fl.String("optimize", "", "compiler optimization: debug, size or speed (default: volt.toml's optimize, else the SDK pack's)")
	pos, ok := parse(fl, args)
	if !ok || len(pos) > 1 {
		return 2
	}
	r := build.NewReporter(*asJSON, *verbose)
	pr, err := loadProject(first(pos), *chip)
	if err == nil {
		err = applyOptimize(&pr, *optimize)
	}
	if err != nil {
		return failed(r, err)
	}
	env, err := chipEnv(pr.chip, r)
	if err != nil {
		return failed(r, err)
	}
	board, err := selectPort(*portFlag, pr.chip) // before building: fail fast
	if err != nil {
		return failed(r, err)
	}
	app, err := buildProject(pr, *devices, env, *asJSON, r)
	if err != nil {
		return failed(r, err)
	}
	r.Size = build.ESP32Size(app, pr.chip)
	if err := build.FlashESP32(pr.dir, pr.chip, board.Port, *flashBaud, *full, env, r); err != nil {
		return failed(r, err)
	}

	// The reset after flashing can re-enumerate the board under a new name.
	r.Step("connect", "Waiting for device...", 96)
	port := board.Port
	if board.VendorID != "" {
		time.Sleep(500 * time.Millisecond)
		port = device.Wait(board, 8*time.Second)
	}
	if port == "" {
		r.Step("connect", "Reconnect manually", 98)
		r.Done("Flashed; board did not reappear", board.Port)
		return 0
	}
	r.Step("connect", "Device on "+port, 98)
	r.Done("Running on device", port)
	elf, a2l := build.CrashTools(pr.dir, pr.chip, env)
	if err := monitor(port, baudFor(*baud, pr.chip), *asJSON, device.NewDecoder(elf, a2l)); err != nil {
		return failed(r, err)
	}
	return 0
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}
