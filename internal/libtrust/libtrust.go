// Package libtrust decides whether the device libraries and framework a build
// compiles are ones the user approved. The Volt IDE is the only approver: it
// holds the private half of a per-computer Ed25519 key, and each approval is a
// signed record of a library's exact bytes in ~/.volt/trust/approved.json. A
// library an AI agent (or anything else) writes or changes no longer matches
// its record, so `volt build` refuses it until the user reviews it in the IDE.
//
// The store is tamper-evident, not tamper-proof: records can't be forged
// without the private key, but a same-user process could swap local.pub and
// re-sign. The IDE pins the key's fingerprint in the OS keychain and flags that.
package libtrust

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Framework is the store name of the framework tree (device prefixes are C
// identifiers, so it can't collide with one).
const Framework = "@framework"

// Entry is one approval: the digest of a library's files when it was approved.
type Entry struct {
	Hash       string `json:"hash"`
	Version    string `json:"version,omitempty"`
	Source     string `json:"source"` // sdk-baseline | local | ai | registry:<keyId>
	ApprovedAt string `json:"approvedAt"`
	Sig        string `json:"sig"`
}

// Store is approved.json. A library may have several approved versions
// (one per device-library location, or older versions), newest last.
type Store struct {
	Schema  int                `json:"schema"`
	Key     string             `json:"key"` // fingerprint of the signing key
	Entries map[string][]Entry `json:"entries"`
}

// keepPerLibrary bounds how many approved versions a library keeps.
const keepPerLibrary = 4

// Dir is ~/.volt/trust.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".volt", "trust"), nil
}

func storePath(dir string) string { return filepath.Join(dir, "approved.json") }
func pubPath(dir string) string   { return filepath.Join(dir, "local.pub") }

// Digest hashes every file under root in fsys: sorted slash paths relative to
// root, each as "path\0size\0bytes". Anything but a regular file or directory (a symlink
// could pull in code from outside) is an error.
func Digest(fsys fs.FS, root string) (string, error) {
	var files []string
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".DS_Store" { // Finder litter, never compiled
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		rel := p
		if root != "." {
			rel = strings.TrimPrefix(p, root+"/")
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Library is one thing a build compiles: a device folder or the framework.
type Library struct {
	Name string // device prefix, or Framework
	FS   fs.FS
	Root string
	// Key is where its approvals are recorded; "" means Name. A project's own
	// libraries are keyed by the project's folder, so two projects can each
	// have a library of the same name without sharing approvals.
	Key string
}

func (l Library) key() string {
	if l.Key != "" {
		return l.Key
	}
	return l.Name
}

// ProjectLibraries lists the project's own device libraries
// (<project>/devices/<prefix>/ holding a device.json). None is not an error.
func ProjectLibraries(project string) ([]Library, error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	devices := filepath.Join(abs, "devices")
	ents, err := os.ReadDir(devices)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var libs []Library
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(devices, e.Name(), "device.json")); err != nil {
			continue
		}
		libs = append(libs, Library{Name: e.Name(), FS: os.DirFS(filepath.Join(devices, e.Name())), Root: ".",
			Key: "project:" + filepath.ToSlash(abs) + "/" + e.Name()})
	}
	return libs, nil
}

// Libraries lists the framework and every device folder (one holding a
// device.json) in sdkDir's layout: <framework>/, <devices>/<prefix>/.
func Libraries(devices, framework string) ([]Library, error) {
	libs := []Library{{Name: Framework, FS: os.DirFS(framework), Root: "."}}
	ents, err := os.ReadDir(devices)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(devices, e.Name(), "device.json")); err != nil {
			continue
		}
		libs = append(libs, Library{Name: e.Name(), FS: os.DirFS(filepath.Join(devices, e.Name())), Root: "."})
	}
	return libs, nil
}

