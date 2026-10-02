// Package term renders the compiler's Rich-style markup ([red bold]…[/red bold])
// as ANSI colour, or strips it when the output is not a terminal.
package term

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var codes = map[string]string{
	"bold": "1", "dim": "2", "italic": "3",
	"red": "31", "green": "32", "yellow": "33", "blue": "34", "magenta": "35", "cyan": "36",
}

// Only tags made of known style words are markup; anything else (e.g. `[1, 2]`
// in a quoted source line) passes through untouched.
var tagRe = regexp.MustCompile(`\[(/?)((?:bold|dim|italic|red|green|yellow|blue|magenta|cyan)(?: (?:bold|dim|italic|red|green|yellow|blue|magenta|cyan))*)\]`)

// Writer prints markup to an underlying stream.
type Writer struct {
	w     io.Writer
	color bool
	TTY   bool // output is a terminal (in-place progress lines are safe)
}

// New returns a Writer for f, colouring only when f is a terminal and
// $NO_COLOR is unset.
func New(f *os.File) *Writer {
	st, err := f.Stat()
	tty := err == nil && st.Mode()&os.ModeCharDevice != 0
	return &Writer{f, tty && os.Getenv("NO_COLOR") == "", tty}
}

// Render converts markup in s to ANSI escapes (or plain text).
func (t *Writer) Render(s string) string {
	var stack []string
	return tagRe.ReplaceAllStringFunc(s, func(tag string) string {
		m := tagRe.FindStringSubmatch(tag)
		if m[1] == "" {
			stack = append(stack, m[2])
		} else if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
		if !t.color {
			return ""
		}
		seq := "\x1b[0m"
		for _, style := range stack {
			for _, word := range strings.Fields(style) {
				seq += "\x1b[" + codes[word] + "m"
			}
		}
		return seq
	})
}

// Printf formats, renders markup and writes.
func (t *Writer) Printf(format string, args ...any) {
	io.WriteString(t.w, t.Render(fmt.Sprintf(format, args...)))
}

// Println renders markup in s and writes it with a newline.
func (t *Writer) Println(s string) {
	io.WriteString(t.w, t.Render(s)+"\n")
}
