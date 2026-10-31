package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/PhilemonChiro/volt-cli/internal/build"
	"github.com/PhilemonChiro/volt-compiler/harness"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP serves the Volt harness over MCP on stdin/stdout, so any MCP
// client's model can read the spec and device cards and check its programs.
func runMCP(args []string) int {
	fl, devices := newFlags("mcp", "[--root dir]...")
	var roots rootList
	fl.Var(&roots, "root", "extra `dir` whose files tools may read and write (default: the working directory only)")
	if pos, ok := parse(fl, args); !ok || len(pos) > 0 {
		return 2
	}
	devDir := useDevices(*devices)
	cwd, err := os.Getwd()
	if err != nil {
		stderr.Printf("[red]volt mcp: %s[/red]\n", err)
		return 1
	}
	allowed := []string{cwd}
	// Desktop apps (Claude Desktop…) start servers in / or the home folder;
	// allowing that would open the whole disk, so use the IDE's projects folder.
	if home, _ := os.UserHomeDir(); filepath.Dir(cwd) == cwd || cwd == home {
		allowed = []string{filepath.Join(home, "Volt")}
	}
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			stderr.Printf("[red]volt mcp: %s[/red]\n", err)
			return 1
		}
		allowed = append(allowed, abs)
	}

	s := newMCPServer(allowed, devDir)
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		stderr.Printf("[red]volt mcp: %s[/red]\n", err)
		return 1
	}
	return 0
}

type rootList []string

func (r *rootList) String() string     { return strings.Join(*r, ",") }
func (r *rootList) Set(v string) error { *r = append(*r, v); return nil }

// programIn names the program a tool works on: a file or directory in the
// project, or source text.
type programIn struct {
	Path   string `json:"path,omitempty" jsonschema:"a .volt file or project directory; volt.toml beside the file sets the chip"`
	Source string `json:"source,omitempty" jsonschema:"program text, when there is no file"`
	Chip   string `json:"chip,omitempty" jsonschema:"chip for source text (esp32, esp8266, rp2040); default: the working directory's volt.toml, else esp32"`
}

type specIn struct {
	Section string `json:"section,omitempty" jsonschema:"one section's title, e.g. Grammar or State machines; empty for the whole reference without devices"`
}

type specOut struct {
	Spec string `json:"spec"`
}

type devicesIn struct {
	Query string `json:"query,omitempty" jsonschema:"words to match against name, category, interface and description, e.g. 'temperature i2c'; empty lists every device"`
}

type devicesOut struct {
	Devices []harness.Device `json:"devices"`
}

type cardsIn struct {
	Names []string `json:"names" jsonschema:"device names as list_devices gives them, e.g. ['BME280','SSD1306']"`
}

type cardsOut struct {
	Cards   []string            `json:"cards"`
	Unknown map[string][]string `json:"unknown,omitempty"`
	Next    string              `json:"next,omitempty"`
}

// pinModes are pin types, not devices: they need no driver.
var pinModes = map[string]bool{"output": true, "input": true, "pullupinput": true, "pulldowninput": true,
	"analog": true, "pwm": true, "servo": true, "touch": true, "dac": true}

// missingDriverNext says what to do when a device has no Volt driver.
func missingDriverNext(unknown map[string][]string, devDir string) string {
	var modes, devices []string
	for name := range unknown {
		if pinModes[strings.ToLower(name)] {
			modes = append(modes, name)
		} else {
			devices = append(devices, name)
		}
	}
	sort.Strings(modes)
	sort.Strings(devices)
	var parts []string
	if len(modes) > 0 {
		parts = append(parts, strings.Join(modes, ", ")+" is a pin type, not a device: declare it as `pin name: "+modes[0]+" = <pin>` (see the pins section of volt_spec); no driver is needed.")
	}
	if len(devices) > 0 {
		parts = append(parts, "No Volt driver for "+strings.Join(devices, ", ")+". If it is a plain analog or digital part (e.g. an MQ-2 gas sensor's AO output, a PIR, a button, a relay), use a pin type such as `pin gas: Analog = 34` and the threshold logic in Volt — no driver needed (an MQ gas sensor reads high for 1-3 min after power-up while its heater warms: ignore it, or don't alarm, until then). If it needs a protocol (I2C/SPI/UART registers, timing), offer to write a driver: read device.json, header.h and driver.c of a similar existing device in "+devDir+" (e.g. dht22 for single-wire timing, bme280 for I2C), create it with library_write (the user reviews and approves it in Volt IDE), then use it. Don't stop at telling the user there is no driver.")
	}
	return strings.Join(parts, " ")
}

type formatIn struct {
	programIn
	Write bool `json:"write,omitempty" jsonschema:"with path: write the formatted program back to the file"`
}

