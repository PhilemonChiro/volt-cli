package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PhilemonChiro/volt-cli/internal/libtrust"
	"github.com/PhilemonChiro/volt-cli/internal/sdkfiles"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/history"
	compiler "github.com/PhilemonChiro/volt-compiler"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ide_* tools drive a running Volt IDE through its agent bridge
// (volt-ide/src/main/agent-bridge.js), found via ~/.volt/ide.json.

const ideInstructions = `

If the Volt IDE is running (ide_status answers), work in it so the user can watch, and only on the project it has open — the file tools (check with a path, review, build) refuse other projects while the IDE runs. Change the open program only with ide_edit (the user sees it and can undo it), never by writing the project's files with your own file tools. Know which project you are in: start with ide_status. If the user's request is about a different project, find it with ide_projects and switch with ide_open (if the open file has unsaved changes, ide_save or ask the user first). If no project fits, create one with ide_new_project (a short name, the chip, and your program as source) — it opens in the IDE. Then: ide_read the open program, change it with ide_edit (check and review as usual), show the user a view with ide_show (state machine and sequence diagrams; the Wiring view of pins and connections) and take an ide_screenshot only when you must see it yourself (checking a diagram you changed, or the user asks what it looks like), build with ide_build, and after ide_run flashes the board confirm the behaviour with ide_serial. To show the user things, use ide_point (notes on code or IDE parts) and ide_walkthrough (step-by-step tour).

Act, don't interview: when the user describes what they want (e.g. "detect gas leaks in the kitchen and alert"), design and propose a complete program with sensible defaults — free pins that don't clash with the hardware block, the usual part for the job, standard thresholds and alerts (buzzer, LED, LCD message) — and state the assumptions in one line so they can correct you. Ask only when an answer really changes the program and can't be defaulted, one question at a time. When a part has no Volt driver, follow device_card's next: use a pin type for simple analog/digital parts, or write the driver with library_write.

Asking in the IDE: when you need a decision about the code while the user is working in Volt IDE (a choice between approaches, a value only they know, whether to apply a change), you may ask with ide_ask, anchored to the code it is about, with a few clear choices and/or a text answer; it waits for their reply. This is optional — use your judgement: ask in the IDE when pointing at the code helps, ask in chat otherwise, and don't ask when the request already answers it.

Writing a device library: it is one folder, <device library>/<prefix>/, holding device.json (name, interface, methods with params and returns — see an existing device with device_card), driver.c (the C implementation, functions named <prefix>_<method>) and header.h (the <prefix>_t struct typedef and a prototype for every <prefix>_* function; include only standard and volt/ headers). The build includes header.h automatically. Never edit Volt's framework files (framework/include/volt/*.h and the runtime) — a library never needs to. ide_status shows the device library folders the IDE and volt build use.

Who may change a library: Volt's official libraries and ones installed from the Grid are never changed — not their methods, not their code. A shared library holds only what any project using that part needs. Methods one project needs (its SMS commands, a whitelist, a custom report format) go in a project library: library_write with scope "project" and a new prefix (e.g. sim7000lite_app), in the open project's devices/ folder; it builds on the shared driver and can't replace it. Plain logic can also just be functions in the program. If a shared driver has a real bug or lacks something every user needs, tell the user instead of changing it.

Device libraries need the user's approval. Any new or changed device library (a folder under the device library with device.json/driver.c, or the framework) is blocked from builds until the user approves it in Volt IDE; the IDE opens a review sheet the moment it changes. Create or change libraries with library_write (it writes the files to the right place and asks for approval) rather than with your own file tools; after any other change call library_request_approval with its prefix and wait: it returns approved, rejected (with the user's note — fix what they said and ask again), later or timeout. Never try to work around a blocked build.`

var errNoIDE = errors.New("The Volt IDE isn't running (or AI agent control is off). Open the IDE, or use the file tools (check, review, build).")

