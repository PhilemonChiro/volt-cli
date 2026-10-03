package esptool

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"regexp"
	"time"

	"go.bug.st/serial"
)

// The flasher stub: Espressif's esp-flasher-stub v0.7.0 (Apache-2.0 OR MIT,
// see stub/README.md), as shipped in esptool 4.12's stub_flasher/2.
//
//go:embed stub/*.json
var stubs embed.FS

// ErrConnect is returned when the chip does not answer in download mode.
var ErrConnect = errors.New("failed to connect")

// FlashPart is one image written at an offset.
type FlashPart struct {
	Offset uint32
	Data   []byte
	Name   string // for messages
}

// FlashOptions are write_flash's settings.
type FlashOptions struct {
	Chip                            *Chip
	Port                            string
	Baud                            int // after the stub runs (ROM talks 115200)
	FlashMode, FlashFreq, FlashSize string
	Parts                           []FlashPart
	// Progress reports done/total bytes written, over all parts.
	Progress func(msg string, done, total int)
	// Full writes every part even when flash already holds it.
	Full bool
	// Skipped, if set, is told about each part left alone because flash
	// already held it.
	Skipped func(FlashPart)
	Log     io.Writer // optional protocol log
}

const (
	romBaud          = 115200
	ramBlock         = 0x1800
	stubWriteSize    = 0x4000
	defaultTimeout   = 3 * time.Second
	syncTimeout      = 100 * time.Millisecond
	memEndROMTimeout = 200 * time.Millisecond
	connectAttempts  = 7
	writeAttempts    = 3
	sessionAttempts  = 3

	opMemBegin      = 0x05
	opMemEnd        = 0x06
	opMemData       = 0x07
	opSync          = 0x08
	opReadReg       = 0x0A
	opSPISetParams  = 0x0B
	opChangeBaud    = 0x0F
	opFlashDeflBeg  = 0x10
	opFlashDeflData = 0x11
	opFlashDeflEnd  = 0x12
	opSPIFlashMD5   = 0x13
)

// esp32 registers (esptool targets/esp32.py).
const (
	chipMagicReg   = 0x40001000
	esp32Magic     = 0x00F01D83
	efuseBase      = 0x3FF5A000
	apbCtlDateAddr = 0x3FF66000 + 0x7C
	bootloaderAddr = 0x1000
)

// WriteFlash writes parts to the chip on o.Port as esptool write_flash does
// with the default options (default_reset, stub, compressed, verified, then
// hard_reset).
func WriteFlash(ctx context.Context, o FlashOptions) error {
	if o.Chip == nil || o.Chip.Name != "esp32" {
		return fmt.Errorf("esptool: flashing is only implemented for esp32")
	}
	size, ok := flashSizeBytes[o.FlashSize]
	if !ok {
		return fmt.Errorf("unknown flash size %q", o.FlashSize)
	}
	for _, p := range o.Parts {
		if int(p.Offset)+len(p.Data) > size {
			return fmt.Errorf("%s (%d bytes at %#x) does not fit in %s of flash", p.Name, len(p.Data), p.Offset, o.FlashSize)
		}
	}
	port, err := serial.Open(o.Port, &serial.Mode{BaudRate: romBaud})
	if err != nil {
		return fmt.Errorf("opening %s: %w", o.Port, err)
	}
	defer port.Close()
	l := &loader{ctx: ctx, port: port, log: o.Log, baud: romBaud}

	// A write that fails after retries restarts the session from a reset at
	// the next lower baud rate. Parts already written are skipped by their
	// MD5, so a retry only redoes what is missing.
	bauds := fallbackBauds(o.Baud)
	for i, baud := range bauds {
		err = l.session(o, baud, size)
		if err == nil {
			o.progress("Resetting device...", 1, 1)
			return l.hardReset()
		}
		var f fatalError
		if errors.As(err, &f) || errors.Is(err, ErrConnect) || ctx.Err() != nil || i == len(bauds)-1 {
			return err
		}
		l.logf("flashing failed at %d baud (%v); retrying at %d baud", baud, err, bauds[i+1])
		o.progress(fmt.Sprintf("Write failed, retrying at %d baud...", bauds[i+1]), 0, 1)
		if err := l.toROMBaud(); err != nil {
			return err
		}
	}
	return err
}

