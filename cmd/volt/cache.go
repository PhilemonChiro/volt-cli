package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/toolchain"
)

func runCache(args []string) int {
	sub := first(args)
	switch sub {
	case "package":
	default:
		stderr.Println("Usage: volt cache package [chip] -o file.tar.gz")
		if sub == "" || sub == "-h" || sub == "--help" {
			return 0
		}
		return 2
	}
	fl, _ := newFlags("cache package", "[chip] -o file.tar.gz")
	out := fl.String("o", "", "output tar.gz path")
	pos, ok := parse(fl, args[1:])
	if !ok || len(pos) > 1 || *out == "" {
		return 2
	}
	chip := first(pos)
	if chip == "" {
		chip, _ = projectInfo(".")
	}
	if chip == "" {
		stderr.Println("[red]volt cache: no chip given and no volt.toml with a chip here[/red]")
		return 2
	}
	if err := packageCache(strings.ToLower(chip), *out); err != nil {
		stderr.Printf("[red]volt cache: %s[/red]\n", err)
		return 1
	}
	stdout.Printf("[green]Packaged[/green] %s\n", toolchain.Tilde(*out))
	return 0
}

func packageCache(chip, outPath string) error {
	src := filepath.Join(toolchain.CacheRoot(), "ccache", chip)
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("no warmed compiler cache at %s; build with ccache enabled first", toolchain.Tilde(src))
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	meta := map[string]any{
		"kind":       "volt-compiler-cache-v1",
		"chip":       chip,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"cache_dir":  "cache/ccache/" + chip,
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeTarBytes(tw, ".volt-prebuilt-cache.json", b); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = name
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func writeTarBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(b)),
	}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}