// Status of one library against the store.
type Status struct {
	Name           string `json:"name"`
	Hash           string `json:"hash"`
	State          string `json:"state"` // approved | changed | unapproved | invalid | unreadable
	Detail         string `json:"detail,omitempty"`
	Source         string `json:"source,omitempty"`
	Version        string `json:"version,omitempty"`
	ApprovedAt     string `json:"approvedAt,omitempty"`
	MatchesBundled bool   `json:"matchesBundled"` // bytes equal this CLI's built-in copy
	Bundled        bool   `json:"bundled"`        // Volt ships a library by this name
	// ApprovedHashes are the versions with a valid approval (newest last).
	ApprovedHashes []string `json:"approvedHashes"`
}

// Load reads the store and local public key from dir. ok is false when
// approvals were never set up (no store and no key).
func Load(dir string) (st *Store, pub ed25519.PublicKey, ok bool, err error) {
	b, serr := os.ReadFile(storePath(dir))
	pb, perr := os.ReadFile(pubPath(dir))
	if errors.Is(serr, fs.ErrNotExist) && errors.Is(perr, fs.ErrNotExist) {
		return nil, nil, false, nil
	}
	if perr != nil {
		return nil, nil, true, fmt.Errorf("approval key %s: %w", pubPath(dir), perr)
	}
	if pub, err = parsePub(pb); err != nil {
		return nil, nil, true, err
	}
	st = &Store{Schema: 2, Key: Fingerprint(pub), Entries: map[string][]Entry{}}
	if serr == nil {
		if err := json.Unmarshal(b, st); err != nil {
			return nil, nil, true, fmt.Errorf("%s: %w", storePath(dir), err)
		}
		if st.Entries == nil {
			st.Entries = map[string][]Entry{}
		}
	} else if !errors.Is(serr, fs.ErrNotExist) {
		return nil, nil, true, serr
	}
	if st.Key != Fingerprint(pub) {
		return nil, nil, true, errors.New("approvals were signed by a different key than local.pub")
	}
	return st, pub, true, nil
}

// Check compares each library with its approval. bundled, when non-nil, maps
// a name to this CLI's built-in digest of it.
func Check(st *Store, pub ed25519.PublicKey, libs []Library, bundled map[string]string) []Status {
	out := make([]Status, 0, len(libs))
	for _, l := range libs {
		s := Status{Name: l.Name, ApprovedHashes: []string{}}
		h, err := Digest(l.FS, l.Root)
		if err != nil {
			s.State, s.Detail = "unreadable", err.Error()
			out = append(out, s)
			continue
		}
		s.Hash = h
		if l.Key == "" { // a project's own library is never a shipped one
			s.MatchesBundled = bundled != nil && bundled[l.Name] == h
			s.Bundled = bundled != nil && bundled[l.Name] != ""
		}
		es := st.Entries[l.key()]
		var match, invalid *Entry
		for k := range es {
			e := &es[k]
			switch {
			case !verifyEntry(pub, st.Key, l.key(), *e):
				invalid = e
			case e.Hash == h:
				match = e
				s.ApprovedHashes = append(s.ApprovedHashes, e.Hash)
			default:
				s.ApprovedHashes = append(s.ApprovedHashes, e.Hash)
			}
		}
		shown := match
		switch {
		case match != nil:
			s.State = "approved"
		case invalid != nil:
			s.State, s.Detail, shown = "invalid", "approval record's signature does not verify", invalid
		case len(es) > 0:
			s.State, shown = "changed", &es[len(es)-1]
		default:
			s.State = "unapproved"
		}
		if shown != nil {
			s.Source, s.Version, s.ApprovedAt = shown.Source, shown.Version, shown.ApprovedAt
		}
		out = append(out, s)
	}
	return out
}

// message is the exact text an approval signs.
func message(key, name string, e Entry) []byte {
	return []byte(strings.Join([]string{"volt-libtrust/1", key, name, e.Hash, e.Version, e.Source, e.ApprovedAt}, "\n") + "\n")
}