// fatalError marks a refusal that no retry can fix.
type fatalError struct{ error }

func (f fatalError) Unwrap() error { return f.error }

// fallbackBauds is baud, then the standard rates below it down to the ROM's.
func fallbackBauds(baud int) []int {
	out := []int{max(baud, romBaud)}
	for _, b := range []int{460800, 230400, romBaud} {
		if b < out[len(out)-1] {
			out = append(out, b)
		}
	}
	return out
}

// toROMBaud returns the host side to the ROM loader's rate for a new reset.
func (l *loader) toROMBaud() error {
	l.stub = false
	if l.baud == romBaud {
		return nil
	}
	l.baud = romBaud
	return l.port.SetMode(&serial.Mode{BaudRate: romBaud})
}

// session connects at baud and writes every part not already in flash.
func (l *loader) session(o FlashOptions, baud, size int) error {
	if err := l.start(o, baud); err != nil {
		return err
	}
	o.progress("Configuring flash...", 0, 1)
	if err := l.setFlashParams(size); err != nil {
		return err
	}
	imgs := make([][]byte, len(o.Parts))
	total := 0
	for i, p := range o.Parts {
		img := padTo(slicesClone(p.Data), 4, 0xFF)
		if p.Offset == bootloaderAddr && !l.secure {
			var err error
			if img, err = updateImageFlashParams(o, img); err != nil {
				return fatalError{err}
			}
		}
		imgs[i] = img
		total += len(img)
	}
	done := 0
	for i, p := range o.Parts {
		img := imgs[i]
		if !o.Full {
			want := md5.Sum(img)
			if got, err := l.flashMD5(p.Offset, len(img)); err == nil && bytes.Equal(got, want[:]) {
				l.logf("%s at %#08x unchanged, skipped", p.Name, p.Offset)
				o.skipped(p)
				done += len(img)
				o.progress(fmt.Sprintf("%s unchanged, skipped", p.Name), done, total)
				continue
			}
		}
		n, err := l.writeCompressed(p, img, func(written int) {
			o.progress(fmt.Sprintf("Writing at %#08x...", int(p.Offset)+written), done+written, total)
		})
		if err != nil {
			return err
		}
		done += n
	}
	return nil
}

// flashMD5 is the MD5 of size bytes of flash at offset, computed on chip.
func (l *loader) flashMD5(offset uint32, size int) ([]byte, error) {
	_, got, err := l.check("calculate md5sum", opSPIFlashMD5, pack(offset, uint32(size), 0, 0), 0, 16,
		timeoutPerMB(8, size))
	return got, err
}

// start connects, checks the chip, runs the stub and raises the baud rate.
// A transient failure after connecting (a dropped stub upload, a missed baud
// change) restarts the whole sequence from a fresh reset, up to
// sessionAttempts times; connect itself already cycles reset strategies.
func (l *loader) start(o FlashOptions, baud int) error {
	var err error
	for attempt := 1; attempt <= sessionAttempts; attempt++ {
		if attempt > 1 {
			l.logf("retrying from reset (%d/%d): %v", attempt, sessionAttempts, err)
			if err := l.toROMBaud(); err != nil {
				return err
			}
		}
		o.progress("Connecting to device...", 0, 1)
		if err = l.connect(); err != nil {
			return err // already retried across reset strategies
		}
		if err = l.checkChip(o.Parts); err != nil {
			return fatalError{err} // a refusal, not a glitch
		}
		o.progress("Uploading flash stub...", 0, 1)
		if err = l.runStub(o.Chip.Name); err != nil {
			continue
		}
		if baud > romBaud {
			if err = l.changeBaud(baud); err != nil {
				continue
			}
		}
		return nil
	}
	return err
}

func (o FlashOptions) skipped(p FlashPart) {
	if o.Skipped != nil {
		o.Skipped(p)
	}
}

