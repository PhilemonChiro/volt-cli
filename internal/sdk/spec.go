// Package sdk makes and uses prebuilt ESP-IDF packs: everything an ESP32
// firmware build needs from ESP-IDF (component archives, headers, linker
// scripts, bootloader, partition table, exact flags), harvested from one real
// ESP-IDF build of a reference project. A Volt build with a pack compiles only
// Volt's own files and links; no CMake, Ninja or ESP-IDF source.
package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SpecFile is the pack's build description, at the pack root.
const SpecFile = "build.json"

// Spec is build.json. Paths and arguments may hold placeholders, expanded at
// build time: {pack} the pack dir, {project} the generated project dir (the
// prefix ESP-IDF maps to "." in __FILE__), {framework} Volt's framework
// include dir, {name}/{version} the app name and version, {map} the link map,
// {app} the archive of Volt's objects (ESP-IDF's libmain.a) and {app_desc}
// the per-project app descriptor archive.
type Spec struct {
	Format     int    `json:"format"`
	Chip       string `json:"chip"`
	IDFVersion string `json:"idf_version"`
	ToolPrefix string `json:"tool_prefix"` // e.g. xtensa-esp32-elf-

	// Compiling Volt's files (generated main.c, runtime, drivers).
	CFlags   []string `json:"cflags"`
	Includes []string `json:"includes"` // in order, as -I

	// AppDesc is ESP-IDF's app descriptor: it embeds the project name and
	// build time, so it is compiled per project into its own archive.
	AppDesc struct {
		Src  string   `json:"src"`
		Args []string `json:"args"` // compiler args, without -c/-o
	} `json:"app_desc"`

	// Link is ESP-IDF's link line: <cxx> Flags LDFlags ElfSrc -o x.elf
	// LibPath Libs.
	Link struct {
		Flags   []string `json:"flags"`
		LDFlags []string `json:"ldflags"`
		ElfSrc  string   `json:"elf_src"`
		LibPath []string `json:"lib_path"`
		Libs    []string `json:"libs"`
	} `json:"link"`

	// Elf2Image are ESP-IDF's esptool arguments ("--chip c elf2image …"),
	// without -o and the ELF.
	Elf2Image []string `json:"elf2image"`

	FlashSettings map[string]string `json:"flash_settings"` // flash_mode, flash_freq, flash_size
	Flash         []FlashPart       `json:"flash"`
}

// FlashPart is one image written at flash time. The app has no File: it is
// the project's own image.
type FlashPart struct {
	Name   string `json:"name"` // bootloader, partition-table, app
	Offset string `json:"offset"`
	File   string `json:"file,omitempty"`
}

// LoadSpec reads a pack's build.json.
func LoadSpec(pack string) (*Spec, error) {
	b, err := os.ReadFile(filepath.Join(pack, SpecFile))
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", SpecFile, err)
	}
	if s.Format != 1 {
		return nil, fmt.Errorf("%s: unsupported format %d", SpecFile, s.Format)
	}
	return &s, nil
}

func expand(args []string, r *strings.Replacer) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = r.Replace(a)
	}
	return out
}
