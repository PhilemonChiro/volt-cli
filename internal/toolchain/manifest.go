package toolchain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The toolchain mirror. Temporary host — move to the permanent domain before
// shipping to customers (see volt-ide/src/main/toolchain-mirror.js).
const (
	mirrorTag   = "toolchains-2026.05"
	mirrorBase  = "https://toolchains.sigma.co.zw/" + mirrorTag
	manifestURL = mirrorBase + "/manifest.json"
	cacheTTL    = 24 * time.Hour
)

// fallbackJSON ships inside the binary and is trusted implicitly. It mirrors
// FALLBACK_MANIFEST in volt-ide/src/main/toolchain-mirror.js.
//
//go:embed fallback-manifest.json
var fallbackJSON []byte

// keysJSON pins the Ed25519 keys a fetched manifest must be signed with.
//
//go:embed keys.json
var keysJSON []byte

// Asset is one downloadable toolchain archive.
type Asset struct {
	Filename  string   `json:"filename"`
	URL       string   `json:"url"`
	Mirrors   []string `json:"mirrors"`
	Upstream  string   `json:"upstream"`
	SHA256    string   `json:"sha256"`
	SizeBytes int64    `json:"size_bytes"`
	Extract   struct {
		Format          string `json:"format"`
		StripComponents int    `json:"strip_components"`
	} `json:"extract"`
	InstallTo string            `json:"install_to"`
	Exposes   map[string]string `json:"exposes"`
	Kind      string            `json:"kind"`
	Version   string            `json:"version"`
}

// Manifest lists the assets and which of them each chip needs per platform.
type Manifest struct {
	SchemaVersion    int                            `json:"schema_version"`
	ManifestVersion  int64                          `json:"manifest_version"`
	Assets           map[string]Asset               `json:"assets"`
	ChipRequirements map[string]map[string][]string `json:"chip_requirements"`
	Signature        string                         `json:"signature"`
}

var fallback = mustParse(fallbackJSON)

func mustParse(b []byte) *Manifest {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		panic("toolchain: bad embedded manifest: " + err.Error())
	}
	return &m
}

// Asset looks key up, falling back to the embedded manifest for assets a
// published manifest predates.
func (m *Manifest) Asset(key string) (Asset, bool) {
	if a, ok := m.Assets[key]; ok {
		return a, true
	}
	a, ok := fallback.Assets[key]
	return a, ok
}

// Requirements returns the asset keys chip needs on platform.
func (m *Manifest) Requirements(chip, platform string) []string {
	if keys, ok := m.ChipRequirements[chip][platform]; ok {
		return keys
	}
	return fallback.ChipRequirements[chip][platform]
}

// LoadManifest returns the manifest and where it came from: a fresh verified
// cache, the network (only when online), a stale verified cache, or the
// embedded fallback. Anything not embedded must carry a valid signature and be
// at or above the rollback floor.
func LoadManifest(online bool) (*Manifest, string) {
	cache := filepath.Join(voltHome(), "cache", "manifest.json")
	if st, err := os.Stat(cache); err == nil && time.Since(st.ModTime()) < cacheTTL {
		if m := readTrusted(cache); m != nil {
			return m, "cache"
		}
	}
	if online {
		if raw, err := httpGet(manifestURL); err == nil {
			if m, err := trusted(raw); err == nil {
				os.MkdirAll(filepath.Dir(cache), 0o755)
				os.WriteFile(cache, raw, 0o644)
				recordVersion(m.ManifestVersion)
				return m, "remote"
			}
		}
	}
	if m := readTrusted(cache); m != nil {
		return m, "stale cache"
	}
	return fallback, "built-in"
}

func readTrusted(path string) *Manifest {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m, _ := trusted(raw)
	return m
}

func httpGet(url string) ([]byte, error) {
	c := http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// trusted parses a fetched or cached manifest and accepts it only if it is
// compatible, signed by a pinned key, not revoked and not rolled back.
func trusted(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.SchemaVersion != 1 || m.Assets == nil {
		return nil, errors.New("incompatible manifest")
	}
	if err := verifySignature(raw, m.Signature, m.ManifestVersion); err != nil {
		return nil, err
	}
	if floor := rollbackFloor(); m.ManifestVersion < floor {
		return nil, fmt.Errorf("rollback: v%d < accepted v%d", m.ManifestVersion, floor)
	}
	return &m, nil
}

func versionPath() string { return filepath.Join(voltHome(), "cache", "manifest-version") }

func rollbackFloor() int64 {
	b, _ := os.ReadFile(versionPath())
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func recordVersion(n int64) {
	if n > rollbackFloor() {
		os.WriteFile(versionPath(), []byte(strconv.FormatInt(n, 10)), 0o644)
	}
}

// verifySignature checks sig ("ed25519:<base64>") over the canonical JSON of
// the manifest minus its signature field — byte-identical to the IDE's
// toolchain-trust.js, so manifests signed by the publisher verify here too.
func verifySignature(raw []byte, sig string, version int64) error {
	b64, ok := strings.CutPrefix(sig, "ed25519:")
	if !ok {
		return errors.New("missing/unknown signature")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("malformed signature")
	}

	var keyset struct {
		Keys []struct {
			ID           string `json:"id"`
			PublicKeyPem string `json:"publicKeyPem"`
			Status       string `json:"status"`
		} `json:"keys"`
		Revoked []string `json:"revoked"`
	}
	json.Unmarshal(keysJSON, &keyset)
	if slices.Contains(keyset.Revoked, strconv.FormatInt(version, 10)) {
		return errors.New("revoked manifest version")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	delete(doc, "signature")
	var msg strings.Builder
	canonical(&msg, doc)

	for _, k := range keyset.Keys {
		if k.Status == "revoked" {
			continue
		}
		block, _ := pem.Decode([]byte(k.PublicKeyPem))
		if block == nil {
			continue
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if key, ok := pub.(ed25519.PublicKey); err == nil && ok && ed25519.Verify(key, []byte(msg.String()), sigBytes) {
			return nil
		}
	}
	return errors.New("signature not verified by any pinned key")
}

// canonical writes v as JSON with recursively sorted object keys, formatted
// exactly as JavaScript's JSON.stringify would (vlib-verify.js canonical()).
func canonical(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		b.WriteString(jsNumber(x))
	case string:
		jsString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			canonical(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jsString(b, k)
			b.WriteByte(':')
			canonical(b, x[k])
		}
		b.WriteByte('}')
	}
}

func jsNumber(n json.Number) string {
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "null"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func jsString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
