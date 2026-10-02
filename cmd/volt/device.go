package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
		var env []string
		for i, p := range ports {
			if p.State != "running" || !strings.HasPrefix(p.Chip, "ESP") {
				continue
			}
			if env == nil {
				if env, err = espEnv(); err != nil {
					stderr.Printf("[red]volt ports: can't probe: %s[/red]\n", err)
					return 1
				}
			}
			ports[i].Probe = device.ProbeESP(p.Port, env)
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

// espEnv is the ESP-IDF environment, for esptool.
func espEnv() ([]string, error) {
	tc, err := toolchain.For("esp32", false)
	if err != nil {
		return nil, err
	}
	if _, ready := tc.Status(); !ready {
		return nil, fmt.Errorf("ESP32 toolchain not ready. Run: volt toolchain install esp32")
	}
	return tc.BuildEnv()
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
	fl, _ := newFlags("flash", "[dir] [-p port] [--chip c] [--json]")
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
	if err := build.FlashESP32(pr.dir, pr.chip, board.Port, env, r); err != nil {
		return failed(r, err)
	}
	r.Done("Flashed "+board.Port, app)
	return 0
}

func runMonitor(args []string) int {
	fl, _ := newFlags("monitor", "[-p port] [-b baud] [--chip c] [--json]")
	portFlag := fl.String("p", "", "serial `port` (default: the only connected board)")
	baud := fl.Int("b", 0, "`baud` rate (default: serial.defaultBaud setting, else 115200)")
	chip := fl.String("chip", "", "only consider boards of this `chip` (default: volt.toml's chip)")
	asJSON := fl.Bool("json", false, `print {"line": …} JSON lines`)
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
	if err := monitor(board.Port, baudFor(*baud, c), *asJSON); err != nil {
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
func monitor(port string, baud int, asJSON bool) error {
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
	fl, devices := newFlags("run", "[dir] [-p port] [--chip c] [--json] [-v]")
	portFlag := fl.String("p", "", "serial `port` (default: the only connected board for the chip)")
	chip := fl.String("chip", "", "target `chip` (default: volt.toml's chip)")
	baud := fl.Int("b", 0, "monitor `baud` rate (default: serial.defaultBaud setting, else 115200)")
	asJSON := fl.Bool("json", false, "print progress (then monitor lines) as JSON lines")
	verbose := fl.Bool("v", false, "show all build and flash tool output")
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
	board, err := selectPort(*portFlag, pr.chip) // before building: fail fast
	if err != nil {
		return failed(r, err)
	}
	if _, err := buildProject(pr, *devices, env, *asJSON, r); err != nil {
		return failed(r, err)
	}
	if err := build.FlashESP32(pr.dir, pr.chip, board.Port, env, r); err != nil {
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
	if err := monitor(port, baudFor(*baud, pr.chip), *asJSON); err != nil {
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
