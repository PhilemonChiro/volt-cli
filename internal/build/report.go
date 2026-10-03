// Package build turns compiled Volt C into firmware: it generates a
// per-project build directory against the read-only framework and runs the
// chip's native build, reporting progress the way the IDE shows it.
package build

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/term"
)

// Event is one progress report. With --json each is printed as a line; the
// shape matches the IDE's build:progress IPC message. Stage is compile,
// build, flash or done (progress), or info, warn or error (messages).
type Event struct {
	Stage    string `json:"stage"`
	Message  string `json:"message"`
	Percent  int    `json:"percent"`
	Artifact string `json:"artifact,omitempty"` // on done
	Size     *Size  `json:"size,omitempty"`     // on done, when known
}

// Size is the firmware's footprint in bytes.
type Size struct {
	Flash    int64 `json:"flash"`               // app image
	FlashMax int64 `json:"flash_max,omitempty"` // app partition
	RAM      int64 `json:"ram,omitempty"`       // static DRAM (data + bss)
}

// Reporter prints events as JSON lines or for a terminal.
type Reporter struct {
	Verbose bool  // show every tool output line
	Size    *Size // footprint to report with Done, if known

	mu       sync.Mutex
	json     *json.Encoder
	out      *term.Writer
	onLine   bool // a progress line is showing and must be cleared first
	lastProg time.Time
}

// NewReporter returns a Reporter writing to stdout.
func NewReporter(asJSON, verbose bool) *Reporter {
	r := &Reporter{Verbose: verbose, out: term.New(os.Stdout)}
	if asJSON {
		r.json = json.NewEncoder(os.Stdout)
		r.json.SetEscapeHTML(false)
	}
	return r
}

func (r *Reporter) clearLine() {
	if r.onLine {
		r.out.Printf("\r\x1b[K")
		r.onLine = false
	}
}

// Step reports a milestone; terminals always show it.
func (r *Reporter) Step(stage, msg string, pct int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(Event{Stage: stage, Message: msg, Percent: pct})
		return
	}
	r.clearLine()
	r.out.Printf("[bold cyan]→[/bold cyan] %s\n", msg)
}

// Progress reports frequent progress (ninja steps), throttled to 5/s.
// Terminals show it on one line that is rewritten in place.
func (r *Reporter) Progress(stage, msg string, pct int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.lastProg) < 200*time.Millisecond {
		return
	}
	r.lastProg = time.Now()
	if r.json != nil {
		r.json.Encode(Event{Stage: stage, Message: msg, Percent: pct})
		return
	}
	if r.out.TTY && !r.Verbose {
		r.out.Printf("\r\x1b[K  [cyan]%3d%%[/cyan] [dim]%s[/dim]", pct, msg)
		r.onLine = true
	}
}

// Message reports an info, warn or error line.
func (r *Reporter) Message(stage, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(Event{Stage: stage, Message: msg})
		return
	}
	r.clearLine()
	switch stage {
	case "error":
		r.out.Printf("  [red]%s[/red]\n", msg)
	case "warn":
		r.out.Printf("  [yellow]%s[/yellow]\n", msg)
	default:
		r.out.Printf("  %s\n", msg)
	}
}

// Raw prints an unfiltered tool output line (verbose mode).
func (r *Reporter) Raw(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(Event{Stage: "info", Message: line})
		return
	}
	r.clearLine()
	r.out.Printf("  [dim]%s[/dim]\n", line)
}

// Done reports success with the firmware image.
func (r *Reporter) Done(msg, artifact string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(Event{Stage: "done", Message: msg, Percent: 100, Artifact: artifact, Size: r.Size})
		return
	}
	r.clearLine()
	r.out.Printf("\n[bold green]✓[/bold green] %s\n  [dim]%s[/dim]\n", msg, artifact)
	if r.Size != nil {
		r.out.Printf("  [dim]%s[/dim]\n", r.Size)
	}
	r.out.Printf("\n")
}

// Fail reports failure.
func (r *Reporter) Fail(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(Event{Stage: "error", Message: msg})
		return
	}
	r.clearLine()
	r.out.Printf("\n[red]✗ %s[/red]\n\n", msg)
}
