package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PhilemonChiro/volt-cli/internal/proc"
)

// Large archives are fetched as several byte ranges at once: on long, lossy
// routes one TCP connection runs far below the link's capacity. A connection
// that stalls is dropped and resumed from the byte it reached, and the
// progress survives across runs in dest+stateSuffix, so an interrupted
// install continues where it stopped.
const (
	parallelMin  = 32 << 20         // files at least this big are split
	parallelN    = 4                // ranges fetched at once
	stallWindow  = 20 * time.Second // a connection must move...
	stallMin     = 32 << 10         // ...at least this many bytes per window
	maxRetries   = 8                // fruitless reconnects per range before trying the next source
	stateSuffix  = ".parts.json"
	saveInterval = time.Second
)

var errStalled = errors.New("download stalled")

type part struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"` // inclusive
	Got   int64 `json:"got"` // bytes written from Start
}

func (p *part) done() bool { return p.Start+p.Got > p.End }

// partState is the resumable progress of one download, tied to the asset's
// SHA-256 so a manifest change never resumes a different file.
type partState struct {
	SHA256 string  `json:"sha256"`
	Size   int64   `json:"size"`
	Parts  []*part `json:"parts"`
}

// fetch downloads url into dest and checks a's SHA-256. An earlier partial
// download of the same asset into dest (from any source) is resumed.
func fetch(e *Emitter, url string, a Asset, dest string) error {
	ctx, cancel := context.WithCancel(proc.Root)
	defer cancel()

	size, ranges, err := probe(ctx, url)
	if err != nil {
		return err
	}
	if a.SizeBytes > 0 && size > 0 && size != a.SizeBytes {
		return fmt.Errorf("%s is %d bytes, manifest says %d", url, size, a.SizeBytes)
	}
	if size <= 0 {
		size = a.SizeBytes
	}

	st := loadState(dest, a.SHA256, size)
	if st == nil || !ranges || size <= 0 {
		st = newState(a.SHA256, size, ranges)
		os.Remove(dest)
	} else if g := st.got(); g > 0 {
		e.Log("Resuming at " + humanSize(g) + " / " + humanSize(size))
	}

	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if size > 0 {
		if err := f.Truncate(size); err != nil {
			return err
		}
	}

	// One goroutine per unfinished range; the first hard failure cancels
	// the rest. Progress and state saving stay on this goroutine.
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		logs     = make(chan string, 16)
	)
	for _, p := range st.Parts {
		if p.done() {
			continue
		}
		wg.Add(1)
		go func(p *part) {
			defer wg.Done()
			if err := getPart(ctx, url, f, p, ranges, logs); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				cancel()
			}
		}(p)
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	lastSave := time.Now()
	report := func() {
		if got := st.got(); size > 0 {
			e.Progress(int(min(99, got*100/size)), "download "+humanSize(got)+" / "+humanSize(size))
		}
	}
loop:
	for {
		select {
		case <-finished:
			break loop
		case line := <-logs:
			e.Log(line)
		case <-tick.C:
			report()
			if ranges && time.Since(lastSave) >= saveInterval {
				st.save(dest)
				lastSave = time.Now()
			}
		}
	}
	for len(logs) > 0 {
		e.Log(<-logs)
	}
	report()
	if firstErr != nil {
		if ranges {
			st.save(dest) // keep what we have for the next source or run
		}
		if proc.Root.Err() != nil {
			return proc.Root.Err()
		}
		return firstErr
	}

	os.Remove(dest + stateSuffix)
	e.Progress(99, "verify: "+a.Filename)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != a.SHA256 {
		os.Remove(dest)
		return fmt.Errorf("sha256 mismatch from %s (got %s…, want %s…)", url, sum[:16], a.SHA256[:16])
	}
	e.Progress(100, "download "+humanSize(size))
	return f.Close()
}

