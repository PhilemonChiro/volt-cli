package sdk

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

// Root is where installed packs live (toolchain.SdkRoot): the toolchain
// installs them from the manifest, `volt sdk install` from a file.
func Root() string { return toolchain.SdkRoot() }

// Provenance is a pack's provenance.json.
type Provenance struct {
	Chip     string `json:"chip"`
	Compiler string `json:"compiler"` // first line of gcc --version
	BuiltAt  string `json:"built_at"`
}

// Find returns the installed pack for chip; with several, the newest built.
func Find(chip string) (string, error) {
	dirs, _ := filepath.Glob(filepath.Join(Root(), "volt-sdk-"+chip+"-*", SpecFile))
	type cand struct{ dir, built string }
	var cs []cand
	for _, f := range dirs {
		var p Provenance
		if readJSON(filepath.Join(filepath.Dir(f), "provenance.json"), &p) == nil && p.Chip == chip {
			cs = append(cs, cand{filepath.Dir(f), p.BuiltAt})
		}
	}
	if len(cs) == 0 {
		return "", fmt.Errorf("no ESP-IDF pack installed for %s (in %s). Install one with: volt sdk install <pack.tar.gz>",
			chip, toolchain.Tilde(Root()))
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].built > cs[j].built })
	return cs[0].dir, nil
}

// Install unpacks a pack archive into Root, replacing an installed copy, and
// returns its dir. A <archive>.sha256 next to it, when present, must match.
func Install(archive string) (string, error) {
	if want, err := readSum(archive + ".sha256"); err == nil {
		got, err := fileSum(archive)
		if err != nil {
			return "", err
		}
		if got != want {
			return "", fmt.Errorf("%s: sha256 %s, expected %s", filepath.Base(archive), got, want)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(Root(), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(Root(), ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := toolchain.ExtractTarGz(archive, tmp); err != nil {
		return "", err
	}
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return "", err
	}
	if len(ents) != 1 || !ents[0].IsDir() || !strings.HasPrefix(ents[0].Name(), "volt-sdk-") {
		return "", fmt.Errorf("%s is not a Volt SDK pack", filepath.Base(archive))
	}
	name := ents[0].Name()
	if _, err := LoadSpec(filepath.Join(tmp, name)); err != nil {
		return "", err
	}
	dst := filepath.Join(Root(), name)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(tmp, name), dst); err != nil {
		return "", err
	}
	return dst, nil
}

func readSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if sum, _, _ := strings.Cut(strings.TrimSpace(line), " "); len(sum) == 64 {
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("%s: no sha256", filepath.Base(path))
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