func (o FlashOptions) progress(msg string, done, total int) {
	if o.Progress != nil {
		o.Progress(msg, done, total)
	}
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

var flashSizeBytes = map[string]int{
	"1MB": 1 << 20, "2MB": 2 << 20, "4MB": 4 << 20, "8MB": 8 << 20,
	"16MB": 16 << 20, "32MB": 32 << 20, "64MB": 64 << 20, "128MB": 128 << 20,
}

// updateImageFlashParams sets the bootloader's flash mode, size and
// frequency to the flash options, fixing its appended SHA-256 if it changed.
func updateImageFlashParams(o FlashOptions, img []byte) ([]byte, error) {
	if len(img) < 24 || img[0] != imageMagic {
		return img, nil
	}
	mode, ok := flashModes[o.FlashMode]
	if !ok {
		return nil, fmt.Errorf("unknown flash mode %q", o.FlashMode)
	}
	sz, ok1 := o.Chip.FlashSizes[o.FlashSize]
	fr, ok2 := o.Chip.FlashFreqs[o.FlashFreq]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("unknown flash size/frequency %s/%s", o.FlashSize, o.FlashFreq)
	}
	if img[2] == mode && img[3] == sz+fr {
		return img, nil
	}
	length, err := imageDataLength(img)
	if err != nil {
		return img, nil // not a valid image: esptool leaves it alone too
	}
	img[2], img[3] = mode, sz+fr
	if img[8+15] == 1 { // SHA-256 appended
		sum := sha256.Sum256(img[:length])
		copy(img[length:], sum[:])
	}
	return img, nil
}

// imageDataLength walks an image's segments and returns the length up to
// and including the checksum block (where an appended digest starts).
func imageDataLength(img []byte) (int, error) {
	n := int(img[1])
	pos := 24
	for i := 0; i < n; i++ {
		if pos+8 > len(img) {
			return 0, errors.New("truncated image")
		}
		size := int(binary.LittleEndian.Uint32(img[pos+4:]))
		pos += 8 + size
	}
	pos += 16 - pos%16 // checksum is the last byte of a 16-byte block
	if pos+32 > len(img) {
		return 0, errors.New("truncated image")
	}
	return pos, nil
}

// loader talks esptool's serial protocol: SLIP-framed commands to the ROM
// loader, then to the flasher stub.
type loader struct {
	ctx    context.Context
	port   serial.Port
	log    io.Writer
	baud   int
	stub   bool
	rd     []byte // bytes read but not yet framed
	secure bool
}

func (l *loader) logf(format string, args ...any) {
	if l.log != nil {
		fmt.Fprintf(l.log, format+"\n", args...)
	}
}