type formatOut struct {
	Source  string `json:"source"`
	Written string `json:"written,omitempty"`
}

func newMCPServer(allowed []string, devDir string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "volt", Title: "Volt firmware language", Version: version},
		&mcp.ServerOptions{Instructions: harness.Instructions + ideInstructions, InitializedHandler: helloIDE})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(s, &mcp.Tool{Name: "volt_spec", Annotations: ro,
		Description: "The Volt language reference (Markdown, ~5k tokens): syntax, rules that are easy to get wrong, pin modes, built-in objects and chips. Read it once before writing Volt; Volt is new, so don't write it from memory. Devices are not in it: use device_card. Pass section to get one section (e.g. \"Grammar\") again."},
		func(_ context.Context, _ *mcp.CallToolRequest, in specIn) (*mcp.CallToolResult, specOut, error) {
			return nil, specOut{harness.SpecSection(in.Section)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_devices", Annotations: ro,
		Description: "Find Volt devices (sensors, displays, actuators, modules) by name or purpose. Then call device_card for the ones you use."},
		func(_ context.Context, _ *mcp.CallToolRequest, in devicesIn) (*mcp.CallToolResult, devicesOut, error) {
			return nil, devicesOut{harness.Devices(in.Query)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "device_card", Annotations: ro,
		Description: "Reference cards for devices: how to declare them (wiring, constructor arguments, I2C address) and their exact methods. Call it for every device a program uses and use only what the card lists."},
		func(_ context.Context, _ *mcp.CallToolRequest, in cardsIn) (*mcp.CallToolResult, cardsOut, error) {
			cards, unknown := harness.Cards(in.Names)
			if cards == nil {
				cards = []string{}
			}
			if len(unknown) == 0 {
				unknown = nil
			}
			out := cardsOut{Cards: cards, Unknown: unknown}
			if unknown != nil {
				out.Next = missingDriverNext(unknown, devDir)
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "check", Annotations: ro,
		Description: "Compile a Volt program and report errors and warnings, each with a line and usually a hint. Call it after every edit and fix everything it reports until ok is true; never present a program that has not passed. `next` says what to do next."},
		func(_ context.Context, _ *mcp.CallToolRequest, in programIn) (*mcp.CallToolResult, *harness.CheckResult, error) {
			syncDevices(devDir)
			src, file, cleanup, err := loadProgram(in, allowed)
			if err != nil {
				return nil, nil, err
			}
			defer cleanup()
			res, err := harness.Check(src, file)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "review", Annotations: ro,
		Description: "Facts about a compiling Volt program — pins, devices, every periodic block and its interval, button events, storage variables and whether they are saved, state machines with transitions and unreachable states, channels, network calls — plus what to compare them against. Call it after check passes and compare the facts with the user's request."},
		func(_ context.Context, _ *mcp.CallToolRequest, in programIn) (*mcp.CallToolResult, *harness.ReviewResult, error) {
			syncDevices(devDir)
			src, file, cleanup, err := loadProgram(in, allowed)
			if err != nil {
				return nil, nil, err
			}
			defer cleanup()
			res, err := harness.Review(src, file)
			return nil, res, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "format", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: new(bool)},
		Description: "Format a Volt program canonically. With path and write: true the file is rewritten in place."},
		func(_ context.Context, _ *mcp.CallToolRequest, in formatIn) (*mcp.CallToolResult, formatOut, error) {
			src, file, cleanup, err := loadProgram(in.programIn, allowed)
			if err != nil {
				return nil, formatOut{}, err
			}
			defer cleanup()
			out, err := harness.Format(src)
			if err != nil {
				return nil, formatOut{}, fmt.Errorf("cannot format until the program parses (call check): %w", err)
			}
			res := formatOut{Source: out}
			if in.Write {
				if in.Path == "" {
					return nil, formatOut{}, errors.New("write needs path")
				}
				if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
					return nil, formatOut{}, err
				}
				res.Written = file
			}
			return nil, res, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "build", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: new(bool)},
		Description: "Build firmware for a Volt project directory (its volt.toml sets the chip) into <project>/build/<chip>/. Run after check passes. The first build of a chip can take several minutes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in buildIn) (*mcp.CallToolResult, buildOut, error) {
			return runBuildTool(ctx, in, allowed, syncDevices(devDir))
		})

	addIDETools(s, allowed)

	s.AddResource(&mcp.Resource{URI: "volt://spec", Name: "spec", Title: "Volt language reference", MIMEType: "text/markdown"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: harness.Spec()}}}, nil
		})
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "volt://devices/{name}", Name: "device", Title: "Volt device card", MIMEType: "text/markdown"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			name := strings.TrimPrefix(req.Params.URI, "volt://devices/")
			cards, _ := harness.Cards([]string{name})
			if len(cards) == 0 {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: cards[0]}}}, nil
		})

	s.AddPrompt(&mcp.Prompt{Name: "write_volt_program", Title: "Write a Volt program",
		Description: "Write firmware in Volt for a request, following the spec → device cards → check-until-clean workflow.",
		Arguments:   []*mcp.PromptArgument{{Name: "request", Description: "what the firmware should do", Required: true}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user",
				Content: &mcp.TextContent{Text: harness.WorkflowPrompt(req.Params.Arguments["request"])}}}}, nil
		})
	return s
}

