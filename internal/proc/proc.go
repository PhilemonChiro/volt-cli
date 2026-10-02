// Package proc starts external tools so that cancelling volt (Ctrl-C, or the
// IDE killing it with SIGTERM) also stops everything they spawned: idf.py,
// install.sh and git leave grandchildren (ninja, gcc, pip) that would
// otherwise keep running and holding the build dir or serial port.
package proc

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	// Root is cancelled on SIGINT/SIGTERM. Long-running work that is not an
	// external process (the serial monitor) watches it to shut down cleanly.
	Root, cancel = context.WithCancel(context.Background())

	mu      sync.Mutex
	running = map[*exec.Cmd]bool{}
)

// HandleSignals makes SIGINT/SIGTERM cancel Root and terminate every running
// command's process group, then exits 130 if main has not returned by then.
func HandleSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
		mu.Lock()
		for cmd := range running {
			terminate(cmd)
		}
		mu.Unlock()
		time.Sleep(3 * time.Second)
		os.Exit(130)
	}()
}

// Command is exec.Command for a tool that may spawn its own children.
// Start and Wait it with Start/Wait below so cancellation can reach it.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	setGroup(cmd)
	return cmd
}

// Start starts cmd and tracks it until Wait.
func Start(cmd *exec.Cmd) error {
	mu.Lock()
	defer mu.Unlock()
	if Root.Err() != nil {
		return Root.Err()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	running[cmd] = true
	return nil
}

// Wait waits for cmd and stops tracking it. After cancellation it reports
// context.Canceled rather than the tool's own failure.
func Wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	mu.Lock()
	delete(running, cmd)
	mu.Unlock()
	if err != nil && Root.Err() != nil {
		return Root.Err()
	}
	return err
}