// probe asks for the first byte to learn the size and whether the server
// honours byte ranges.
func probe(ctx context.Context, url string) (size int64, ranges bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		// Content-Range: bytes 0-0/<total>
		cr := resp.Header.Get("Content-Range")
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
				return n, true, nil
			}
		}
		return 0, false, nil
	case http.StatusOK:
		return resp.ContentLength, false, nil
	}
	return 0, false, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
}

func newState(sha string, size int64, ranges bool) *partState {
	n := int64(1)
	if ranges && size >= parallelMin {
		n = parallelN
	}
	end := size - 1
	if size <= 0 {
		end = 1<<62 - 1 // unknown size: read to EOF
	}
	st := &partState{SHA256: sha, Size: size}
	chunk := (end + 1) / n
	for i := int64(0); i < n; i++ {
		p := &part{Start: i * chunk, End: (i+1)*chunk - 1}
		if i == n-1 {
			p.End = end
		}
		st.Parts = append(st.Parts, p)
	}
	return st
}

func loadState(dest, sha string, size int64) *partState {
	b, err := os.ReadFile(dest + stateSuffix)
	if err != nil {
		return nil
	}
	var st partState
	if json.Unmarshal(b, &st) != nil || st.SHA256 != sha || st.Size != size || size <= 0 {
		return nil
	}
	if _, err := os.Stat(dest); err != nil {
		return nil
	}
	return &st
}

func (st *partState) got() int64 {
	var n int64
	for _, p := range st.Parts {
		n += atomic.LoadInt64(&p.Got)
	}
	return n
}

func (st *partState) save(dest string) {
	snap := partState{SHA256: st.SHA256, Size: st.Size}
	for _, p := range st.Parts {
		snap.Parts = append(snap.Parts, &part{Start: p.Start, End: p.End, Got: atomic.LoadInt64(&p.Got)})
	}
	b, _ := json.Marshal(snap)
	tmp := dest + stateSuffix + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, dest+stateSuffix)
	}
}

// getPart fills p, reconnecting after errors and stalls. Without range
// support a reconnect has to start the (single) part over.
func getPart(ctx context.Context, url string, f *os.File, p *part, ranges bool, logs chan<- string) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case logs <- fmt.Sprintf("Reconnecting (%v); attempt %d of %d", lastErr, attempt, maxRetries):
			default:
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(min(attempt, 5)) * 2 * time.Second):
			}
			if !ranges {
				atomic.StoreInt64(&p.Got, 0)
			}
		}
		before := atomic.LoadInt64(&p.Got)
		err := getPartOnce(ctx, url, f, p, ranges)
		if err == nil || p.done() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = err
		// Only attempts that got nowhere count towards giving up.
		if ranges && atomic.LoadInt64(&p.Got) > before {
			attempt = 0
		}
	}
	return fmt.Errorf("%s: %w after %d retries", url, lastErr, maxRetries)
}

func getPartOnce(parent context.Context, url string, f *os.File, p *part, ranges bool) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	off := p.Start + atomic.LoadInt64(&p.Got)
	if ranges {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, p.End))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	want := http.StatusOK
	if ranges {
		want = http.StatusPartialContent
	}
	if resp.StatusCode != want {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Watchdog: drop the connection if it moves too little per window.
	var moved int64
	go func() {
		t := time.NewTicker(stallWindow)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if atomic.SwapInt64(&moved, 0) < stallMin {
					cancel(errStalled)
					return
				}
			}
		}
	}()

	buf := make([]byte, 256<<10)
	for !p.done() {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if left := p.End - off + 1; int64(n) > left {
				n = int(left)
			}
			if _, err := f.WriteAt(buf[:n], off); err != nil {
				return err
			}
			off += int64(n)
			atomic.AddInt64(&p.Got, int64(n))
			atomic.AddInt64(&moved, int64(n))
		}
		if rerr == io.EOF {
			if !ranges || p.done() {
				p.End = off - 1 // unknown size: EOF ends the part
				return nil
			}
			return io.ErrUnexpectedEOF
		}
		if rerr != nil {
			if c := context.Cause(ctx); c != nil && parent.Err() == nil {
				return c
			}
			return rerr
		}
	}
	return nil
}
