package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/proc"
)

// markerFile records which asset (by SHA-256) an install dir holds, so a
// manifest bump is noticed and the asset replaced.
const markerFile = ".volt-asset.json"

type marker struct {
	Key     string `json:"key"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// assetComponent is the Component for one manifest asset.
func assetComponent(h *Host, key string) Component {
	a, ok := h.Manifest.Asset(key)
	if !ok {
		return Component{ID: key, Name: key, Check: func() Status {
			return Status{Detail: "not in manifest (" + h.ManifestSource + ")"}
		}}
	}
	dir := assetInstallDir(a)
	return Component{
		ID:   a.InstallTo,
		Name: a.InstallTo,
		Check: func() Status {
			st := Status{Path: dir}
			for _, rel := range a.Exposes {
				if !exists(filepath.Join(dir, rel)) {
					st.Detail = "not installed"
					return st
				}
			}
			// No marker = installed by an older IDE; accept it as is.
			var m marker
			if b, err := os.ReadFile(filepath.Join(dir, markerFile)); err == nil &&
				json.Unmarshal(b, &m) == nil && m.SHA256 != a.SHA256 {
				st.Detail = fmt.Sprintf("outdated (have %s, want %s)", m.Version, a.Version)
				return st
			}
			st.OK, st.Detail = true, a.Version
			return st
		},
		Install: func(e *Emitter) error { return installAsset(e, key, a, dir) },
	}
}

// BinDirs returns the directories holding the commands the given assets expose.
func BinDirs(h *Host, keys []string) []string {
	var dirs []string
	for _, key := range keys {
		if a, ok := h.Manifest.Asset(key); ok {
			for _, rel := range a.Exposes {
				dirs = append(dirs, filepath.Dir(filepath.Join(assetInstallDir(a), rel)))
			}
		}
	}
	return dirs
}

func assetInstallDir(a Asset) string {
	if a.Kind == "compiler-cache" {
		return filepath.Join(CacheRoot(), strings.TrimPrefix(a.InstallTo, "cache/"))
	}
	return filepath.Join(Root(), a.InstallTo)
}

// installAsset downloads, verifies and extracts a into dir. It extracts into a
// temporary dir and swaps it in, so an interrupted install never looks done.
func installAsset(e *Emitter, key string, a Asset, dir string) error {
	if err := os.MkdirAll(Root(), 0o755); err != nil {
		return err
	}
	// A failed download keeps the archive and its progress for the next run.
	archive := filepath.Join(Root(), ".download-"+a.Filename)
	if !verified(archive, a.SHA256) {
		if err := download(e, a, archive); err != nil {
			return err
		}
	}

	// Symlinks may point at sibling installs (e.g. the tools' Python env at
	// the managed Python), never outside the managed root.
	linkRoot := Root()
	if !within(linkRoot, dir) {
		linkRoot = CacheRoot()
	}

	e.Progress(0, "extract: "+a.Filename)
	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := extract(archive, a.Extract.Format, a.Extract.StripComponents, tmp, linkRoot); err != nil {
		return fmt.Errorf("extract %s: %w", a.Filename, err)
	}
	for _, rel := range a.Exposes {
		if err := os.Chmod(filepath.Join(tmp, rel), 0o755); err != nil {
			return fmt.Errorf("%s: archive lacks %s", a.Filename, rel)
		}
	}
	b, _ := json.Marshal(marker{key, a.SHA256, a.Version})
	if err := os.WriteFile(filepath.Join(tmp, markerFile), b, 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	os.Remove(archive) // kept until here so a failed extract needs no re-download
	e.Log("Installed " + Tilde(dir))
	return nil
}

// download fetches a from its sources in preference order, checking the
// SHA-256 while streaming. Every source is untrusted: only the (signed or
// embedded) manifest hash decides.
func download(e *Emitter, a Asset, dest string) error {
	var urls []string
	pref := sourcePreference()
	if pref != "upstream" {
		if a.URL != "" {
			urls = append(urls, a.URL)
		}
		urls = append(urls, a.Mirrors...)
	}
	// An asset not yet on the mirror comes from upstream whatever the
	// preference; the manifest hash still decides.
	if a.Upstream != "" && (pref != "mirror" || len(urls) == 0) {
		if pref == "mirror" {
			e.Log("Not on the Volt mirror yet; using upstream")
		}
		urls = append(urls, a.Upstream)
	}
	if len(urls) == 0 {
		return fmt.Errorf("%s: no download source for preference %q", a.Filename, pref)
	}
	if a.SHA256 == "" {
		return fmt.Errorf("%s: manifest has no sha256; refusing to install", a.Filename)
	}
	var errs []error
	for _, u := range urls {
		e.Log("Downloading " + u)
		err := fetch(e, u, a, dest)
		if err == nil {
			e.Log("Verified sha256 " + a.SHA256[:16] + "…")
			return nil
		}
		if proc.Root.Err() != nil {
			return err
		}
		e.Log(err.Error())
		errs = append(errs, err)
	}
	return fmt.Errorf("%s: all sources failed: %w", a.Filename, errors.Join(errs...))
}

// HTTP/1.1 only: HTTP/2 would multiplex parallel ranges onto one TCP
// connection, which is exactly what parallel ranges are meant to avoid.
var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 30 * time.Second,
	TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
}}

func humanSize(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fG", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%dB", n)
}

// extract unpacks archive (tar.gz or zip) into dest, dropping the first strip
// path components of every entry. Entries that would land outside dest, and
// links that point outside linkRoot, are rejected.
func extract(archive, format string, strip int, dest, linkRoot string) error {
	switch format {
	case "tar.gz":
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		return extractTar(tar.NewReader(gz), strip, dest, linkRoot)
	case "zip":
		return extractZip(archive, strip, dest, linkRoot)
	}
	return fmt.Errorf("unsupported archive format %q", format)
}

// target maps an archive entry name to a path under dest ("" = skip).
func target(dest, name string, strip int) (string, error) {
	parts := strings.Split(strings.Trim(path.Clean("/"+name), "/"), "/")
	if len(parts) <= strip || parts[0] == "" {
		return "", nil
	}
	return filepath.Join(dest, filepath.FromSlash(strings.Join(parts[strip:], "/"))), nil
}

// within reports whether p is dest or below it.
func within(dest, p string) bool {
	rel, err := filepath.Rel(dest, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkLink rejects a symlink at p whose target escapes linkRoot.
func checkLink(linkRoot, p, link string) error {
	if filepath.IsAbs(link) || !within(linkRoot, filepath.Join(filepath.Dir(p), link)) {
		return fmt.Errorf("unsafe symlink %s -> %s", p, link)
	}
	return nil
}

// checkParent rejects an entry whose directory resolves outside dest, which
// would mean writing through a symlink extracted earlier.
func checkParent(dest, p string) error {
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return err
	}
	if !within(realDest, dir) {
		return fmt.Errorf("unsafe path %s: its directory is outside the archive", p)
	}
	return nil
}

// verified reports whether path exists and has the given SHA-256.
func verified(path, sha string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == sha
}

func extractTar(tr *tar.Reader, strip int, dest, linkRoot string) error {
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, _ := target(dest, h.Name, strip)
		if p == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := checkParent(dest, p); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			err = writeFile(p, tr, h.FileInfo().Mode().Perm())
		case tar.TypeSymlink:
			if err = checkLink(linkRoot, p, h.Linkname); err == nil {
				err = os.Symlink(h.Linkname, p)
			}
		case tar.TypeLink:
			var old string
			if old, _ = target(dest, h.Linkname, strip); old == "" {
				err = fmt.Errorf("bad hard link %s -> %s", h.Name, h.Linkname)
			} else {
				err = os.Link(old, p)
			}
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(archive string, strip int, dest, linkRoot string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, _ := target(dest, f.Name, strip)
		if p == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := checkParent(dest, p); err != nil {
			return err
		}
		mode := f.Mode()
		if mode.IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		if mode&os.ModeSymlink != 0 {
			var link []byte
			if link, err = io.ReadAll(r); err == nil {
				if err = checkLink(linkRoot, p, string(link)); err == nil {
					err = os.Symlink(string(link), p)
				}
			}
		} else {
			err = writeFile(p, r, mode.Perm()|0o600)
		}
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(p string, r io.Reader, perm os.FileMode) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
