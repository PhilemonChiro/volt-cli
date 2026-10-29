// Package sdkfiles ships the device library and framework inside the volt
// binary, so a plain install can build without the IDE. sdk/ is a copy of
// volt/devices and volt/framework, refreshed by scripts/release.sh.
//
// ~/.volt/sdk is the one live library everything reads and writes. A shipped
// SDK (this binary's, the IDE's, or the repo's via `volt sdk sync`) only
// updates it, and only on a new version or an explicit sync: a shipped library
// is added when missing and replaced only while the live copy is still what
// was last shipped. A library changed locally is reported, never overwritten,
// until the user takes the update or keeps theirs.
package sdkfiles

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed all:sdk
var files embed.FS

// stamp marks an sdk directory as unpacked by the CLI, holding the version
// that wrote it.
const stamp = ".volt-cli-version"

// Record is <dir>/.shipped.json: each unit's digest as last shipped
// ("devices/<name>", "framework", "examples/devices"), "kept:<unit>" for a shipped
// version the user chose not to take, and "_owner" / "_version_<owner>": who
// keeps the library up to date and the version that last synced it.
const Record = ".shipped.json"

// Source is a shipped SDK: the devices, framework and examples trees in FS.
type Source struct {
	FS                           fs.FS
	Devices, Framework, Examples string
}

// Embedded is this binary's SDK.
func Embedded() Source {
	return Source{FS: files, Devices: "sdk/devices", Framework: "sdk/framework", Examples: "sdk/examples/devices"}
}

// FromDir reads a shipped SDK from disk, e.g. the repo's volt/ folder.
func FromDir(dir string) Source {
	return Source{FS: os.DirFS(dir), Devices: "devices", Framework: "framework", Examples: "examples/devices"}
}

// Options steer a Sync. Take and Keep name units ("hc05" = "devices/hc05").
type Options struct {
	Owner, Version string
	Take, Keep     []string
	DryRun         bool
}

// Result is what a Sync did; Kept are units changed locally that it left.
type Result struct {
	Added   []string `json:"added"`
	Updated []string `json:"updated"`
	Kept    []string `json:"kept"`
	Backup  string   `json:"backup,omitempty"`
	NewerBy string   `json:"newer_by,omitempty"` // a newer release keeps the library
}

// ReadRecord loads dir's record (empty if none).
func ReadRecord(dir string) map[string]string {
	rec := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, Record)); err == nil {
		json.Unmarshal(b, &rec)
	}
	return rec
}

// UnitName turns "hc05" into "devices/hc05" and "examples" into
// "examples/devices"; a unit's name is its path under the live library.
func UnitName(s string) string {
	if s == "examples" {
		return "examples/devices"
	}
	if s == "framework" || strings.Contains(s, "/") {
		return s
	}
	return "devices/" + s
}

// Sync brings dir up to date with src.
func Sync(src Source, dir string, o Options) (Result, error) {
	res := Result{Added: []string{}, Updated: []string{}, Kept: []string{}}
	rec := ReadRecord(dir)
	take, keep := map[string]bool{}, map[string]bool{}
	for _, t := range o.Take {
		take[UnitName(t)] = true
	}
	for _, k := range o.Keep {
		keep[UnitName(k)] = true
	}
	units := map[string]string{"framework": src.Framework, "examples/devices": src.Examples}
	if ents, err := fs.ReadDir(src.FS, src.Devices); err == nil {
		for _, e := range ents {
			if e.IsDir() && !skip.MatchString(e.Name()) {
				units["devices/"+e.Name()] = path.Join(src.Devices, e.Name())
			}
		}
	}
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	backup := filepath.Join(filepath.Dir(dir), "sdk-backups", time.Now().Format("20060102-150405"))
	live := os.DirFS(dir)
	for _, key := range keys {
		want := digest(src.FS, units[key])
		if want == "" {
			continue
		}
		have := digest(live, key)
		switch {
		case have == want:
			rec[key] = want
			delete(rec, "kept:"+key)
			continue
		case have == "":
			res.Added = append(res.Added, key)
		case take[key] || rec[key] == have:
			// Taken by the user, or still exactly what was last shipped.
			res.Updated = append(res.Updated, key)
			if !o.DryRun {
				if err := copyFS(live, key, filepath.Join(backup, filepath.FromSlash(key))); err != nil {
					return res, err
				}
				res.Backup = backup
			}
		case keep[key]:
			rec["kept:"+key] = want // don't ask again for this shipped version
			addHeader(src.FS, units[key], dir, key, o.DryRun)
			continue
		case rec["kept:"+key] == want:
			addHeader(src.FS, units[key], dir, key, o.DryRun)
			continue
		default:
			res.Kept = append(res.Kept, key)
			addHeader(src.FS, units[key], dir, key, o.DryRun)
			continue
		}
		if !o.DryRun {
			if err := copyFS(src.FS, units[key], filepath.Join(dir, filepath.FromSlash(key))); err != nil {
				return res, err
			}
		}
		rec[key] = want
		delete(rec, "kept:"+key)
	}
	if o.DryRun {
		return res, nil
	}
	if o.Owner != "" {
		rec["_owner"] = o.Owner
		if o.Version != "" {
			rec["_version_"+o.Owner] = o.Version
		}
	}
	out, _ := json.MarshalIndent(rec, "", " ")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	return res, os.WriteFile(filepath.Join(dir, Record), out, 0o644)
}