// ideCall sends one bridge request and returns its result.
func ideCall(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".volt", "ide.json"))
	if err != nil {
		return nil, errNoIDE
	}
	var info struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
		PID   int    `json:"pid"`
	}
	if json.Unmarshal(data, &info) != nil || info.Port == 0 || !processAlive(info.PID) {
		// A stale file from a crashed IDE: its port may now belong to another
		// program, which must not be sent the token.
		return nil, errNoIDE
	}
	body, _ := json.Marshal(map[string]any{"method": method, "params": params})
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d/rpc", info.Port), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+info.Token)
	if name, _ := clientName.Load().(string); name != "" {
		req.Header.Set("X-Volt-Client", name)
	}
	// The Claude Code session this server runs in, so the IDE can send a
	// question from the editor to the session that is working in it.
	// Set by the IDE for its own chat agents, which already asked the user.
	if host := os.Getenv("VOLT_AGENT_HOST"); host != "" {
		req.Header.Set("X-Volt-Host", host)
	}
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" {
		req.Header.Set("X-Volt-Session", sid)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the IDE did not answer %s within %s", method, timeout)
		}
		return nil, errNoIDE
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("bad answer from the IDE (HTTP %d)", resp.StatusCode)
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	return out.Result, nil
}

// clientName is the AI app on the other end of this MCP session ("Cursor").
var clientName atomic.Value

// helloIDE tells a running IDE which app just connected, so it can show
// "Cursor connected" before the agent does anything.
func helloIDE(ctx context.Context, req *mcp.InitializedRequest) {
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil {
		return
	}
	name := friendlyClient(p.ClientInfo)
	clientName.Store(name)
	go ideCall(context.Background(), "agent_hello", map[string]string{"client": name}, 5*time.Second)
}

func friendlyClient(c *mcp.Implementation) string {
	n := strings.ToLower(c.Name)
	for _, k := range []struct{ match, name string }{
		{"claude-code", "Claude Code"}, {"claude-ai", "Claude Desktop"}, {"cursor", "Cursor"},
		{"visual studio code", "VS Code"}, {"vscode", "VS Code"}, {"windsurf", "Windsurf"},
		{"codex", "Codex"}, {"gemini", "Gemini CLI"}, {"zed", "Zed"},
	} {
		if strings.Contains(n, k.match) {
			return k.name
		}
	}
	if c.Title != "" {
		return c.Title
	}
	if c.Name != "" {
		return c.Name
	}
	return "AI agent"
}

// processAlive reports whether pid is a running process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // FindProcess opens the process, so it exists
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func jsonResult(raw json.RawMessage) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

type ideOpenIn struct {
	Path string `json:"path" jsonschema:"a Volt project directory or .volt file to open in the IDE"`
}

type ideEdit struct {
	StartLine int    `json:"start_line" jsonschema:"first line, 1-based"`
	StartCol  int    `json:"start_col,omitempty" jsonschema:"default 1"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"default start_line"`
	EndCol    int    `json:"end_col,omitempty" jsonschema:"default end of end_line"`
	Text      string `json:"text" jsonschema:"replacement text"`
}

type ideEditIn struct {
	Source string    `json:"source,omitempty" jsonschema:"the whole new program; or use edits"`
	Edits  []ideEdit `json:"edits,omitempty" jsonschema:"ranged replacements, applied together"`
	Save   bool      `json:"save,omitempty" jsonschema:"save the file afterwards"`
}

type ideNewProjectIn struct {
	Name     string `json:"name" jsonschema:"folder name: letters, numbers, spaces, - and _"`
	Chip     string `json:"chip" jsonschema:"target chip, e.g. esp32 (default) or rp2040"`
	Source   string `json:"source,omitempty" jsonschema:"the program for main.volt (recommended; run check on it first)"`
	Template string `json:"template,omitempty" jsonschema:"without source: blink (default), empty, events, timers, state-machine or network"`
}

type ideHighlightIn struct {
	Lines []int  `json:"lines" jsonschema:"1-based lines to mark; empty clears the marks"`
	Note  string `json:"note,omitempty" jsonschema:"shown when the user hovers a marked line"`
}

