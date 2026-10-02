package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The ESP32 toolchain users build with: Espressif's GCC and a prebuilt
// ESP-IDF pack (see internal/sdk), both manifest assets. Building and
// flashing need nothing else: no ESP-IDF tree, Python, CMake or Ninja.
func init() { register(newESP32, "esp32") }

// packKind marks a manifest asset that is an SDK pack, installed under
// SdkRoot rather than on PATH.
const packKind = "sdk-pack"

// SdkRoot is where SDK packs live: <Root>/sdks/<pack name>/.
func SdkRoot() string { return filepath.Join(Root(), "sdks") }

func newESP32(chip string, h *Host) (*Toolchain, error) {
	keys := h.Manifest.Requirements(chip, h.Platform)
	t := &Toolchain{Chip: chip, Name: strings.ToUpper(chip)}
	var tools []string
	pack := false
	for _, key := range keys {
		t.Components = append(t.Components, assetComponent(h, key))
		a, ok := h.Manifest.Asset(key)
		if !ok {
			continue
		}
		t.Managed = append(t.Managed, assetInstallDir(a))
		if a.Kind == packKind {
			pack = true
		} else {
			tools = append(tools, key)
		}
	}
	if !pack {
		return nil, fmt.Errorf("the toolchain manifest (%s) has no %s SDK pack for %s; update volt", h.ManifestSource, chip, h.Platform)
	}
	t.BuildEnv = func() ([]string, error) {
		path := strings.Join(append(BinDirs(h, tools), os.Getenv("PATH")), string(os.PathListSeparator))
		return []string{"PATH=" + path}, nil
	}
	return t, nil
}
