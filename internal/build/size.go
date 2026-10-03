package build

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/PhilemonChiro/volt-cli/internal/sdk"
)

// ESP32Size measures a built ESP32-family app: the image against the pack's
// first app partition, and static DRAM from the ELF next to it. Parts it
// can't read are left zero; nil if the image itself is missing.
func ESP32Size(app, chip string) *Size {
	st, err := os.Stat(app)
	if err != nil {
		return nil
	}
	s := &Size{Flash: st.Size()}
	if f, err := elf.Open(strings.TrimSuffix(app, ".bin") + ".elf"); err == nil {
		for _, sec := range f.Sections {
			if strings.HasPrefix(sec.Name, ".dram0.") {
				s.RAM += int64(sec.Size)
			}
		}
		f.Close()
	}
	if pack, err := sdk.Find(chip); err == nil {
		if spec, err := sdk.LoadSpec(pack); err == nil {
			for _, part := range spec.Flash {
				if strings.Contains(part.File, "partition") {
					s.FlashMax = firstAppPartition(strings.ReplaceAll(part.File, "{pack}", pack))
				}
			}
		}
	}
	return s
}

// firstAppPartition returns the size of the first app (type 0) entry in an
// ESP-IDF binary partition table: 32-byte entries starting with magic 0xAA50.
func firstAppPartition(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for i := 0; i+32 <= len(b); i += 32 {
		if binary.LittleEndian.Uint16(b[i:]) != 0x50AA {
			break
		}
		if b[i+2] == 0x00 {
			return int64(binary.LittleEndian.Uint32(b[i+8:]))
		}
	}
	return 0
}

func (s *Size) String() string {
	out := "Flash " + kb(s.Flash)
	if s.FlashMax > 0 {
		out += fmt.Sprintf(" of %s (%d%%)", kb(s.FlashMax), s.Flash*100/s.FlashMax)
	}
	if s.RAM > 0 {
		out += " · RAM " + kb(s.RAM)
	}
	return out
}

func kb(n int64) string { return fmt.Sprintf("%d KB", (n+512)/1024) }