// ideAnchor places a mark, step or question: code (line…, or text) or a part
// of the IDE (target).
type ideAnchor struct {
	Line       int    `json:"line,omitempty" jsonschema:"1-based start line"`
	EndLine    int    `json:"end_line,omitempty" jsonschema:"last line (default: line)"`
	Col        int    `json:"col,omitempty" jsonschema:"1-based start column (default: line start)"`
	EndCol     int    `json:"end_col,omitempty" jsonschema:"end column, exclusive (default: line end)"`
	Find       string `json:"find,omitempty" jsonschema:"exact code to point at instead of line/col, e.g. a name or statement; searched from line if given"`
	Occurrence int    `json:"occurrence,omitempty" jsonschema:"with find: which match, 1-based (default 1)"`
	EndFind    string `json:"end_find,omitempty" jsonschema:"with find: stretch the mark to the end of the first match of this code after find, to cover several lines (e.g. an if and its branches)"`
	Target     string `json:"target,omitempty" jsonschema:"a part of the IDE instead of code: check, run, monitor, save, export, format, settings, ai, new, open, editor, diagrams, board, output, side, toolbar, hardware, outline or library"`
}

type ideNoteAction struct {
	Label string    `json:"label" jsonschema:"button text, a few words (e.g. Fix it, Explain, Open Hardware)"`
	Run   string    `json:"run,omitempty" jsonschema:"IDE command to run on click: check, run, monitor, save, format, settings, panel:hardware, panel:outline, panel:library, board or addPart"`
	Edits []ideEdit `json:"edits,omitempty" jsonschema:"code edits to apply on click (one undo step), e.g. a proposed fix"`
}

type idePointMark struct {
	ideAnchor
	Note    string          `json:"note,omitempty" jsonschema:"shown as a bubble after the code (or beside the IDE part); one short sentence; code spans and bold allowed"`
	Kind    string          `json:"kind,omitempty" jsonschema:"info (default), change, warning or question — sets the colour"`
	Persist string          `json:"persist,omitempty" jsonschema:"dismissable (default: the user closes it with ×), fixed (stays until you clear it) or timeout (fades after seconds)"`
	Seconds int             `json:"seconds,omitempty" jsonschema:"with persist=timeout: how long it shows (default 8, max 600)"`
	Actions []ideNoteAction `json:"actions,omitempty" jsonschema:"up to 3 buttons on the note; every click is recorded for ide_note_events"`
}

type idePointIn struct {
	Marks []idePointMark `json:"marks" jsonschema:"places to point at; empty clears all marks"`
	Keep  bool           `json:"keep,omitempty" jsonschema:"add to the current marks instead of replacing them"`
}

type ideStep struct {
	ideAnchor
	Title string `json:"title,omitempty" jsonschema:"step heading"`
	Text  string `json:"text" jsonschema:"what to tell the user at this step (short; code spans and bold allowed)"`
}

type ideWalkthroughIn struct {
	Title    string    `json:"title,omitempty" jsonschema:"walkthrough name"`
	Steps    []ideStep `json:"steps" jsonschema:"1–30 steps, each anchored to code or an IDE part (or neither)"`
	TimeoutS int       `json:"timeout_s,omitempty" jsonschema:"give up after this many seconds (default 600, max 1800)"`
}

type ideAskIn struct {
	Question  string     `json:"question" jsonschema:"what to ask the user"`
	Choices   []string   `json:"choices,omitempty" jsonschema:"up to 8 answer buttons; the first is the default"`
	AllowText *bool      `json:"allow_text,omitempty" jsonschema:"also offer a typed answer (default: only when there are no choices)"`
	Anchor    *ideAnchor `json:"anchor,omitempty" jsonschema:"show the question next to this code or IDE part"`
	TimeoutS  int        `json:"timeout_s,omitempty" jsonschema:"give up after this many seconds (default 600, max 1800)"`
}

type ideShowIn struct {
	View    string `json:"view" jsonschema:"code, diagrams or board"`
	Diagram string `json:"diagram,omitempty" jsonschema:"with view=diagrams: state or sequence"`
}

