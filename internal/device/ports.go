// Package device finds connected boards and talks to them over serial.
package device

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.bug.st/serial/enumerator"

	"github.com/PhilemonChiro/volt-cli/internal/esptool"
)

// Port is one connected board. The JSON shape matches the IDE's
// scan_devices entries.
type Port struct {
	Port      string `json:"port"` // serial device, or mount path when bootsel
	Chip      string `json:"chip"` // ESP32, ESP32-S3, RP2040, RP2350, STM32, Unknown
	Desc      string `json:"desc"`
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
	Serial    string `json:"serialNumber,omitempty"`
	State     string `json:"state"`           // running (serial) or bootsel (mass storage)
	Probe     string `json:"probe,omitempty"` // ESP only, with --probe: verified or silent
}

// Matches reports whether the port's detected chip is chip (volt.toml name).
func (p Port) Matches(chip string) bool {
	return strings.EqualFold(strings.ReplaceAll(p.Chip, "-", ""), chip)
}

// SameBoard reports whether q is the same physical board as p, e.g. after it
// re-enumerated under another name.
func (p Port) SameBoard(q Port) bool {
	return p.VendorID == q.VendorID && p.ProductID == q.ProductID && p.Serial == q.Serial
}

var ttySRe = regexp.MustCompile(`/dev/ttyS\d+$`)

// List returns connected boards: USB serial ports, then Picos in BOOTSEL.
func List() ([]Port, error) {
	var out []Port
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	for _, p := range ports {
		vid, pid := strings.ToLower(p.VID), strings.ToLower(p.PID)
		if vid == "" || vid == "8086" || ttySRe.MatchString(p.Name) && p.Manufacturer == "" {
			continue
		}
		desc := p.Manufacturer
		if desc == "" {
			desc = p.Product
		}
		if desc == "" {
			desc = p.Name
		}
		out = append(out, Port{Port: p.Name, Chip: detectChip(vid, pid, p.Manufacturer),
			Desc: desc, VendorID: vid, ProductID: pid, Serial: p.SerialNumber, State: "running"})
	}
	for _, v := range bootselVolumes() {
		chip := "RP2040"
		if strings.Contains(v, "RP2350") {
			chip = "RP2350"
		}
		out = append(out, Port{Port: v, Chip: chip, Desc: "BOOTSEL mode @ " + v,
			VendorID: "2e8a", ProductID: "0003", State: "bootsel"})
	}
	return out, nil
}

// detectChip guesses the chip from the USB bridge's VID:PID.
func detectChip(vid, pid, mfr string) string {
	mfr = strings.ToLower(mfr)
	switch {
	case vid == "303a":
		return "ESP32-S3"
	case vid == "10c4" && pid == "ea60", vid == "1a86" && pid == "7523", vid == "1a86" && pid == "55d4":
		return "ESP32" // CP210x, CH340, CH9102 bridges
	case vid == "2e8a":
		return "RP2040"
	case vid == "0483":
		return "STM32"
	case strings.Contains(mfr, "espressif"):
		return "ESP32"
	case strings.Contains(mfr, "raspberry"):
		return "RP2040"
	case strings.Contains(mfr, "stm"), strings.Contains(mfr, "st-link"):
		return "STM32"
	}
	return "Unknown"
}

// bootselVolumes finds mounted RPI-RP2 / RP2350 drives (a Pico in BOOTSEL).
func bootselVolumes() []string {
	match := func(n string) bool { return strings.Contains(n, "RPI-RP2") || strings.Contains(n, "RP2350") }
	var out []string
	for _, base := range []string{"/Volumes", "/media", "/mnt", "/run/media"} {
		ents, _ := os.ReadDir(base)
		for _, e := range ents {
			p := filepath.Join(base, e.Name())
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if match(e.Name()) {
				out = append(out, p)
				continue
			}
			subs, _ := os.ReadDir(p) // /media/<user>/<label>
			for _, s := range subs {
				if match(s.Name()) {
					out = append(out, filepath.Join(p, s.Name()))
				}
			}
		}
	}
	return out
}

// Wait polls for board to be connected again (after a flash resets it),
// returning its current port, or "" after timeout.
func Wait(board Port, timeout time.Duration) string {
	for end := time.Now().Add(timeout); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
		ports, _ := List()
		for _, p := range ports {
			if p.State == "running" && board.SameBoard(p) {
				return p.Port
			}
		}
	}
	return ""
}

// ProbeESP asks the chip behind port for its ID without resetting it, to
// tell a live ESP from a bare USB-serial bridge: "verified" when it answers
// within 2 s, else "silent" (dead, unwired, or running firmware).
func ProbeESP(port string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if esptool.Probe(ctx, port) {
		return "verified"
	}
	return "silent"
}
