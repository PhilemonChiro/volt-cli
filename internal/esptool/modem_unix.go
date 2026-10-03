//go:build darwin || linux

package esptool

import (
	"reflect"

	"go.bug.st/serial"
	"golang.org/x/sys/unix"
)

// setDTRRTS sets both modem lines in one TIOCMSET, like esptool's
// UnixTightReset. Two separate calls pass through DTR=RTS=1, which on the
// usual two-transistor auto-reset circuit releases EN with IO0 high, so a
// slow USB-serial driver (macOS CH340/CP210x) lets the chip boot the app.
// ok is false when the port's descriptor can't be reached.
func setDTRRTS(p serial.Port, dtr, rts bool) (ok bool, err error) {
	fd, ok := portFD(p)
	if !ok {
		return false, nil
	}
	status, err := unix.IoctlGetInt(fd, unix.TIOCMGET)
	if err != nil {
		return true, err
	}
	status &^= unix.TIOCM_DTR | unix.TIOCM_RTS
	if dtr {
		status |= unix.TIOCM_DTR
	}
	if rts {
		status |= unix.TIOCM_RTS
	}
	return true, unix.IoctlSetPointerInt(fd, unix.TIOCMSET, status)
}

// portFD reads go.bug.st/serial's unexported unixPort.handle (v1.8.0).
func portFD(p serial.Port) (int, bool) {
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return 0, false
	}
	h := v.Elem().FieldByName("handle")
	if !h.IsValid() || h.Kind() != reflect.Int {
		return 0, false
	}
	return int(h.Int()), true
}
