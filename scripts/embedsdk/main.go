// Command embedsdk refreshes the SDK built into volt (internal/sdkfiles/sdk)
// from the repo: the framework, the device examples, and only the default
// device libraries listed in volt/default-libraries.txt. Run from cli/:
//
//	go run ./scripts/embedsdk
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var skip = regexp.MustCompile(`(^|/)(__pycache__|build|\.DS_Store)$|\.py[co]$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "embedsdk:", err)
		os.Exit(1)
	}
}

func run() error {
	volt, embed := filepath.Join("..", "volt"), filepath.Join("internal", "sdkfiles", "sdk")
	libs, err := defaults(filepath.Join(volt, "devices"), filepath.Join(volt, "default-libraries.txt"))
	if err != nil {
		return err
	}
	for _, d := range []string{"devices", "framework", "examples"} {
		if err := os.RemoveAll(filepath.Join(embed, d)); err != nil {
			return err
		}
	}
	for _, l := range libs {
		if err := copyTree(filepath.Join(volt, "devices", l), filepath.Join(embed, "devices", l)); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(volt, "framework"), filepath.Join(embed, "framework")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(volt, "examples", "devices"), filepath.Join(embed, "examples", "devices")); err != nil {
		return err
	}
	fmt.Printf("embedsdk: %d device libraries: %s\n", len(libs), strings.Join(libs, " "))
	return nil
}

// defaults resolves the listed libraries to folders: each one's driver folder
// (its c_prefix) plus every variant sharing that driver.
func defaults(devices, list string) ([]string, error) {
	prefixOf := map[string]string{}
	ents, err := os.ReadDir(devices)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		var d struct {
			CPrefix string `json:"c_prefix"`
		}
		b, err := os.ReadFile(filepath.Join(devices, e.Name(), "device.json"))
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("%s/device.json: %v", e.Name(), err)
		}
		prefixOf[e.Name()] = strings.ToLower(d.CPrefix)
	}
	f, err := os.Open(list)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	drivers := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		p, ok := prefixOf[name]
		if !ok {
			return nil, fmt.Errorf("%s: no library %q in %s", list, name, devices)
		}
		if p == "" {
			p = name
		}
		drivers[p] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var out []string
	for name, p := range prefixOf {
		if drivers[p] || drivers[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if skip.MatchString(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}
