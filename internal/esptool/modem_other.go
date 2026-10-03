//go:build !(darwin || linux)

package esptool

import "go.bug.st/serial"

// setDTRRTS: no atomic modem-line update here; connect uses ClassicReset.
func setDTRRTS(serial.Port, bool, bool) (bool, error) { return false, nil }