func (l *loader) write(pkt []byte) error {
	var b bytes.Buffer
	b.WriteByte(0xC0)
	for _, c := range pkt {
		switch c {
		case 0xDB:
			b.Write([]byte{0xDB, 0xDD})
		case 0xC0:
			b.Write([]byte{0xDB, 0xDC})
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(0xC0)
	_, err := l.port.Write(b.Bytes())
	return err
}

// readPacket returns the next SLIP packet, waiting up to timeout for each
// read like pyserial's timeout.
func (l *loader) readPacket(timeout time.Duration) ([]byte, error) {
	var pkt []byte
	started, escape := false, false
	buf := make([]byte, 4096)
	for {
		for len(l.rd) > 0 {
			c := l.rd[0]
			l.rd = l.rd[1:]
			switch {
			case !started:
				if c != 0xC0 {
					rest := append([]byte{c}, l.rd...)
					l.rd = nil
					if m := panicRe.Find(rest); m != nil {
						return nil, fmt.Errorf("chip crashed: %s", m)
					}
					return nil, fmt.Errorf("invalid head of packet (%#02x): possible serial noise or corruption", c)
				}
				started = true
				pkt = []byte{}
			case escape:
				escape = false
				switch c {
				case 0xDC:
					pkt = append(pkt, 0xC0)
				case 0xDD:
					pkt = append(pkt, 0xDB)
				default:
					return nil, fmt.Errorf("invalid SLIP escape (0xdb, %#02x)", c)
				}
			case c == 0xDB:
				escape = true
			case c == 0xC0:
				return pkt, nil
			default:
				pkt = append(pkt, c)
			}
		}
		if err := l.ctx.Err(); err != nil {
			return nil, err
		}
		if err := l.port.SetReadTimeout(timeout); err != nil {
			return nil, err
		}
		n, err := l.port.Read(buf)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			if !started {
				return nil, errors.New("no serial data received")
			}
			return nil, fmt.Errorf("packet content transfer stopped (received %d bytes)", len(pkt))
		}
		l.rd = append(l.rd, buf[:n]...)
	}
}

var panicRe = regexp.MustCompile(`G?uru Meditation Error[^\n]*|F?atal exception \(\d+\)[^\n]*`)

func (l *loader) flushInput() {
	l.port.ResetInputBuffer()
	l.rd = nil
}

type unsupportedError struct{ op byte }

func (e unsupportedError) Error() string {
	return fmt.Sprintf("command %#02x is not supported by the loader", e.op)
}

// command sends a request (op 0: none, just read) and returns the response's
// value and data.
func (l *loader) command(op byte, data []byte, chk uint32, timeout time.Duration) (uint32, []byte, error) {
	if op != 0 {
		pkt := make([]byte, 8, 8+len(data))
		pkt[1] = op
		binary.LittleEndian.PutUint16(pkt[2:], uint16(len(data)))
		binary.LittleEndian.PutUint32(pkt[4:], chk)
		if err := l.write(append(pkt, data...)); err != nil {
			return 0, nil, err
		}
	}
	for range 100 {
		p, err := l.readPacket(timeout)
		if err != nil {
			return 0, nil, err
		}
		if len(p) < 8 || p[0] != 1 {
			continue
		}
		val := binary.LittleEndian.Uint32(p[4:])
		body := p[8:]
		if op == 0 || p[1] == op {
			return val, body, nil
		}
		if len(body) > 1 && body[0] != 0 && body[1] == 0x05 {
			time.Sleep(200 * time.Millisecond)
			l.flushInput()
			return 0, nil, unsupportedError{op}
		}
	}
	return 0, nil, errors.New("response doesn't match request")
}

// check runs a command and checks its status bytes (after respLen bytes of
// data), returning the value, or the data when respLen > 0.
func (l *loader) check(what string, op byte, data []byte, chk uint32, respLen int, timeout time.Duration) (uint32, []byte, error) {
	val, body, err := l.command(op, data, chk, timeout)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to %s: %w", what, err)
	}
	if len(body) < respLen+2 {
		if len(body) > 0 && body[0] != 0 {
			return 0, nil, fmt.Errorf("failed to %s (status %#x %#x)", what, body[0], body[1])
		}
		return 0, nil, fmt.Errorf("failed to %s: only got a %d byte status response", what, len(body))
	}
	if st := body[respLen : respLen+2]; st[0] != 0 {
		return 0, nil, fmt.Errorf("failed to %s (status %#x, error %#x)", what, st[0], st[1])
	}
	return val, body[:respLen], nil
}

func pack(vals ...uint32) []byte {
	b := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(b[4*i:], v)
	}
	return b
}

func checksum(data []byte) uint32 {
	c := byte(checksumSeed)
	for _, b := range data {
		c ^= b
	}
	return uint32(c)
}

func (l *loader) sync() error {
	data := append([]byte{0x07, 0x07, 0x12, 0x20}, bytes.Repeat([]byte{0x55}, 32)...)
	if _, _, err := l.command(opSync, data, 0, syncTimeout); err != nil {
		return err
	}
	for range 7 { // the ROM answers a sync several times
		if _, _, err := l.command(0, nil, 0, syncTimeout); err != nil {
			return err
		}
	}
	return nil
}

var bootRe = regexp.MustCompile(`(?s)boot:(0x[0-9a-fA-F]+)(.*waiting for download)?`)

// resetStrategy is one way of pulsing EN/IO0 into download mode.
type resetStrategy struct {
	name  string
	tight bool
	delay time.Duration
}

