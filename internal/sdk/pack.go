package sdk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Archive writes pack dir as a reproducible tar.gz (sorted entries, fixed
// times and owners, top-level dir named after the pack) and returns its
// SHA-256.
func Archive(pack, out string) (string, error) {
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, sum))
	tw := tar.NewWriter(gz)
	root := filepath.Base(pack)
	epoch := time.Unix(0, 0)
	err = filepath.WalkDir(pack, func(p string, d fs.DirEntry, err error) error { // WalkDir is lexical
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(pack, p)
		name := filepath.ToSlash(filepath.Join(root, rel))
		h := &tar.Header{Name: name, ModTime: epoch, Mode: 0o644, Format: tar.FormatPAX}
		if d.IsDir() {
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, name+"/", 0o755
			return tw.WriteHeader(h)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h.Typeflag, h.Size = tar.TypeReg, int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err != nil {
		os.Remove(out)
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), f.Close()
}

// ImageDiff compares two ESP app images. Same means byte-identical; Loaded
// means equal apart from the embedded ELF SHA-256 (at 0xb0) and the trailing
// checksum and digest, i.e. every byte the chip runs is the same.
type ImageDiff struct {
	Same, Loaded bool
	First        int // first differing offset outside those fields, or -1
}

func CompareImages(a, b []byte) ImageDiff {
	d := ImageDiff{Same: bytes.Equal(a, b), First: -1}
	if d.Same {
		d.Loaded = true
		return d
	}
	n := min(len(a), len(b))
	tail := n - 1 // checksum byte
	if n > 0x17 && a[0x17] == 1 {
		tail = n - 33 // checksum + appended SHA-256
	}
	for i := 0; i < n; i++ {
		if (i >= 0xb0 && i < 0xd0) || i >= tail {
			continue
		}
		if a[i] != b[i] {
			d.First = i
			return d
		}
	}
	if len(a) != len(b) {
		d.First = n
		return d
	}
	d.Loaded = true
	return d
}
