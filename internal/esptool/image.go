// Package esptool does in Go what Volt used Espressif's esptool (Python) for:
// making an app image from an ELF (elf2image) and writing images to a board.
// Output matches esptool 4.12 byte for byte.
package esptool

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Chip holds what image making needs to know about a chip.
type Chip struct {
	Name        string
	ImageChipID uint16
	IROM, DROM  [2]uint32 // flash-mapped [start, end)
	IROMAlign   int       // MMU page size
	MemoryMap   []memRange
	FlashSizes  map[string]byte
	FlashFreqs  map[string]byte
}

type memRange struct {
	start, end uint32
	name       string
}

var chips = map[string]*Chip{
	"esp32": {
		Name: "esp32", ImageChipID: 0,
		IROM: [2]uint32{0x400D0000, 0x40400000}, DROM: [2]uint32{0x3F400000, 0x3F800000},
		IROMAlign: 0x10000,
		MemoryMap: []memRange{
			{0x00000000, 0x00010000, "PADDING"},
			{0x3F400000, 0x3F800000, "DROM"},
			{0x3F800000, 0x3FC00000, "EXTRAM_DATA"},
			{0x3FF80000, 0x3FF82000, "RTC_DRAM"},
			{0x3FF90000, 0x40000000, "BYTE_ACCESSIBLE"},
			{0x3FFAE000, 0x40000000, "DRAM"},
			{0x3FFE0000, 0x3FFFFFFC, "DIRAM_DRAM"},
			{0x40000000, 0x40070000, "IROM"},
			{0x40070000, 0x40078000, "CACHE_PRO"},
			{0x40078000, 0x40080000, "CACHE_APP"},
			{0x40080000, 0x400A0000, "IRAM"},
			{0x400A0000, 0x400BFFFC, "DIRAM_IRAM"},
			{0x400C0000, 0x400C2000, "RTC_IRAM"},
			{0x400D0000, 0x40400000, "IROM"},
			{0x50000000, 0x50002000, "RTC_DATA"},
		},
		FlashSizes: map[string]byte{"1MB": 0x00, "2MB": 0x10, "4MB": 0x20, "8MB": 0x30,
			"16MB": 0x40, "32MB": 0x50, "64MB": 0x60, "128MB": 0x70},
		FlashFreqs: map[string]byte{"80m": 0xF, "40m": 0x0, "26m": 0x1, "20m": 0x2},
	},
}

// ChipByName returns a supported chip.
func ChipByName(name string) (*Chip, error) {
	if c, ok := chips[strings.ToLower(name)]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("esptool: unsupported chip %q", name)
}

var flashModes = map[string]byte{"qio": 0, "qout": 1, "dio": 2, "dout": 3}

// ImageOptions are elf2image's settings.
type ImageOptions struct {
	Chip                            *Chip
	FlashMode, FlashFreq, FlashSize string
	ELFSHA256Offset                 int // 0: none
	MinRev                          uint8
	MinRevFull, MaxRevFull          uint16
}

// ParseElf2ImageArgs reads esptool's "--chip c elf2image …" arguments, as a
// pack's build.json records them. Any other argument is an error, so a pack
// asking for something this port does not do fails instead of differing.
func ParseElf2ImageArgs(args []string) (ImageOptions, error) {
	o := ImageOptions{FlashMode: "qio", FlashFreq: "40m", FlashSize: "1MB"}
	chip := ""
	seen := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "elf2image" {
			seen = true
			continue
		}
		if i+1 >= len(args) {
			return o, fmt.Errorf("elf2image: %s needs a value", a)
		}
		v := args[i+1]
		i++
		var err error
		switch a {
		case "--chip", "-c":
			chip = v
		case "--flash_mode", "--flash-mode", "-fm":
			o.FlashMode = v
		case "--flash_freq", "--flash-freq", "-ff":
			o.FlashFreq = v
		case "--flash_size", "--flash-size", "-fs":
			o.FlashSize = v
		case "--elf-sha256-offset", "--elf_sha256_offset":
			o.ELFSHA256Offset, err = parseInt(v)
		case "--min-rev", "--min_rev", "-r":
			var n int
			n, err = parseInt(v)
			o.MinRev = uint8(n)
		case "--min-rev-full", "--min_rev_full":
			var n int
			n, err = parseInt(v)
			o.MinRevFull = uint16(n)
		case "--max-rev-full", "--max_rev_full":
			var n int
			n, err = parseInt(v)
			o.MaxRevFull = uint16(n)
		default:
			return o, fmt.Errorf("elf2image: unsupported argument %s", a)
		}
		if err != nil {
			return o, fmt.Errorf("elf2image: %s %s: %w", a, v, err)
		}
	}
	if !seen {
		return o, fmt.Errorf("elf2image: not an elf2image command")
	}
	c, err := ChipByName(chip)
	if err != nil {
		return o, err
	}
	o.Chip = c
	return o, nil
}

