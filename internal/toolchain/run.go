package toolchain

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PhilemonChiro/volt-cli/internal/proc"
)

// run executes name with args in dir, streaming stdout and stderr to e line
// by line (splitting on \r too, so git/pip progress lines arrive live). env
// entries override the inherited environment.
func run(e *Emitter, dir string, env []string, name string, args ...string) error {
	cmd := proc.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(cmd.Environ(), env...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	e.Log("$ " + filepath.Base(name) + " " + strings.Join(args, " "))
	if err := proc.Start(cmd); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, r := range []io.Reader{stdout, stderr} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			sc.Split(scanLinesCR)
			for sc.Scan() {
				e.Log(sc.Text())
			}
		}()
	}
	wg.Wait()
	if err := proc.Wait(cmd); err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return nil
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
