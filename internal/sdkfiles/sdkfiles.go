// Package sdkfiles ships the device library and framework inside the volt
// binary, so a plain install can build without the IDE. sdk/ is a copy of
// volt/devices and volt/framework, refreshed by scripts/release.sh.
package sdkfiles

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:sdk
var files embed.FS

// stamp marks an sdk directory as unpacked by the CLI, holding the version
// that wrote it. A directory without it (the IDE's, or hand-edited) is never
// touched.
const stamp = ".volt-cli-version"

// Ensure makes dir hold this binary's devices and framework: it unpacks them
// when dir/devices is missing, or replaces a copy an older CLI unpacked.
func Ensure(dir, version string) error {
	if _, err := os.Stat(filepath.Join(dir, "devices")); err == nil {
		b, err := os.ReadFile(filepath.Join(dir, stamp))
		if err != nil || string(b) == version {
			return nil
		}
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	err := fs.WalkDir(files, "sdk", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p[len("sdk"):]))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err == nil {
		err = os.WriteFile(filepath.Join(tmp, stamp), []byte(version), 0o644)
	}
	if err == nil {
		os.RemoveAll(dir)
		err = os.Rename(tmp, dir)
	}
	if err != nil {
		os.RemoveAll(tmp)
	}
	return err
}
