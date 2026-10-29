package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/sdkfiles"
)

// dlBase is where release.sh publishes (VOLT_DL overrides, for testing).
func dlBase() string {
	if d := os.Getenv("VOLT_DL"); d != "" {
		return strings.TrimRight(d, "/")
	}
	return "https://dl.voltembedded.com"
}

// ideOwned reports whether exe is the copy inside Volt IDE, which the IDE
// updates with itself.
func ideOwned(exe string) bool {
	return strings.Contains(exe, ".app/Contents/") || strings.Contains(exe, string(filepath.Separator)+"volt-ide"+string(filepath.Separator)) ||
		strings.Contains(strings.ToLower(exe), "volt ide")
}

// runUpdate replaces this volt with the latest release, checked against
// the release's SHA256SUMS.
func runUpdate(args []string) int {
	fl, _ := newFlags("update", "[--check]")
	check := fl.Bool("check", false, "only report whether a newer volt exists")
	if pos, ok := parse(fl, args); !ok || len(pos) != 0 {
		return 2
	}
	exe, err := voltExecutable()
	if err != nil {
		stderr.Printf("[red]volt update: %v[/red]\n", err)
		return 1
	}
	if ideOwned(exe) {
		stderr.Println("This volt is part of Volt IDE, which updates it together with the IDE (Volt IDE › Check for Updates).")
		return 1
	}
	if !sdkfiles.IsRelease(version) {
		stderr.Printf("This is a development build (%s); build it from source instead.\n", version)
		return 1
	}
	latest, err := fetch(dlBase()+"/latest.txt", 1<<10)
	if err != nil {
		stderr.Printf("[red]volt update: could not reach %s: %v[/red]\n", dlBase(), err)
		return 1
	}
	v := strings.TrimSpace(string(latest))
	if sdkfiles.Compare(v, version) <= 0 {
		fmt.Printf("volt %s is the latest.\n", version)
		return 0
	}
	if *check {
		fmt.Printf("volt %s is available (you have %s). Run `volt update`.\n", v, version)
		return 0
	}
	if err := install(v, exe); err != nil {
		stderr.Printf("[red]volt update: %v[/red]\n", err)
		return 1
	}
	fmt.Printf("Updated volt %s → %s (%s)\n", version, v, exe)
	return 0
}

func install(v, exe string) error {
	name := fmt.Sprintf("volt-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".zip"
	} else {
		name += ".tar.gz"
	}
	fmt.Printf("Downloading volt %s (%s/%s)\n", v, runtime.GOOS, runtime.GOARCH)
	archive, err := fetch(dlBase()+"/"+v+"/"+name, 200<<20)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	sums, err := fetch(dlBase()+"/"+v+"/SHA256SUMS", 1<<16)
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	got := sha256.Sum256(archive)
	if want == "" || want != hex.EncodeToString(got[:]) {
		return fmt.Errorf("checksum mismatch for %s — not installed", name)
	}
	bin, err := extractVolt(archive, runtime.GOOS == "windows")
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	// Windows can't replace a running exe, but it can rename it aside.
	old := exe + ".old"
	os.Remove(old)
	if runtime.GOOS == "windows" {
		if err := os.Rename(exe, old); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		if runtime.GOOS == "windows" {
			os.Rename(old, exe)
		}
		return err
	}
	return nil
}

func fetch(url string, limit int64) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// extractVolt pulls the volt binary out of a release archive.
func extractVolt(archive []byte, isZip bool) ([]byte, error) {
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "volt.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, errors.New("volt.exe not found in the release archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("volt not found in the release archive")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == "volt" && h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}
