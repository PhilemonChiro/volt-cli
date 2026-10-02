package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/term"
)

// Event is one install event. With --json each is printed as a line; the
// shapes match the IDE's toolchain:event IPC messages.
type Event struct {
	Type    string `json:"type"` // stage_start, stage_done, stage_error, stage_progress, log, complete
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	Percent *int   `json:"percent,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Success *bool  `json:"success,omitempty"`
}

// Emitter reports install progress as JSON lines or for a terminal. Log lines
// from tools (git, idf_tools.py, apt) are scanned for progress, as the IDE did.
type Emitter struct {
	mu       sync.Mutex
	json     *json.Encoder
	out      *term.Writer
	stage    string
	onLine   bool // a progress line is showing and must be cleared first
	lastProg time.Time
	toolPlan int
	toolDone int
	tool     string
}

// NewEmitter returns an Emitter writing to stdout.
func NewEmitter(asJSON bool) *Emitter {
	e := &Emitter{out: term.New(os.Stdout)}
	if asJSON {
		e.json = json.NewEncoder(os.Stdout)
		e.json.SetEscapeHTML(false)
	}
	return e
}

func (e *Emitter) send(ev Event) { e.json.Encode(ev) }

func (e *Emitter) clearLine() {
	if e.onLine {
		e.out.Printf("\r\x1b[K")
		e.onLine = false
	}
}

// StageStart begins stage id (a component), shown as name.
func (e *Emitter) StageStart(id, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stage, e.toolPlan, e.toolDone = id, 0, 0
	if e.json != nil {
		e.send(Event{Type: "stage_start", ID: id, Name: name})
		return
	}
	e.clearLine()
	e.out.Printf("[bold cyan]→[/bold cyan] %s\n", name)
}

// StageDone ends the current stage successfully.
func (e *Emitter) StageDone(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stage = ""
	if e.json != nil {
		e.send(Event{Type: "stage_done", ID: id})
		return
	}
	e.clearLine()
}

// StageError ends the current stage with a failure.
func (e *Emitter) StageError(id, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stage = ""
	if e.json != nil {
		e.send(Event{Type: "stage_error", ID: id, Message: msg})
		return
	}
	e.clearLine()
	e.out.Printf("  [red]✗ %s[/red]\n", msg)
}

// Complete reports the overall result.
func (e *Emitter) Complete(ok bool, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.json != nil {
		e.send(Event{Type: "complete", Success: &ok, Message: msg})
		return
	}
	e.clearLine()
	if ok {
		e.out.Printf("\n[bold green]✓[/bold green] %s\n", msg)
	} else {
		e.out.Printf("\n[red]✗ %s[/red]\n", msg)
	}
}

// Progress reports percent (0–100) within the current stage.
func (e *Emitter) Progress(pct int, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress(pct, detail)
}

// progress sends a high-frequency update, throttled to 5/s (100% always goes).
func (e *Emitter) progress(pct int, detail string) {
	if pct < 100 && time.Since(e.lastProg) < 200*time.Millisecond {
		return
	}
	e.step(pct, detail)
}

// step sends a milestone update, never throttled.
func (e *Emitter) step(pct int, detail string) {
	if e.stage == "" {
		return
	}
	e.lastProg = time.Now()
	if e.json != nil {
		e.send(Event{Type: "stage_progress", ID: e.stage, Percent: &pct, Detail: detail})
		return
	}
	if e.out.TTY {
		e.out.Printf("\r\x1b[K  [cyan]%3d%%[/cyan] [dim]%s[/dim]", pct, detail)
		e.onLine = true
	}
}

var (
	gitRe      = regexp.MustCompile(`(?:Receiving objects|Resolving deltas):\s+(\d+)%\s*\(([\d.]+)/([\d.]+)\)`)
	toolPlanRe = regexp.MustCompile(`^Installing tools:\s+(.+)$`)
	toolRe     = regexp.MustCompile(`^(?:Installing|Skipping)\s+([^\s@]+)@`)
	venvRe     = regexp.MustCompile(`(?i)^(Installing Python|Setting up Python|Successfully installed)`)
	percentRe  = regexp.MustCompile(`^\s*(\d{1,3})%$`) // idf_tools.py per-download progress
)

// Log prints a tool's output line and derives progress from it.
func (e *Emitter) Log(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return
	}
	if m := gitRe.FindStringSubmatch(line); m != nil {
		var pct int
		fmt.Sscan(m[1], &pct)
		e.progress(pct, fmt.Sprintf("git: %s / %s objects", m[2], m[3]))
		return // git rewrites these lines with \r; don't log each one
	}
	if m := percentRe.FindStringSubmatch(line); m != nil {
		if e.toolPlan > 0 && e.toolDone > 0 {
			var pct int
			fmt.Sscan(m[1], &pct)
			e.progress(((e.toolDone-1)*100+pct)/e.toolPlan, fmt.Sprintf("tool %d / %d: %s %s%%", e.toolDone, e.toolPlan, e.tool, m[1]))
		}
		return
	}
	if e.json != nil {
		e.send(Event{Type: "log", Message: line})
	} else {
		e.clearLine()
		e.out.Printf("  [dim]%s[/dim]\n", line)
	}

	switch {
	case toolPlanRe.MatchString(line):
		e.toolPlan = len(strings.Split(toolPlanRe.FindStringSubmatch(line)[1], ","))
		e.toolDone = 0
		e.step(0, fmt.Sprintf("0 / %d tools", e.toolPlan))
	case e.toolPlan > 0 && toolRe.MatchString(line):
		e.toolDone++
		e.tool = toolRe.FindStringSubmatch(line)[1]
		e.step((e.toolDone-1)*100/e.toolPlan, fmt.Sprintf("tool %d / %d: %s", e.toolDone, e.toolPlan, e.tool))
	case e.toolPlan > 0 && venvRe.MatchString(line):
		e.step(100, "Python environment")
	}
}
