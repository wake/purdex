package fleet

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
)

// MaxFrame is the most one frame may hold (spec §5). A bigger one is dropped whole, and the reader goes on with the next.
const MaxFrame = 64 << 10

var (
	// ErrFrameTooBig: a frame passed MaxFrame bytes; it was skipped to its @@end.
	ErrFrameTooBig = errors.New("fleet: frame over the size cap")
	// ErrIncompleteFrame: a new @@f began before the previous frame's @@end; the earlier one was dropped.
	ErrIncompleteFrame = errors.New("fleet: frame without @@end")
)

// FrameReader splits a loop's stdout into frames. It has no token limit of its own (no bufio.Scanner): a long line is read
// in pieces and only counts against MaxFrame. Not safe for concurrent use.
type FrameReader struct {
	r *bufio.Reader
}

// NewFrameReader reads frames from r.
func NewFrameReader(r io.Reader) *FrameReader { return &FrameReader{r: bufio.NewReaderSize(r, 4096)} }

// line reads one line without its newline. Past limit bytes it keeps reading but stops keeping (kept false).
func (fr *FrameReader) line(buf []byte, limit int) (out []byte, kept bool, err error) {
	kept = true
	for {
		chunk, e := fr.r.ReadSlice('\n')
		if len(buf)+len(chunk) > limit {
			kept = false
		}
		if kept {
			buf = append(buf, chunk...)
		}
		switch {
		case e == nil:
			return bytes.TrimRight(buf, "\r\n"), kept, nil
		case errors.Is(e, bufio.ErrBufferFull):
			continue
		default:
			return buf, kept, e // EOF with a partial line: the caller drops it
		}
	}
}

// Next returns the next complete frame as text, from its @@f line to its @@end line inclusive. Lines outside a frame (a
// banner, noise) are ignored. A bad frame comes back as ErrFrameTooBig or ErrIncompleteFrame with no text, and the next call
// goes on; a read error or io.EOF ends the stream (a partial frame at the end is dropped).
func (fr *FrameReader) Next() (string, error) {
	var frame []byte
	in, big := false, false
	for {
		raw, kept, err := fr.line(nil, MaxFrame)
		if err != nil {
			return "", err
		}
		l := string(raw)
		switch {
		case !kept && !in:
			continue // a huge line outside a frame is noise
		case l == FrameStart && kept:
			if in {
				// The previous frame never ended. Count it, and start this one on the next call.
				fr.unread(l)
				return "", ErrIncompleteFrame
			}
			in, frame = true, append(frame[:0], l...)
			frame = append(frame, '\n')
		case !in:
			continue
		case l == FrameEnd && kept:
			if big {
				return "", ErrFrameTooBig
			}
			frame = append(frame, l...)
			frame = append(frame, '\n')
			return string(frame), nil
		default:
			if !kept || len(frame)+len(raw)+1 > MaxFrame {
				big = true
				frame = frame[:0]
				continue
			}
			if !big {
				frame = append(frame, raw...)
				frame = append(frame, '\n')
			}
		}
	}
}

// IsBadFrame reports whether err is a frame-level fault of FrameReader.Next (a frame over the cap, or one without its @@end):
// a frame that counts, like a ParseFast error, toward the five unparsable frames in a row that restart a session.
func IsBadFrame(err error) bool {
	return errors.Is(err, ErrFrameTooBig) || errors.Is(err, ErrIncompleteFrame)
}

// unread puts one line back in front of the stream.
func (fr *FrameReader) unread(l string) {
	fr.r = bufio.NewReaderSize(io.MultiReader(strings.NewReader(l+"\n"), fr.r), 4096)
}

// Fast is the one-second part of a frame, in the shape of the local source plus what the local source does not carry.
type Fast struct {
	LocalFast
	OS         string
	DiskPct    float64 // used percent of the disk mount (DiskMount)
	DiskFreeGB float64
	// RateKnown is false while the page window has no baseline yet, or the host gives no paging counters.
	RateKnown bool
	// LevelKnown is false on a Linux host without PSI (Level is then 0 and the zone uses the page rate only).
	LevelKnown bool
}