func verifyEntry(pub ed25519.PublicKey, key, name string, e Entry) bool {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(e.Sig, "ed25519:"))
	return err == nil && strings.HasPrefix(e.Sig, "ed25519:") && ed25519.Verify(pub, message(key, name, e), sig)
}

// Fingerprint identifies a public key.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Init makes a new approval key: it writes local.pub (and an empty store) to
// dir and returns the private key PEM for the IDE to keep in the keychain.
// Existing approvals are signed by the old key, so re-keying drops them.
func Init(dir string, force bool) ([]byte, error) {
	if _, err := os.Stat(pubPath(dir)); err == nil && !force {
		return nil, errors.New("approvals already set up (use --force to start over; all approvals are dropped)")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	pder, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeAtomic(pubPath(dir), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	if err := save(dir, &Store{Schema: 2, Key: Fingerprint(pub), Entries: map[string][]Entry{}}); err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pder}), nil
}

// Approve signs the current digest of each library into the store, using the
// private key PEM (which must match local.pub).
func Approve(dir string, privPEM []byte, libs []Library, source, version string) error {
	st, pub, ok, err := Load(dir)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("approvals not set up — run: volt trust init")
	}
	priv, err := parsePriv(privPEM)
	if err != nil {
		return err
	}
	if !priv.Public().(ed25519.PublicKey).Equal(pub) {
		return errors.New("private key does not match local.pub")
	}
	if !validSource(source) {
		return fmt.Errorf("bad source %q (want sdk-baseline, local, ai or registry:<keyId>)", source)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, l := range libs {
		h, err := Digest(l.FS, l.Root)
		if err != nil {
			return fmt.Errorf("%s: %w", l.Name, err)
		}
		e := Entry{Hash: h, Version: version, Source: source, ApprovedAt: now}
		e.Sig = "ed25519:" + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, message(st.Key, l.key(), e)))
		kept := []Entry{}
		for _, old := range st.Entries[l.key()] {
			if old.Hash != h {
				kept = append(kept, old)
			}
		}
		kept = append(kept, e)
		if len(kept) > keepPerLibrary {
			kept = kept[len(kept)-keepPerLibrary:]
		}
		st.Entries[l.key()] = kept
	}
	return save(dir, st)
}

// Revoke drops approvals (no key needed: it only takes trust away).
func Revoke(dir string, names []string) error {
	st, _, ok, err := Load(dir)
	if err != nil || !ok {
		return err
	}
	for _, n := range names {
		delete(st.Entries, n)
	}
	return save(dir, st)
}

func validSource(s string) bool {
	switch s {
	case "sdk-baseline", "local", "ai":
		return true
	}
	return strings.HasPrefix(s, "registry:") && len(s) > len("registry:")
}

func save(dir string, st *Store) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(storePath(dir), append(b, '\n'), 0o644)
}

func writeAtomic(p string, b []byte, mode os.FileMode) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func parsePub(b []byte) (ed25519.PublicKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("local.pub is not a PEM public key")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if pub, ok := k.(ed25519.PublicKey); err == nil && ok {
		return pub, nil
	}
	return nil, errors.New("local.pub is not an Ed25519 public key")
}

func parsePriv(b []byte) (ed25519.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("no PEM private key on stdin")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if priv, ok := k.(ed25519.PrivateKey); err == nil && ok {
		return priv, nil
	}
	return nil, errors.New("private key is not Ed25519")
}

// BundledDigests digests a built-in SDK tree (devices/, framework/).
func BundledDigests(sdk fs.FS) map[string]string {
	out := map[string]string{}
	if h, err := Digest(sdk, "framework"); err == nil {
		out[Framework] = h
	}
	ents, _ := fs.ReadDir(sdk, "devices")
	for _, e := range ents {
		if h, err := Digest(sdk, path.Join("devices", e.Name())); err == nil {
			out[e.Name()] = h
		}
	}
	return out
}
