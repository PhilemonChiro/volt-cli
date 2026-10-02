package build

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/PhilemonChiro/volt-cli/internal/proc"
)

// band is the percent range a stage's progress maps onto.
type band struct{ start, end int }

var (
	buildBand = band{30, 60}
	flashBand = band{65, 95}
)

// cmakeMilestones give feedback during the silent configure window before
// ninja's [N/M] lines start.
var cmakeMilestones = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?i)-- The C compiler identification`), "Detecting C compiler"},
	{regexp.MustCompile(`(?i)-- The CXX compiler identification`), "Detecting C++ compiler"},
	{regexp.MustCompile(`(?i)-- Detecting C compile features`), "Detecting compiler features"},
	{regexp.MustCompile(`(?i)-- Check for working C compiler`), "Checking C compiler"},
	{regexp.MustCompile(`(?i)-- Found Python`), "Found Python"},
	{regexp.MustCompile(`(?i)-- Found Git`), "Found Git"},
	{regexp.MustCompile(`(?i)-- Build type:`), "Setting build type"},
	{regexp.MustCompile(`(?i)-- Configuring done`), "Configuring done"},
	{regexp.MustCompile(`(?i)-- Generating done`), "Generating done"},
	{regexp.MustCompile(`(?i)-- Build files have been written`), "Build files written"},
}

var (
	ninjaStepRe = regexp.MustCompile(`\[(\d+)/(\d+)\]\s*(.*)`)
	flashPctRe  = regexp.MustCompile(`\((\d+)\s*%\)`)
)

var noisePrefixes = []string{
	"--", "PICO_", "Using board", "Build type", "Using precompiled", "PICOTOOL_NO_LIBUSB",
	"Target board", "Pico Platform", "TinyUSB", "BTstack", "cyw43", "Executing action:",
	"Running ninja", `Executing "ninja`, `Executing "cmake`, "Project build complete",
	"Configuring toolchain", "Compressed ", "Hash of data", `Checking "python`, "[1/1] cd",
}

func isNoise(line string) bool {
	for _, p := range noisePrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return strings.Contains(line, "available at /") || strings.Contains(line, "Findpicotool")
}

// tool runs one build command, turning its output into progress.
type tool struct {
	r     *Reporter
	stage string
	band  band
	log   io.Writer // full raw output
	cmake int       // milestones seen

	mu     sync.Mutex
	output strings.Builder // raw output of the current run
	errors int             // error lines reported
}

// run executes name with args in dir (env entries override the inherited
// environment). It returns the raw output alongside any failure.
func (t *tool) run(dir string, env []string, name string, args ...string) (string, error) {
	t.output.Reset()
	t.cmake = 0
	cmd := proc.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(t.log, "$ %s %s\n", filepath.Base(name), strings.Join(args, " "))
	if err := proc.Start(cmd); err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	for _, rd := range []io.Reader{stdout, stderr} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := bufio.NewScanner(rd)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			sc.Split(scanLinesCR)
			for sc.Scan() {
				t.line(sc.Text())
			}
		}()
	}
	wg.Wait()
	err = proc.Wait(cmd)
	if err != nil {
		err = fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return t.output.String(), err
}

func (t *tool) line(raw string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintln(t.log, raw)
	t.output.WriteString(raw + "\n")

	line := strings.TrimSpace(raw)
	if line == "" {
		return
	}
	if t.r.Verbose {
		t.r.Raw(line)
	} else if isNoise(line) {
		return
	}
	b := t.band

	if m := ninjaStepRe.FindStringSubmatch(line); m != nil {
		var cur, total int
		fmt.Sscan(m[1], &cur)
		fmt.Sscan(m[2], &total)
		if total > 0 {
			label := "Building"
			switch desc := m[3]; {
			case strings.Contains(desc, "ompil"):
				label = "Compiling"
			case strings.Contains(desc, "inking"):
				label = "Linking"
			case strings.Contains(desc, "enerating"):
				label = "Generating"
			}
			t.r.Progress(t.stage, fmt.Sprintf("%s (%d/%d)", label, cur, total), b.start+cur*(b.end-b.start)/total)
			return
		}
	}
	for _, ms := range cmakeMilestones {
		if ms.re.MatchString(line) {
			// Climb gently from the band start toward a quarter of the band.
			t.cmake++
			budget := max(1, (b.end-b.start)/4)
			pct := min(b.start+budget, b.start+(t.cmake*budget+len(cmakeMilestones)-1)/len(cmakeMilestones))
			t.r.Progress(t.stage, "CMake: "+ms.label, pct)
			return
		}
	}
	if t.r.Verbose {
		return // already shown raw
	}

	if t.stage == "flash" && strings.Contains(line, "Writing at") {
		if m := flashPctRe.FindStringSubmatch(line); m != nil {
			var p int
			fmt.Sscan(m[1], &p)
			t.r.Progress(t.stage, fmt.Sprintf("Flashing... %d%%", p), b.start+p*(b.end-b.start)/100)
			return
		}
	}
	switch {
	case strings.HasPrefix(line, "Connecting"):
		t.r.Step(t.stage, "Connecting to device...", b.start)
	case strings.HasPrefix(line, "Uploading stub"):
		t.r.Step(t.stage, "Uploading flash stub...", b.start+2)
	case strings.HasPrefix(line, "Configuring flash"):
		t.r.Step(t.stage, "Configuring flash...", b.start+5)
	case strings.HasPrefix(line, "Leaving"), strings.HasPrefix(line, "Hard resetting"):
		t.r.Step(t.stage, "Resetting device...", b.end-1)
	case strings.Contains(line, "binary size") && strings.Contains(line, "free"):
		t.r.Message("info", line)
	case strings.Contains(line, "error:"), strings.Contains(line, "FAILED:"), strings.Contains(line, "Error"),
		strings.HasPrefix(line, "A fatal error occurred"): // esptool

		t.errors++
		t.r.Message("error", line)
	case strings.Contains(line, "warning:") && strings.Contains(line, ".c:"):
		t.r.Message("warn", line)
	case strings.HasPrefix(line, "Successfully created"):
		t.r.Message("info", line)
	}
}

// tail returns the last n non-empty lines of out.
func tail(out string, n int) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines[max(0, len(lines)-n):]
}

// scanLinesCR is bufio.ScanLines that also ends a line at a bare \r.
func scanLinesCR(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