func parseInt(s string) (int, error) {
	n, err := strconv.ParseInt(s, 0, 64)
	return int(n), err
}

// segment is an image segment; ELF sections keep their name.
type segment struct {
	name string
	addr uint32
	data []byte
}

func newSegment(name string, addr uint32, data []byte) *segment {
	s := &segment{name, addr, data}
	if addr != 0 { // "real" segments are 4-byte aligned in length
		s.data = padTo(s.data, 4, 0)
	}
	return s
}

func padTo(b []byte, align int, c byte) []byte {
	if m := len(b) % align; m != 0 {
		b = append(b, bytes.Repeat([]byte{c}, align-m)...)
	}
	return b
}

const (
	segHeaderLen = 8
	checksumSeed = 0xEF
	imageMagic   = 0xE9
)

// Elf2Image makes an app image from an ELF, as esptool elf2image does.
func Elf2Image(elfData []byte, o ImageOptions) ([]byte, error) {
	c := o.Chip
	f, err := elf.NewFile(bytes.NewReader(elfData))
	if err != nil {
		return nil, err
	}
	if f.Machine != elf.EM_XTENSA && f.Machine != elf.EM_RISCV {
		return nil, fmt.Errorf("not an Xtensa or RISC-V ELF (machine %v)", f.Machine)
	}
	mode, ok := flashModes[o.FlashMode]
	if !ok {
		return nil, fmt.Errorf("unknown flash mode %q", o.FlashMode)
	}
	size, ok := c.FlashSizes[o.FlashSize]
	if !ok {
		return nil, fmt.Errorf("unknown flash size %q for %s", o.FlashSize, c.Name)
	}
	freq, ok := c.FlashFreqs[o.FlashFreq]
	if !ok {
		return nil, fmt.Errorf("unknown flash frequency %q for %s", o.FlashFreq, c.Name)
	}

	// Sections, in header order: loadable data at a non-zero address.
	var segs []*segment
	for _, s := range f.Sections {
		if s.Addr == 0 || s.Size == 0 {
			continue
		}
		switch s.Type {
		case elf.SHT_PROGBITS, elf.SHT_INIT_ARRAY, elf.SHT_FINI_ARRAY, elf.SHT_PREINIT_ARRAY:
			d, err := s.Data()
			if err != nil {
				return nil, fmt.Errorf("section %s: %w", s.Name, err)
			}
			segs = append(segs, newSegment(s.Name, uint32(s.Addr), d))
		}
	}
	segs = mergeAdjacent(c, segs)
	if len(segs) > 16 {
		return nil, fmt.Errorf("invalid segment count %d (max 16): linker script problem?", len(segs))
	}
	var elfSHA []byte
	if o.ELFSHA256Offset != 0 {
		sum := sha256.Sum256(elfData)
		elfSHA = sum[:]
	}

	w := &imageWriter{chip: c, elfSHA: elfSHA, shaOffset: o.ELFSHA256Offset}
	// Common header; the segment count is patched in at the end.
	w.buf.Write([]byte{imageMagic, byte(len(segs)), mode, size + freq})
	binary.Write(&w.buf, binary.LittleEndian, uint32(f.Entry))
	// Extended header.
	w.buf.WriteByte(0xEE) // WP pin disabled
	w.buf.Write([]byte{0, 0, 0})
	binary.Write(&w.buf, binary.LittleEndian, c.ImageChipID)
	w.buf.WriteByte(o.MinRev)
	binary.Write(&w.buf, binary.LittleEndian, o.MinRevFull)
	binary.Write(&w.buf, binary.LittleEndian, o.MaxRevFull)
	w.buf.Write([]byte{0, 0, 0, 0})
	w.buf.WriteByte(1) // SHA-256 digest appended

	isFlash := func(a uint32) bool {
		return (c.IROM[0] <= a && a < c.IROM[1]) || (c.DROM[0] <= a && a < c.DROM[1])
	}
	sorted := slices.Clone(segs)
	slices.SortStableFunc(sorted, func(a, b *segment) int { return cmpU32(a.addr, b.addr) })
	var flash, ram []*segment
	for _, s := range sorted {
		cp := &segment{s.name, s.addr, slices.Clone(s.data)}
		if isFlash(s.addr) {
			flash = append(flash, cp)
		} else {
			ram = append(ram, cp)
		}
	}
	moveFirst(flash, ".flash.appdesc")
	moveFirst(ram, ".dram0.bootdesc")
	align := uint32(c.IROMAlign)
	for i := 1; i < len(flash); i++ {
		if flash[i].addr/align == flash[i-1].addr/align {
			return nil, fmt.Errorf("segment at %#010x lands in the same %d KB flash mapping as segment at %#010x",
				flash[i].addr, align/1024, flash[i-1].addr)
		}
	}

	checksum := byte(checksumSeed)
	total := 0
	for len(flash) > 0 {
		s := flash[0]
		pad := w.alignmentNeeded(s)
		if pad > 0 {
			var p *segment
			if len(ram) > 0 && pad > segHeaderLen {
				r := ram[0]
				n := min(pad, len(r.data))
				p = &segment{r.name, r.addr, r.data[:n:n]}
				r.data = r.data[n:]
				r.addr += uint32(n)
				if len(r.data) == 0 {
					ram = ram[1:]
				}
			} else {
				p = &segment{"", 0, make([]byte, pad)}
			}
			if checksum, err = w.save(p, checksum); err != nil {
				return nil, err
			}
		} else {
			if (w.buf.Len()+segHeaderLen)%c.IROMAlign != int(s.addr%align) {
				return nil, fmt.Errorf("internal error: %s not aligned", s.name)
			}
			if c.Name == "esp32" {
				// ESP-IDF's old 2nd stage bootloader did not map the last MMU
				// page if an IROM/DROM segment was < 0x24 bytes over it.
				end := w.buf.Len() + len(s.data) + segHeaderLen
				if r := end % c.IROMAlign; r < 0x24 {
					s.data = append(s.data, make([]byte, 0x24-r)...)
				}
			}
			if checksum, err = w.save(s, checksum); err != nil {
				return nil, err
			}
			flash = flash[1:]
		}
		total++
	}
	for _, s := range ram {
		if checksum, err = w.save(s, checksum); err != nil {
			return nil, err
		}
		total++
	}
	// Checksum in the last byte of a 16-byte block.
	w.buf.Write(make([]byte, 15-w.buf.Len()%16))
	w.buf.WriteByte(checksum)
	img := w.buf.Bytes()
	img[1] = byte(total)
	sum := sha256.Sum256(img)
	return append(img, sum[:]...), nil
}

