package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// newTemplates are the starting programs `volt new` offers, the same ones Volt IDE's new-project
// walkthrough writes (volt-ide main.js createProject). {led} is the chip's on-board LED.
var newTemplates = map[string]string{
	"blink": "hardware {\n    pin led: Output = {led}\n}\n\nevery 500ms {\n    led.toggle()\n}\n",
	"iot-sensor": "hardware {\n    pin led: Output = {led}\n    pin sensor: Analog = 36\n}\n\nsetup {\n" +
		"    wifi.connect(\"MyNetwork\", \"password\") or retry(10)\n    mqtt.connect(\"mqtt://broker.local\") or retry(5)\n}\n\n" +
		"every 500ms { led.toggle() }\n\nevery 5s when wifi.is_connected() {\n    mqtt.send_int(\"data/sensor\", sensor.read())\n}\n",
	"empty": "hardware {\n    pin led: Output = {led}\n}\n",
}

// onBoardLED is the GPIO of the LED most development boards for the chip have.
var onBoardLED = map[string]string{"esp32": "2", "esp8266": "2", "rp2040": "25"}

var validProjectName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// runNew creates a project folder: volt.toml naming the chip, main.volt from a template, and a .gitignore
// for the build output.
func runNew(args []string) int {
	fl, _ := newFlags("new", "<name> [--chip esp32] [--template blink]")
	chip := fl.String("chip", "esp32", "target `chip`: esp32, esp8266 or rp2040")
	tmpl := fl.String("template", "blink", "starting program: blink, iot-sensor or empty")
	pos, ok := parse(fl, args)
	if !ok || len(pos) != 1 {
		fl.Usage()
		return 2
	}
	name := pos[0]
	if !validProjectName.MatchString(filepath.Base(name)) {
		stderr.Printf("[red]volt new: use letters, numbers, dashes and underscores in the name[/red]\n")
		return 2
	}
	led, ok := onBoardLED[*chip]
	if !ok {
		stderr.Printf("[red]volt new: unknown chip %q; use esp32, esp8266 or rp2040[/red]\n", *chip)
		return 2
	}
	src, ok := newTemplates[*tmpl]
	if !ok {
		stderr.Printf("[red]volt new: unknown template %q; use blink, iot-sensor or empty[/red]\n", *tmpl)
		return 2
	}
	if _, err := os.Stat(name); err == nil {
		stderr.Printf("[red]volt new: %s already exists[/red]\n", name)
		return 1
	}
	files := map[string]string{
		"volt.toml":  fmt.Sprintf("[project]\nname = %q\nchip = %q\n", filepath.Base(name), *chip),
		"main.volt":  regexp.MustCompile(`\{led\}`).ReplaceAllString(src, led),
		".gitignore": "build/\n.volt/\n",
	}
	if err := os.MkdirAll(name, 0o755); err != nil {
		stderr.Printf("[red]volt new: %s[/red]\n", err)
		return 1
	}
	for _, f := range []string{"volt.toml", "main.volt", ".gitignore"} {
		if err := os.WriteFile(filepath.Join(name, f), []byte(files[f]), 0o644); err != nil {
			stderr.Printf("[red]volt new: %s[/red]\n", err)
			return 1
		}
	}
	stderr.Printf("Created %s for %s. Next:\n  cd %s\n  volt run\n", name, *chip, name)
	return 0
}