type ideScreenshotIn struct {
	Target   string `json:"target,omitempty" jsonschema:"window (default), editor, diagrams, board, output, side or toolbar"`
	MaxWidth int    `json:"max_width,omitempty" jsonschema:"downscale wider images to this many pixels (default 1600)"`
}

type ideSerialIn struct {
	Seconds int   `json:"seconds,omitempty" jsonschema:"how long to listen before answering (default 5, max 60)"`
	Since   int   `json:"since,omitempty" jsonschema:"only lines after this position (the previous answer's next)"`
	Start   *bool `json:"start,omitempty" jsonschema:"start the monitor if it is off (default true)"`
}

// addIDETools registers the ide_* tools; allowed bounds ide_open paths.
func addIDETools(s *mcp.Server, allowed []string) {
	simple := func(name, method, desc string, timeout time.Duration, ro bool) {
		ann := &mcp.ToolAnnotations{ReadOnlyHint: ro}
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Annotations: ann},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				raw, err := ideCall(ctx, method, map[string]any{}, timeout)
				if err != nil {
					return nil, nil, err
				}
				return jsonResult(raw), nil, nil
			})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "ide_status", Description: "What the running Volt IDE has open: project, file, chip, unsaved changes, selected board and port, every connected board (boards), serial monitor on/off, active view, and the device library folders builds use. Fails if the IDE isn't running.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "status", map[string]any{}, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			var st map[string]any
			if json.Unmarshal(raw, &st) == nil {
				if p, err := ideCall(ctx, "library_paths", map[string]any{}, 20*time.Second); err == nil {
					var lp map[string]any
					if json.Unmarshal(p, &lp) == nil {
						st["device_libraries"] = lp["devices"]
					}
				}
				raw, _ = json.Marshal(st)
			}
			return jsonResult(raw), nil, nil
		})
	simple("ide_read", "read", "The program open in the IDE editor, including unsaved changes, with the cursor and any selection.", 20*time.Second, true)
	simple("ide_save", "save", "Save the program open in the IDE.", 30*time.Second, false)
	simple("ide_diagnostics", "diagnostics", "The IDE's problems list (errors and warnings) for the program in the editor, unsaved changes included.", 30*time.Second, true)
	simple("ide_build", "build", "Build firmware in the IDE as if the user clicked Build; output shows in the IDE. Returns ok, firmware size, errors.", 21*time.Minute, false)
	type runIn struct {
		Port string `json:"port,omitempty" jsonschema:"serial port to flash, e.g. /dev/cu.usbserial-0001 (one of ide_status boards); empty = the board selected in the IDE"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "ide_run", Description: "Build, flash a board, and start its serial monitor. Flashes the board selected in the IDE, or `port` (ide_status lists the connected boards). The IDE asks the user once per session to allow flashing. Follow with ide_serial to confirm behaviour."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "run", map[string]any{"port": in.Port}, 21*time.Minute)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_projects", Description: "The user's Volt projects (projects folder plus recently opened ones), newest first: name, path, chip, last change, and which one is open in the IDE now. Use it to find the project a request is about before ide_open.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "projects", map[string]any{}, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			var out struct {
				ProjectsDir string           `json:"projects_dir"`
				Projects    []map[string]any `json:"projects"`
				Open        string           `json:"open"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, nil, err
			}
			if st, err := ideCall(ctx, "status", map[string]any{}, 20*time.Second); err == nil {
				var s struct {
					Project string `json:"project"`
				}
				if json.Unmarshal(st, &s) == nil {
					out.Open = s.Project
					for _, p := range out.Projects {
						p["open"] = p["path"] == s.Project
					}
				}
			}
			b, _ := json.Marshal(out)
			return jsonResult(b), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_new_project", Description: "Create a new Volt project in the user's projects folder and open it in the IDE. Give a short name, the chip, and ideally your program as source (check it first); without source it starts from a template. Refuses if the open file has unsaved changes or the name is taken."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideNewProjectIn) (*mcp.CallToolResult, any, error) {
			if st, err := ideCall(ctx, "status", map[string]any{}, 20*time.Second); err == nil {
				var s struct {
					File    string `json:"file"`
					Unsaved bool   `json:"unsaved"`
				}
				if json.Unmarshal(st, &s) == nil && s.Unsaved {
					return nil, nil, fmt.Errorf("%s has unsaved changes. Save them (ide_save) or ask the user first", filepath.Base(s.File))
				}
			}
			raw, err := ideCall(ctx, "new_project", in, 30*time.Second)
			if err != nil {
				return nil, nil, err
			}
			var made struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &made); err != nil {
				return nil, nil, err
			}
			if !within(made.Path, allowed) {
				return nil, nil, fmt.Errorf("created %s, but it is outside the directories this server may open (%s); ask the user to open it", made.Path, strings.Join(allowed, ", "))
			}
			raw, err = ideCall(ctx, "open", map[string]any{"path": made.Path}, 70*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_open", Description: "Switch the IDE to a Volt project directory (or .volt file), e.g. one found with ide_projects. Refuses if the open file has unsaved changes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideOpenIn) (*mcp.CallToolResult, any, error) {
			abs, err := filepath.Abs(in.Path)
			if err != nil {
				return nil, nil, err
			}
			if !within(abs, allowed) {
				return nil, nil, fmt.Errorf("%s is outside the allowed directories (%s)", abs, strings.Join(allowed, ", "))
			}
			raw, err := ideCall(ctx, "open", map[string]any{"path": abs}, 70*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_edit", Description: "Change the program in the IDE editor live, as one undo step the user can revert: give the whole new program as source, or ranged edits. Returns the new diagnostics. Run check/review on the result as usual."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideEditIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "edit", in, 40*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_highlight", Description: "Mark lines in the IDE editor (e.g. to point the user at what you changed or found), with an optional hover note. An empty list clears the marks.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideHighlightIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "highlight", in, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_point", Description: "Point the user at code or parts of the IDE: each mark highlights exact code (lines, columns, or find: the first match of some code) or an IDE part (e.g. the run button, the hardware panel) and shows its note as a ✦ bubble after the line (or beside the IDE part). Kinds colour them: info, change, warning, question. persist picks the lifetime: dismissable (user closes with ×), fixed (until you clear it) or timeout. actions add buttons that run an IDE command, apply your proposed edit, or just tell you (read clicks with ide_note_events). Marks also go when replaced, cleared (empty marks) or the user edits that code. Prefer this to ide_highlight.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in idePointIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "point", in, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_note_events", Description: "Which action buttons the user clicked on your ide_point notes (oldest first), with the mark id and label. Pass since=the previous answer's next to get only new clicks.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			Since int `json:"since,omitempty" jsonschema:"only clicks after this position"`
		}) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "note_events", in, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_history", Description: "The open project's change history, newest first: who made each change (the user, Volt Assistant and its model, an AI app, or an edit outside the IDE), when, what it said, and +/− lines. Give file to see one file's changes, line to trace who last changed that line. Read it before editing code the user recently changed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			File  string `json:"file,omitempty" jsonschema:"a file in the project, e.g. main.volt"`
			Line  int    `json:"line,omitempty" jsonschema:"with file: trace this line to the change that last touched it"`
			Limit int    `json:"limit,omitempty" jsonschema:"most changes to list (default 15)"`
		}) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "status", map[string]any{}, 10*time.Second)
			if err != nil {
				return nil, nil, err
			}
			var st struct {
				Project string `json:"project"`
			}
			if json.Unmarshal(raw, &st) != nil || st.Project == "" {
				return nil, nil, errors.New("no project is open in Volt IDE")
			}
			h, err := history.Open(st.Project)
			if err != nil {
				return nil, nil, err
			}
			var out any
			if in.Line > 0 && in.File != "" {
				lines, err := h.Blame(in.File)
				if err != nil {
					return nil, nil, err
				}
				if in.Line > len(lines) {
					return nil, nil, fmt.Errorf("%s has %d recorded lines", in.File, len(lines))
				}
				out = lines[in.Line-1]
			} else {
				if in.Limit <= 0 {
					in.Limit = 15
				}
				if out, err = h.Log(in.File, in.Limit); err != nil {
					return nil, nil, err
				}
			}
			b, _ := json.Marshal(out)
			return jsonResult(b), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "library_write", Description: "Create or update a device library (device.json, driver.c, header.h) in the device library the user's builds use, then ask the user to approve it in Volt IDE and wait (approved / rejected with their note / later / timeout). Use this instead of writing library files yourself. Look at an existing device with device_card for the device.json shape. The library must pass the publish checks or it never reaches the user (status failed_checks with the errors): header.h struct fields only uint8_t/16/32/64, int8_t/16/32/64, int, float, bool, char (fixed arrays ok); method returns float, int, bool, void or string; driver.c must not include <unistd.h> or <sys/…> or call system, exec*, popen, fork, dlopen, mmap, exit or abort, or use inline asm."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in libraryWriteIn) (*mcp.CallToolResult, any, error) {
			if !prefixRe.MatchString(in.Prefix) {
				return nil, nil, errors.New("prefix must be lowercase letters, digits and _ (2–32), starting with a letter")
			}
			var dj map[string]any
			if err := json.Unmarshal([]byte(in.DeviceJSON), &dj); err != nil {
				return nil, nil, fmt.Errorf("device_json is not valid JSON: %w", err)
			}
			if p, _ := dj["c_prefix"].(string); p != "" && p != in.Prefix {
				return nil, nil, fmt.Errorf("device_json c_prefix %q differs from prefix %q", p, in.Prefix)
			}
			if strings.TrimSpace(in.DriverC) == "" || strings.TrimSpace(in.HeaderH) == "" {
				return nil, nil, errors.New("driver_c and header_h are both required")
			}
			// The user's agent permissions (Settings, volt.toml [agent]) decide
			// before anything is written. Without the IDE nothing can be
			// approved, so builds refuse the library anyway.
			if _, err := ideCall(ctx, "agent_gate", map[string]any{"tool": "library_write", "scope": in.Scope}, 16*time.Minute); err != nil && !errors.Is(err, errNoIDE) {
				return nil, nil, err
			}
			shared := syncDevices(useDevices(""))
			if shared == "" {
				return nil, nil, errors.New("no device library folder found")
			}
			name, _ := dj["name"].(string)
			root := shared
			switch in.Scope {
			case "", "shared":
				if why := lockedLibrary(shared, in.Prefix); why != "" {
					return nil, nil, fmt.Errorf("%s is %s and can't be changed here. Put the methods this project needs in a project library instead: library_write with scope \"project\" and a new prefix (e.g. %s_%s)", in.Prefix, why, in.Prefix, "app")
				}
			case "project":
				root = compiler.ProjectDevicesDir
				if root == "" {
					return nil, nil, errors.New("no project is open in Volt IDE: open the project first (ide_open), then write its library")
				}
				if isDir(filepath.Join(shared, in.Prefix)) || (name != "" && compiler.Registry().Get(name) != nil && !compiler.Registry().Get(name).Project) {
					return nil, nil, fmt.Errorf("a shared library already uses the prefix or name of %q; a project library needs its own (e.g. %s_app)", in.Prefix, in.Prefix)
				}
			default:
				return nil, nil, errors.New(`scope must be "shared" or "project"`)
			}
			dir := filepath.Join(root, in.Prefix)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, err
			}
			pretty, _ := json.MarshalIndent(dj, "", "  ")
			for name, text := range map[string]string{"device.json": string(pretty) + "\n", "driver.c": in.DriverC, "header.h": in.HeaderH} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					return nil, nil, err
				}
			}
			out := map[string]any{"written": dir, "scope": map[bool]string{true: "project", false: "shared"}[root != shared]}
			if in.Wait != nil && !*in.Wait {
				out["next"] = "Call library_request_approval when you want the user to review it; builds refuse it until approved."
				b, _ := json.Marshal(out)
				return jsonResult(b), nil, nil
			}
			raw, err := ideCall(ctx, "library_request_approval", map[string]any{"prefix": in.Prefix}, 16*time.Minute)
			if err != nil {
				out["approval"] = "Volt IDE isn't running: the user must approve it there before it builds."
			} else {
				out["approval"] = json.RawMessage(raw)
			}
			b, _ := json.Marshal(out)
			return jsonResult(b), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "library_request_approval", Description: "Ask the user to review and approve a device library you wrote or changed. Volt IDE shows them its full code (with changes against the version they last approved) and waits for their decision: approved, rejected (with their note), later, or timeout. Builds refuse unapproved libraries, so call this after creating or editing one. A library failing the publish checks returns failed_checks with the errors instead, without asking the user."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			Prefix string `json:"prefix" jsonschema:"the library's folder name / C prefix, e.g. bme280"`
		}) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "library_request_approval", in, 16*time.Minute)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_select", Description: "Put the user's cursor/selection on code (lines, columns, or find: a code match) and scroll it into view.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideAnchor) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "select", in, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_walkthrough", Description: "Walk the user through code or the IDE step by step: each step highlights its code or IDE part and shows a card with your text and Back/Next. Waits until the user finishes or closes it, then returns status done, closed, question (they typed a question on a step — answer it, then continue from that step if useful), timeout or replaced, with the step they were on and the furthest they got. Keep steps short; use it to explain a program, a change, or how to do something in the IDE.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideWalkthroughIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "walkthrough", in, waitFor(in.TimeoutS))
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_ask", Description: "Ask the user a question in the IDE, anchored to the code (or IDE part) it is about, with answer buttons and/or a text box; the rest of the code dims so they see what you mean. Waits for the answer: status answered (with choice or text), closed, timeout or replaced. Optional: use it when a decision about specific code is easier to answer while looking at it; otherwise ask in chat.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideAskIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "ask", in, waitFor(in.TimeoutS))
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_show", Description: "Switch the IDE's main view for the user: code, diagrams (state machine or sequence diagram, generated from the program) or board (the Wiring view: every pin and connection, buses included). Use board when the user asks about wiring, connections or pins. Take an ide_screenshot only when you must see the view yourself.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideShowIn) (*mcp.CallToolResult, any, error) {
			raw, err := ideCall(ctx, "show", in, 20*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_screenshot", Description: "A PNG screenshot of the IDE window or one area of it (editor, diagrams, board, output, side, toolbar) — exactly what the user sees.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideScreenshotIn) (*mcp.CallToolResult, any, error) {
			if in.Target == "" {
				in.Target = "window"
			}
			if in.MaxWidth == 0 {
				in.MaxWidth = 1600
			}
			raw, err := ideCall(ctx, "screenshot", in, 30*time.Second)
			if err != nil {
				return nil, nil, err
			}
			var shot struct {
				PNG    string `json:"png"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			}
			if err := json.Unmarshal(raw, &shot); err != nil {
				return nil, nil, err
			}
			png, err := base64.StdEncoding.DecodeString(shot.PNG)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: png, MIMEType: "image/png"},
				&mcp.TextContent{Text: fmt.Sprintf("%s screenshot, %dx%d", in.Target, shot.Width, shot.Height)},
			}}, nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ide_serial", Description: "Read the IDE's serial monitor for the selected board: listens for `seconds`, then returns the lines printed since `since` and the `next` position to pass on the following call. Starts the monitor if it is off.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideSerialIn) (*mcp.CallToolResult, any, error) {
			if in.Seconds == 0 {
				in.Seconds = 5
			}
			start := in.Start == nil || *in.Start
			raw, err := ideCall(ctx, "serial", map[string]any{"seconds": in.Seconds, "since": in.Since, "start": start},
				time.Duration(min(in.Seconds, 60)+20)*time.Second)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(raw), nil, nil
		})
}

// waitFor is how long to wait on a card the user answers (renderer default
// 600 s, cap 1800 s), plus slack for the bridge.
func waitFor(timeoutS int) time.Duration {
	if timeoutS <= 0 {
		timeoutS = 600
	}
	return time.Duration(min(timeoutS, 1800)+60) * time.Second
}

// requireOpenProject keeps agents on the project the user can see: while Volt
// IDE is running, file-based tools only work on the project it has open.
// Without the IDE there is nothing to watch, so anything goes.
func requireOpenProject(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := ideCall(ctx, "status", map[string]any{}, 3*time.Second)
	if err != nil {
		return nil // IDE not running (or not answering)
	}
	var st struct {
		Project string `json:"project"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	abs, _ := filepath.Abs(path)
	if st.Project == "" {
		return fmt.Errorf("Volt IDE is open but has no project open. Open this one with ide_open (or ide_new_project) first so the user can watch, then try again")
	}
	if within(abs, []string{st.Project}) {
		return nil
	}
	return fmt.Errorf("Volt IDE has %s open, not %s. Work on the project the user has open; if they asked for this one, switch with ide_open first (ask if unsure). To try a program without a project, pass it as source", st.Project, abs)
}

// syncDevices points the compiler at the device library Volt IDE builds with
// while the IDE runs (else the server's own), so an agent's checks and builds
// see exactly the devices the user's do. It returns the dir in use.
func syncDevices(fallback string) string {
	dir := fallback
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if raw, err := ideCall(ctx, "library_paths", map[string]any{}, 3*time.Second); err == nil {
		var lp struct {
			IDE     string  `json:"ide_builds_use"`
			Project *string `json:"project_devices"`
		}
		if json.Unmarshal(raw, &lp) == nil && lp.IDE != "" && isDir(lp.IDE) {
			dir = lp.IDE
		}
		// The open project's own libraries, for programs passed as text.
		if lp.Project != nil {
			compiler.ProjectDevicesDir = *lp.Project
		}
	}
	if dir != "" {
		compiler.DevicesDir = dir
	}
	return dir
}

var prefixRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

type libraryWriteIn struct {
	Prefix     string `json:"prefix" jsonschema:"folder name and C prefix, lowercase, e.g. tmp117"`
	DeviceJSON string `json:"device_json" jsonschema:"the full device.json (name, category, interface, default_address, description, c_prefix, c_source: driver.c, methods with description/params/returns, plus manufacturer, specs, wiring, datasheet when known)"`
	DriverC    string `json:"driver_c" jsonschema:"driver.c: the C implementation, functions named <prefix>_<method>, plus <prefix>_init"`
	HeaderH    string `json:"header_h" jsonschema:"header.h: the <prefix>_t struct typedef and a prototype for every <prefix>_* function; standard and volt/ headers only"`
	Wait       *bool  `json:"wait,omitempty" jsonschema:"wait for the user's approval in Volt IDE (default true)"`
	Scope      string `json:"scope,omitempty" jsonschema:"where it goes: shared (default) — a general driver for a part, available to every project; project — methods only the open project needs, in its devices/ folder (a new prefix; it can't replace a shared library)"`
}

// lockedLibrary says why a shared library can't be changed on this computer
// ("" when it can): Volt's own libraries and ones installed from the Grid
// stay as their authors made them.
func lockedLibrary(root, prefix string) string {
	dir := filepath.Join(root, prefix)
	if !isDir(dir) {
		return ""
	}
	official := "an official Volt library"
	if libtrust.BundledDigests(sdkfiles.FS())[prefix] != "" {
		return official
	}
	tdir, err := libtrust.Dir()
	if err != nil {
		return ""
	}
	st, pub, ok, err := libtrust.Load(tdir)
	if err != nil || !ok {
		return ""
	}
	for _, s := range libtrust.Check(st, pub, []libtrust.Library{{Name: prefix, FS: os.DirFS(dir), Root: "."}}, nil) {
		switch {
		case s.Source == "sdk-baseline":
			return official
		case strings.HasPrefix(s.Source, "registry:"):
			return "installed from the Grid"
		}
	}
	return ""
}
