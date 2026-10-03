package device

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Frame is one decoded code address.
type Frame struct {
	PC       string `json:"pc"`
	Func     string `json:"func"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Inlined  bool   `json:"inlined,omitempty"` // inlined into the frame above
	Resolved bool   `json:"resolved"`
}

// Decoder turns the code addresses in ESP-IDF panic output (backtraces,
// "abort() was called at PC", the register dump's PC) into functions and
// source lines with addr2line against the app's ELF. (The register dump's
// PC is not decoded: it repeats the backtrace's first frame.) Generated C carries
// #line directives, so Volt code decodes to main.volt lines.
type Decoder struct {
	elf, addr2line string
	sha            string // hex SHA-256 of the ELF
	warned         bool
	pending        []Frame // decoded, held until the panic's ELF hash confirms the build
}

// NewDecoder returns nil when the ELF or addr2line is missing.
func NewDecoder(elf, addr2line string) *Decoder {
	b, err := os.ReadFile(elf)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(addr2line); err != nil {
		return nil
	}
	sum := sha256.Sum256(b)
	return &Decoder{elf: elf, addr2line: addr2line, sha: hex.EncodeToString(sum[:])}
}

var (
	backtraceRe = regexp.MustCompile(`0x(4[0-9a-fA-F]{7}):0x[0-9a-fA-F]{8}`)
	singlePCRe  = regexp.MustCompile(`abort\(\) was called at PC *(0x4[0-9a-fA-F]{7})`)
	elfSHARe    = regexp.MustCompile(`ELF file SHA256:\s*([0-9a-fA-F]+)`)
	a2lRe       = regexp.MustCompile(`^(?:0x[0-9a-fA-F]+: )?(?:\(inlined by\) )?(.*?) at (.*?):(\d+|\?)`)
)

// Line inspects one monitor line. ESP-IDF's panic handler prints the
// running app's ELF hash after the backtrace, so decoded frames are held
// until that line: they are returned if the hash matches the ELF, dropped
// with a warning if not (the frames would name the wrong code). A reboot or
// halt line without a hash releases them unverified.
func (d *Decoder) Line(s string) (frames []Frame, warning string) {
	if d == nil {
		return nil, ""
	}
	if m := elfSHARe.FindStringSubmatch(s); m != nil {
		held := d.pending
		d.pending = nil
		if strings.HasPrefix(d.sha, strings.ToLower(m[1])) {
			return held, ""
		}
		if len(held) > 0 || !d.warned {
			d.warned = true
			return nil, "The board runs a different build than " + d.elf + "; not decoding its backtrace. Flash this build first."
		}
		return nil, ""
	}
	if strings.HasPrefix(s, "Rebooting...") || strings.Contains(s, "CPU halted") {
		held := d.pending
		d.pending = nil
		return held, ""
	}
	var pcs []string
	if strings.Contains(s, "Backtrace:") {
		for _, m := range backtraceRe.FindAllStringSubmatch(s, -1) {
			pcs = append(pcs, "0x"+m[1])
		}
	} else if m := singlePCRe.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		pcs = []string{m[1]}
	}
	if len(pcs) > 0 {
		d.pending = append(d.pending, d.decode(pcs)...)
	}
	return nil, ""
}

func (d *Decoder) decode(pcs []string) []Frame {
	out, err := exec.Command(d.addr2line, append([]string{"-pfiaC", "-e", d.elf}, pcs...)...).Output()
	if err != nil {
		return nil
	}
	var frames []Frame
	pc := 0
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		inlined := strings.HasPrefix(strings.TrimSpace(l), "(inlined by)")
		f := Frame{Inlined: inlined}
		if !inlined && pc < len(pcs) {
			f.PC = pcs[pc]
			pc++
		}
		if m := a2lRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil && m[1] != "??" {
			f.Func, f.File, f.Resolved = m[1], m[2], true
			f.Line, _ = strconv.Atoi(m[3])
		} else {
			f.Func = "??"
		}
		frames = append(frames, f)
	}
	return frames
}