// loadProgram returns a tool's program text and the file name to compile it
// as. Paths must lie inside an allowed directory. Source text with a chip is
// compiled in a temporary project carrying that chip's volt.toml.
func loadProgram(in programIn, allowed []string) (src, file string, cleanup func(), err error) {
	cleanup = func() {}
	switch {
	case in.Path != "" && in.Source != "":
		return "", "", cleanup, errors.New("give path or source, not both")
	case in.Path != "":
		abs, err := filepath.Abs(in.Path)
		if err != nil {
			return "", "", cleanup, err
		}
		if !within(abs, allowed) {
			return "", "", cleanup, fmt.Errorf("%s is outside the allowed directories (%s); start volt mcp with --root to allow more", abs, strings.Join(allowed, ", "))
		}
		if file, err = findSource(abs); err != nil {
			return "", "", cleanup, err
		}
		if err := requireOpenProject(file); err != nil {
			return "", "", cleanup, err
		}
		b, err := os.ReadFile(file)
		return string(b), file, cleanup, err
	case in.Source != "":
		if in.Chip == "" {
			return in.Source, "main.volt", cleanup, nil
		}
		dir, err := os.MkdirTemp("", "volt-mcp-")
		if err != nil {
			return "", "", cleanup, err
		}
		cleanup = func() { os.RemoveAll(dir) }
		if err := os.WriteFile(filepath.Join(dir, "volt.toml"), fmt.Appendf(nil, "chip = %q\n", in.Chip), 0o644); err != nil {
			cleanup()
			return "", "", func() {}, err
		}
		return in.Source, filepath.Join(dir, "main.volt"), cleanup, nil
	}
	return "", "", cleanup, errors.New("give path or source")
}

// within reports whether path lies inside one of dirs. macOS and Windows
// file systems ignore letter case (~/dev is ~/Dev), so the compare does too.
func within(path string, dirs []string) bool {
	fold := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	if fold {
		path = strings.ToLower(path)
	}
	for _, d := range dirs {
		if fold {
			d = strings.ToLower(d)
		}
		if rel, err := filepath.Rel(d, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

type buildIn struct {
	Path string `json:"path" jsonschema:"the project directory (or a .volt file in it)"`
}

type buildOut struct {
	OK       bool        `json:"ok"`
	Artifact string      `json:"artifact,omitempty"`
	Size     *build.Size `json:"size,omitempty"`
	Errors   []string    `json:"errors,omitempty"`
	Warnings []string    `json:"warnings,omitempty"`
}

// runBuildTool runs `volt build --json` on the project and condenses its
// event stream.
func runBuildTool(ctx context.Context, in buildIn, allowed []string, devDir string) (*mcp.CallToolResult, buildOut, error) {
	abs, err := filepath.Abs(in.Path)
	if err != nil {
		return nil, buildOut{}, err
	}
	if !within(abs, allowed) {
		return nil, buildOut{}, fmt.Errorf("%s is outside the allowed directories (%s)", abs, strings.Join(allowed, ", "))
	}
	if !isDir(abs) {
		abs = filepath.Dir(abs)
	}
	if err := requireOpenProject(abs); err != nil {
		return nil, buildOut{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, buildOut{}, err
	}
	args := []string{"build", abs, "--json"}
	if devDir != "" {
		args = append(args, "--devices", devDir)
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	stdout, _ := cmd.Output() // a failed build exits non-zero; the events say why
	var out buildOut
	for _, line := range strings.Split(string(stdout), "\n") {
		var ev build.Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Stage {
		case "error":
			out.Errors = append(out.Errors, ev.Message)
		case "warn":
			out.Warnings = append(out.Warnings, ev.Message)
		case "done":
			out.OK, out.Artifact, out.Size = true, ev.Artifact, ev.Size
		}
	}
	if !out.OK && len(out.Errors) == 0 {
		out.Errors = []string{"build failed without a report; run `volt build` in the project to see why"}
	}
	return nil, out, nil
}