// addHeader gives a library the user changed locally (and so kept) the
// header.h it is missing: device libraries now declare themselves there rather
// than in the shared framework headers, so without it their builds would fail.
// It only ever adds that one file; nothing of the user's is touched.
func addHeader(fsys fs.FS, srcDir, dir, key string, dryRun bool) {
	if dryRun || !strings.HasPrefix(key, "devices/") {
		return
	}
	dst := filepath.Join(dir, filepath.FromSlash(key), "header.h")
	if _, err := os.Stat(dst); err == nil {
		return
	}
	if b, err := fs.ReadFile(fsys, path.Join(srcDir, "header.h")); err == nil {
		_ = os.WriteFile(dst, b, 0o644)
	}
}

// Ensure is the CLI's own upgrade path: when this binary's SDK is new and
// the library is the CLI's to keep (not a developer's), it syncs the embedded
// SDK. A release never downgrades: if a newer release already synced the
// library, an older volt leaves it (Result.NewerBy names that release). It
// never deletes anything; locally changed libraries come back in Kept.
func Ensure(dir, version string) (Result, error) {
	rec := ReadRecord(dir)
	if o := rec["_owner"]; o != "" && o != "cli" {
		return Result{}, nil
	}
	if last := rec["_release"]; IsRelease(version) && IsRelease(last) && Compare(version, last) < 0 {
		return Result{NewerBy: last}, nil
	}
	// The embedded SDK's digest is part of the stamp, so a rebuilt binary
	// (a dev build is always "dev") with changed libraries still syncs.
	id := version + "+" + digest(files, "sdk")[:12]
	b, err := os.ReadFile(filepath.Join(dir, stamp))
	if err == nil && string(b) == id {
		return Result{}, nil
	}
	res, serr := Sync(Embedded(), dir, Options{Owner: "cli", Version: version})
	if serr != nil {
		return res, serr
	}
	if IsRelease(version) {
		rec = ReadRecord(dir)
		rec["_release"] = version
		out, _ := json.MarshalIndent(rec, "", " ")
		if err := os.WriteFile(filepath.Join(dir, Record), out, 0o644); err != nil {
			return res, err
		}
	}
	return res, os.WriteFile(filepath.Join(dir, stamp), []byte(id), 0o644)
}

// isRelease reports whether v is a release version like v1.2.3 (not "dev").
func IsRelease(v string) bool { _, ok := parseVersion(v); return ok }

func parseVersion(v string) ([3]int, bool) {
	var n [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// Compare orders release versions: -1, 0 or 1 (non-releases sort first).
func Compare(a, b string) int {
	x, okA := parseVersion(a)
	y, okB := parseVersion(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range x {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

var skip = regexp.MustCompile(`^(__pycache__|build|\.DS_Store)$|\.py[co]$|\.tmp$`)

// digest hashes every file under root (sorted, path + contents); "" if absent.
func digest(fsys fs.FS, root string) string {
	if st, err := fs.Stat(fsys, root); err != nil || !st.IsDir() {
		return ""
	}
	h := sha256.New()
	fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && skip.MatchString(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			h.Write([]byte(strings.TrimPrefix(strings.TrimPrefix(p, root), "/") + "\x00"))
			h.Write(b)
			h.Write([]byte("\x00"))
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// copyFS replaces dst with the tree src in fsys.
func copyFS(fsys fs.FS, src, dst string) error {
	tmp := dst + ".tmp"
	os.RemoveAll(tmp)
	err := fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != src && skip.MatchString(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		out := filepath.Join(tmp, filepath.FromSlash(strings.TrimPrefix(p, src)))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err == nil {
		os.RemoveAll(dst)
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.RemoveAll(tmp)
	}
	return err
}

// Short drops the devices/ prefix from unit names, for messages.
func Short(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = strings.TrimPrefix(k, "devices/")
	}
	return strings.Join(out, ", ")
}

// FS is the built-in SDK tree (devices/, framework/).
func FS() fs.FS {
	sub, _ := fs.Sub(files, "sdk")
	return sub
}
