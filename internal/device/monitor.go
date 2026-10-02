package device

import (
	"bufio"
	"context"
	"errors"
	"strings"

	"go.bug.st/serial"
)

// ErrLost reports that the port went away while monitoring (board unplugged
// or reset into a different USB identity).
var ErrLost = errors.New("connection lost")

// Monitor reads lines from port until ctx is cancelled or the port goes
// away, calling opened once the port is open and line for each line. DTR and
// RTS stay de-asserted: asserting them holds many ESP32 boards in reset
// through the auto-reset circuit.
func Monitor(ctx context.Context, port string, baud int, opened func(), line func(string)) error {
	p, err := serial.Open(port, &serial.Mode{
		BaudRate:          baud,
		InitialStatusBits: &serial.ModemOutputBits{RTS: false, DTR: false},
	})
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { p.Close() })
	defer stop()
	defer p.Close()
	opened()

	sc := bufio.NewScanner(p)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		line(strings.TrimRight(sc.Text(), "\r"))
	}
	if ctx.Err() != nil {
		return nil
	}
	return ErrLost
}