// resetStrategies mirrors esptool's default_reset order on POSIX: tight
// (atomic DTR+RTS) resets first, then classic ones, each with the normal
// and the longer delay. connect cycles through them.
func (l *loader) resetStrategies() []resetStrategy {
	var s []resetStrategy
	if ok, _ := setDTRRTS(l.port, false, false); ok {
		s = append(s, resetStrategy{"tight", true, 50 * time.Millisecond},
			resetStrategy{"tight", true, 550 * time.Millisecond})
	}
	return append(s, resetStrategy{"classic", false, 50 * time.Millisecond},
		resetStrategy{"classic", false, 550 * time.Millisecond})
}

// connect resets the chip into download mode and syncs, like esptool's
// default_reset.
func (l *loader) connect() error {
	var last error
	strategies := l.resetStrategies()
	for attempt := range connectAttempts {
		rs := strategies[attempt%len(strategies)]
		l.port.ResetInputBuffer()
		reset := l.classicReset
		if rs.tight {
			reset = l.tightReset
		}
		if err := reset(rs.delay); err != nil {
			return fmt.Errorf("resetting the chip: %w", err)
		}
		var boot []byte
		l.port.SetReadTimeout(10 * time.Millisecond)
		buf := make([]byte, 4096)
		for {
			n, _ := l.port.Read(buf)
			if n == 0 {
				break
			}
			boot = append(boot, buf[:n]...)
		}
		for range 5 {
			if err := l.ctx.Err(); err != nil {
				return err
			}
			l.flushInput()
			l.port.ResetOutputBuffer()
			if last = l.sync(); last == nil {
				l.logf("connected (attempt %d, %s reset, %v)", attempt+1, rs.name, rs.delay)
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		if m := bootRe.FindSubmatch(boot); m != nil {
			if len(m[2]) > 0 {
				last = errors.New("download mode detected, but no sync reply: the serial TX path seems to be down")
			} else {
				last = fmt.Errorf("wrong boot mode detected (%s): the chip needs to be in download mode", m[1])
			}
		}
	}
	return fmt.Errorf("%w to ESP32: %v", ErrConnect, last)
}

// tightReset is esptool's UnixTightReset: every step sets DTR and RTS
// together, so the chip never sees EN released with IO0 high.
func (l *loader) tightReset(delay time.Duration) error {
	set := func(dtr, rts bool) error { _, err := setDTRRTS(l.port, dtr, rts); return err }
	for _, st := range []struct{ dtr, rts bool }{{false, false}, {true, true}, {false, true}} {
		if err := set(st.dtr, st.rts); err != nil {
			return err
		}
	}
	time.Sleep(100 * time.Millisecond)       // IO0 high, EN low: in reset
	if err := set(true, false); err != nil { // IO0 low, EN high: out of reset
		return err
	}
	time.Sleep(delay)
	if err := set(false, false); err != nil { // IO0 high, done
		return err
	}
	return l.port.SetDTR(false) // some drivers need DTR set on its own too
}

func (l *loader) classicReset(delay time.Duration) error {
	steps := []func() error{
		func() error { return l.port.SetDTR(false) }, // IO0 high
		func() error { return l.port.SetRTS(true) },  // EN low: in reset
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	time.Sleep(100 * time.Millisecond)
	if err := l.port.SetDTR(true); err != nil { // IO0 low
		return err
	}
	if err := l.port.SetRTS(false); err != nil { // EN high: out of reset
		return err
	}
	time.Sleep(delay)
	return l.port.SetDTR(false) // IO0 high, done
}

func (l *loader) hardReset() error {
	if err := l.port.SetRTS(true); err != nil { // EN low
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return l.port.SetRTS(false)
}

func (l *loader) readReg(addr uint32) (uint32, error) {
	v, _, err := l.check("read target memory", opReadReg, pack(addr), 0, 0, defaultTimeout)
	return v, err
}

// checkChip makes sure the chip is an ESP32 the images are for, and refuses
// what esptool refuses without --force: writing the bootloader region with
// secure boot on, plaintext with flash encryption locked on, and images for
// another chip revision.
func (l *loader) checkChip(parts []FlashPart) error {
	magic, err := l.readReg(chipMagicReg)
	if err != nil {
		return err
	}
	if magic != esp32Magic {
		return fmt.Errorf("this chip is not an ESP32 (magic value %#08x)", magic)
	}
	efuse := func(n uint32) (uint32, error) { return l.readReg(efuseBase + 4*n) }
	w0, err := efuse(0)
	if err != nil {
		return err
	}
	w3, err := efuse(3)
	if err != nil {
		return err
	}
	w5, err := efuse(5)
	if err != nil {
		return err
	}
	w6, err := efuse(6)
	if err != nil {
		return err
	}
	date, err := l.readReg(apbCtlDateAddr)
	if err != nil {
		return err
	}
	combined := (date>>31&1)<<2 | (w5>>20&1)<<1 | w3>>15&1
	major := map[uint32]int{0: 0, 1: 1, 3: 2, 7: 3}[combined]
	rev := major*100 + int(w5>>24&3)

	secureBoot := w6&(1<<4) != 0 || (rev >= 300 && w6&(1<<5) != 0)
	encrypted := bits.OnesCount32(w0&(0x7F<<20))&1 == 1
	dlEncryptOff := w6&(1<<7) != 0
	for _, p := range parts {
		if secureBoot && p.Offset < 0x8000 {
			return errors.New("secure boot is enabled: writing below 0x8000 would overwrite the bootloader")
		}
		if encrypted && dlEncryptOff {
			return errors.New("flash encryption is enabled and manual encryption disabled: plaintext images would brick the device")
		}
		if len(p.Data) < 24 || p.Data[0] != imageMagic {
			continue
		}
		if id := binary.LittleEndian.Uint16(p.Data[12:]); id != 0 {
			return fmt.Errorf("%s is not an ESP32 image (chip id %d)", p.Name, id)
		}
		minRev := int(p.Data[14])
		minFull := int(binary.LittleEndian.Uint16(p.Data[15:]))
		maxFull := int(binary.LittleEndian.Uint16(p.Data[17:]))
		useFull := maxFull != 0 && !(maxFull == 0xFFFF && minFull == 0 && minRev != 0)
		switch {
		case useFull && (rev < minFull || rev > maxFull):
			return fmt.Errorf("%s needs chip revision v%d.%d-v%d.%d; this chip is v%d.%d",
				p.Name, minFull/100, minFull%100, maxFull/100, maxFull%100, rev/100, rev%100)
		case !useFull && major < minRev:
			return fmt.Errorf("%s needs chip revision %d or higher; this chip is %d", p.Name, minRev, major)
		}
	}
	l.secure = secureBoot
	return nil
}

func (l *loader) runStub(chip string) error {
	b, err := stubs.ReadFile("stub/" + chip + ".json")
	if err != nil {
		return fmt.Errorf("no flasher stub for %s", chip)
	}
	var s struct {
		Entry     uint32 `json:"entry"`
		Text      string `json:"text"`
		TextStart uint32 `json:"text_start"`
		Data      string `json:"data"`
		DataStart uint32 `json:"data_start"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for _, f := range []struct {
		b64  string
		addr uint32
	}{{s.Text, s.TextStart}, {s.Data, s.DataStart}} {
		if f.b64 == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(f.b64)
		if err != nil {
			return err
		}
		blocks := (len(data) + ramBlock - 1) / ramBlock
		if _, _, err := l.check("enter RAM download mode", opMemBegin,
			pack(uint32(len(data)), uint32(blocks), ramBlock, f.addr), 0, 0, defaultTimeout); err != nil {
			return err
		}
		for seq := range blocks {
			blk := data[seq*ramBlock : min(len(data), (seq+1)*ramBlock)]
			if _, _, err := l.check("write to target RAM", opMemData,
				append(pack(uint32(len(blk)), uint32(seq), 0, 0), blk...), checksum(blk), 0, defaultTimeout); err != nil {
				return err
			}
		}
	}
	// The ROM may not answer MEM_END before jumping to the stub.
	l.check("leave RAM download mode", opMemEnd, pack(0, s.Entry), 0, 0, memEndROMTimeout)
	p, err := l.readPacket(defaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to start the flasher stub: %w", err)
	}
	if string(p) != "OHAI" {
		return fmt.Errorf("failed to start the flasher stub: unexpected response %q", p)
	}
	l.stub = true
	l.logf("stub running")
	return nil
}

func (l *loader) changeBaud(baud int) error {
	if _, _, err := l.command(opChangeBaud, pack(uint32(baud), uint32(l.baud)), 0, defaultTimeout); err != nil {
		return fmt.Errorf("changing baud rate: %w", err)
	}
	if err := l.port.SetMode(&serial.Mode{BaudRate: baud}); err != nil {
		return err
	}
	l.baud = baud
	time.Sleep(50 * time.Millisecond) // drop what was sent during the change
	l.flushInput()
	return nil
}

func (l *loader) setFlashParams(size int) error {
	_, _, err := l.check("set SPI params", opSPISetParams,
		pack(0, uint32(size), 64*1024, 4*1024, 256, 0xFFFF), 0, 0, defaultTimeout)
	return err
}

func timeoutPerMB(secondsPerMB float64, size int) time.Duration {
	t := time.Duration(secondsPerMB * float64(size) / 1e6 * float64(time.Second))
	return max(t, defaultTimeout)
}

// writeCompressed writes one image deflated, then checks its MD5 in flash.
// It returns the bytes written.
func (l *loader) writeCompressed(p FlashPart, img []byte, progress func(int)) (int, error) {
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
	zw.Write(img)
	zw.Close()
	comp := z.Bytes()
	blocks := (len(comp) + stubWriteSize - 1) / stubWriteSize
	if _, _, err := l.check("enter compressed flash mode", opFlashDeflBeg,
		pack(uint32(len(img)), uint32(blocks), stubWriteSize, p.Offset, 0), 0, 0, defaultTimeout); err != nil {
		return 0, err
	}
	ratio := float64(len(img)) / float64(len(comp))
	timeout := defaultTimeout
	start := time.Now()
	for seq := range blocks {
		if err := l.ctx.Err(); err != nil {
			return 0, err
		}
		blk := comp[seq*stubWriteSize : min(len(comp), (seq+1)*stubWriteSize)]
		written := min(len(img), int(float64((seq+1)*stubWriteSize)*ratio))
		progress(written)
		var err error
		for range writeAttempts {
			_, _, err = l.check(fmt.Sprintf("write compressed data to flash after seq %d", seq), opFlashDeflData,
				append(pack(uint32(len(blk)), uint32(seq), 0, 0), blk...), checksum(blk), 0, timeout)
			if err == nil {
				break
			}
		}
		if err != nil {
			return 0, err
		}
		// The stub acks a block before writing it: the next command waits for
		// this block's erase and write.
		timeout = timeoutPerMB(40, int(float64(len(blk))*ratio))
	}
	if _, _, err := l.check("leave compressed flash mode", opFlashDeflEnd, pack(1), 0, 0, timeout); err != nil {
		return 0, err
	}
	progress(len(img))
	l.logf("wrote %d bytes (%d compressed) at %#08x in %.1fs", len(img), len(comp), p.Offset, time.Since(start).Seconds())

	got, err := l.flashMD5(p.Offset, len(img))
	if err != nil {
		return 0, err
	}
	want := md5.Sum(img)
	if !bytes.Equal(got, want[:]) {
		return 0, fmt.Errorf("%s: MD5 of file (%s) does not match data in flash (%s)", p.Name, hex.EncodeToString(want[:]), hex.EncodeToString(got))
	}
	l.logf("hash of data verified at %#08x", p.Offset)
	return len(img), nil
}

// Probe reports whether an ESP32 in download mode answers on port, without
// resetting it (esptool --before no_reset chip_id): a live chip, rather than
// a bare USB-serial bridge or a board running firmware.
func Probe(ctx context.Context, port string) bool {
	p, err := serial.Open(port, &serial.Mode{BaudRate: romBaud})
	if err != nil {
		return false
	}
	defer p.Close()
	l := &loader{ctx: ctx, port: p, baud: romBaud}
	for range 5 {
		if ctx.Err() != nil {
			return false
		}
		l.flushInput()
		if l.sync() == nil {
			_, err := l.readReg(chipMagicReg)
			return err == nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