// ParseFast reads the fast part of one frame. at is the receive time (the remote clock is never used). The page rate comes
// from win, the same window for every host; a nil win leaves it 0 and unknown.
func ParseFast(frame string, at time.Time, win *PageWindow) (Fast, error) {
	sec, err := splitSections(frame)
	if err != nil {
		return Fast{}, err
	}
	f := Fast{LocalFast: LocalFast{At: at}}
	f.OS = strings.TrimSpace(sec["os"])
	if f.OS != "Darwin" && f.OS != "Linux" {
		return Fast{}, fmt.Errorf("unsupported os %q", f.OS)
	}
	if f.NCPU, err = strconv.Atoi(strings.TrimSpace(sec["ncpu"])); err != nil || f.NCPU <= 0 {
		return Fast{}, errors.New("bad cpu count")
	}
	if f.Load1, err = parseLoad1(sec["load"]); err != nil {
		return Fast{}, err
	}
	if f.CPU, err = parseCPU(sec["cpu"]); err != nil {
		return Fast{}, err
	}
	var counter uint64
	var haveCounter bool
	if f.OS == "Darwin" {
		if f.Mem, err = darwinMem(sec["memsize"], sec["vm"], f.NCPU); err != nil {
			return Fast{}, err
		}
		lv, e := strconv.Atoi(strings.TrimSpace(sec["level"]))
		if e != nil || lv < 1 || lv > 4 {
			return Fast{}, errors.New("bad pressure level")
		}
		f.Level, f.LevelKnown = lv, true
		if vm, e := hostmem.ParseVMStat([]byte(sec["vm"])); e == nil && vm.CountersErr() == nil {
			counter, haveCounter = vm.Pageouts+vm.Swapouts, true
		}
	} else {
		if f.Mem, err = linuxMem(sec["vm"]); err != nil {
			return Fast{}, err
		}
		f.Level, f.LevelKnown = psiLevel(sec["level"])
		counter, haveCounter = linuxSwapCounter(sec["vm"])
	}
	if f.DiskPct, err = parseDF(sec["disk"]); err != nil {
		return Fast{}, err
	}
	f.DiskFreeGB = dfFreeGB(sec["disk"])
	if win != nil && haveCounter {
		f.PageRate, f.RateKnown = win.Observe(at, counter)
	}
	return f, nil
}

// parseCPU reads "us sy id" (iostat, or the Linux loop's own figures) as the busy percentage, 100 - id.
func parseCPU(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) != 3 {
		return 0, errors.New("bad cpu line")
	}
	id, err := strconv.ParseFloat(f[2], 64)
	if err != nil || math.IsNaN(id) || id < 0 || id > 100 {
		return 0, errors.New("bad cpu line")
	}
	return 100 - id, nil
}

// psiLevel maps /proc/pressure/memory to the kernel-style level: full avg10 >= 10 -> 4, some avg10 >= 10 -> 2, else 1; no
// PSI (an empty section, or no avg10 on the some line) -> 0, unknown. First values, tuned on the live record (spec §5).
func psiLevel(s string) (int, bool) {
	var some, full float64
	var haveSome bool
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "some" && f[0] != "full") {
			continue
		}
		for _, kv := range f[1:] {
			v, ok := strings.CutPrefix(kv, "avg10=")
			if !ok {
				continue
			}
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			if f[0] == "some" {
				some, haveSome = n, true
			} else {
				full = n
			}
		}
	}
	switch {
	case !haveSome:
		return 0, false
	case full >= 10:
		return 4, true
	case some >= 10:
		return 2, true
	}
	return 1, true
}

// linuxSwapCounter is pswpin + pswpout from the loop's vm section.
func linuxSwapCounter(s string) (uint64, bool) {
	var in, out uint64
	var sawIn, sawOut bool
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "pswpin":
			in, sawIn = n, true
		case "pswpout":
			out, sawOut = n, true
		}
	}
	return in + out, sawIn && sawOut
}

// dfFreeGB is the Available column (KiB) of the last df line, in GiB; 0 when it cannot be read.
func dfFreeGB(s string) float64 {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0
	}
	kb, err := strconv.ParseFloat(f[3], 64)
	if err != nil || kb < 0 {
		return 0
	}
	return kb / (1 << 20)
}

// Slow is the five-second part of a frame.
type Slow struct {
	LocalSlow
	// GateBad: the gate section was there but did not parse (or was cut); Gate is nil and the queue is blank, the lists stay.
	GateBad bool
	// GateNone: the host has no pdx (the loop printed `@@gate none`).
	GateNone bool
}

// ParseSlow reads the slow part of a frame; ok is false when the frame carries none. The process lists are best effort (a
// line that does not read is skipped); the gate section never fails the frame.
func ParseSlow(frame string, at time.Time) (s Slow, ok bool) {
	if !strings.Contains(frame, "\n@@s\n") {
		return Slow{}, false
	}
	sec, err := splitSections(frame)
	if err != nil {
		return Slow{}, false
	}
	s.At = at
	s.TopCPU = parseProcs(sec["top_cpu"])
	s.TopMem = parseProcs(sec["top_mem"])
	if _, none := sec["gate none"]; none {
		s.GateNone = true
		return s, true
	}
	body, has := sec["gate"]
	if !has {
		s.GateBad = true
		return s, true
	}
	var st gate.Status
	dec := json.NewDecoder(strings.NewReader(body))
	if err := dec.Decode(&st); err != nil {
		s.GateBad = true
		return s, true
	}
	// The slot tokens are secrets of the remote host; nothing here needs them.
	for _, hs := range [][]gate.Holder{st.Heavy, st.Light, st.Suspect} {
		for i := range hs {
			hs[i].Token = ""
		}
	}
	s.Gate = &st
	return s, true
}

// parseProcs reads `pid pcpu rss_kb name…` lines. The name may hold spaces.
func parseProcs(s string) []Proc {
	var out []Proc
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		cpu, e2 := strconv.ParseFloat(f[1], 64)
		rss, e3 := strconv.ParseFloat(f[2], 64)
		if e1 != nil || e2 != nil || e3 != nil || cpu < 0 || rss < 0 {
			continue
		}
		out = append(out, Proc{PID: pid, Name: strings.Join(f[3:], " "), CPU: cpu, RSSMB: rss / 1024})
	}
	return out
}