func cmpU32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func moveFirst(segs []*segment, name string) {
	for i, s := range segs {
		if s.name == name {
			copy(segs[1:i+1], segs[:i])
			segs[0] = s
			return
		}
	}
}

// mergeAdjacent joins sections that end where the next starts, in the same
// memory type(s), walking backwards like esptool.
func mergeAdjacent(c *Chip, segs []*segment) []*segment {
	if len(segs) == 0 {
		return segs
	}
	memType := func(a uint32) string {
		var t []string
		for _, r := range c.MemoryMap {
			if r.start <= a && a < r.end {
				t = append(t, r.name)
			}
		}
		return strings.Join(t, ",")
	}
	var out []*segment
	for i := len(segs) - 1; i > 0; i-- {
		prev, next := segs[i-1], segs[i]
		if memType(prev.addr) == memType(next.addr) && next.addr == prev.addr+uint32(len(prev.data)) {
			prev.data = append(prev.data, next.data...)
		} else {
			out = append([]*segment{next}, out...)
		}
	}
	return append([]*segment{segs[0]}, out...)
}

type imageWriter struct {
	chip      *Chip
	buf       bytes.Buffer
	elfSHA    []byte
	shaOffset int
}

func (w *imageWriter) alignmentNeeded(s *segment) int {
	a := w.chip.IROMAlign
	alignPast := int(s.addr%uint32(a)) - segHeaderLen
	pad := (a - w.buf.Len()%a) + alignPast
	if pad == 0 || pad == a {
		return 0
	}
	pad -= segHeaderLen
	if pad < 0 {
		pad += a
	}
	return pad
}

// save writes a segment (patching in the ELF's SHA-256 when it covers the
// offset) and returns the running checksum.
func (w *imageWriter) save(s *segment, checksum byte) (byte, error) {
	data := s.data
	pos := w.buf.Len()
	if w.elfSHA != nil && w.shaOffset >= pos && w.shaOffset < pos+len(data) {
		off := w.shaOffset - pos
		if off < segHeaderLen || off+32 > len(data) {
			return 0, fmt.Errorf("cannot place the ELF SHA-256 on a segment boundary (offset %#x)", w.shaOffset)
		}
		off -= segHeaderLen
		if !bytes.Equal(data[off:off+32], make([]byte, 32)) {
			return 0, fmt.Errorf("segment bytes at ELF SHA-256 offset %#x are not zero", w.shaOffset)
		}
		data = slices.Clone(data)
		copy(data[off:], w.elfSHA)
	}
	if len(data)&3 != 0 {
		return 0, fmt.Errorf("invalid %s segment length %#x: not a multiple of 4", s.name, len(data))
	}
	if len(data) >= 0x1000000 {
		return 0, fmt.Errorf("invalid %s segment length %#x: over 16 MB", s.name, len(data))
	}
	binary.Write(&w.buf, binary.LittleEndian, [2]uint32{s.addr, uint32(len(data))})
	w.buf.Write(data)
	for _, b := range data {
		checksum ^= b
	}
	return checksum, nil
}
