// Package transcripttail reads complete jsonl lines from the end of (or from a
// byte offset in) a transcript file without loading the whole file. It only
// ever cuts on '\n' boundaries, so a returned line is never partial and never
// splits a UTF-8 sequence (0x0A cannot occur inside a multibyte rune).
package transcripttail

import (
	"bytes"
	"errors"
	"io"
)

const (
	// blockSize is the I/O chunk used when scanning for newlines.
	blockSize = 64 << 10
	// MaxLineBytes is the hard ceiling for one line; a longer line is an error.
	MaxLineBytes = 8 << 20
)

// ErrLineTooLarge means a single line exceeds MaxLineBytes.
var ErrLineTooLarge = errors.New("transcript line too large")

// Result is one window of complete lines. Start and End are byte offsets:
// Start is always at a line start, End is just past the last returned line's
// newline (or equals Start when no line was returned). More means content
// remains beyond the byte cap (older for Tail, newer for After).
type Result struct {
	Lines []string
	Start int64
	End   int64
	More  bool
}

// lastNewline returns the index of the last '\n' in [lo,hi). If none is found
// it reports found=false; exceeded=true when the scan gave up after
// maxScan bytes without finding one (and lo had not yet been reached).
func lastNewline(r io.ReaderAt, lo, hi int64, maxScan int64) (idx int64, found, exceeded bool, err error) {
	buf := make([]byte, blockSize)
	scanned := int64(0)
	for hi > lo {
		if scanned > maxScan {
			return 0, false, true, nil
		}
		start := hi - blockSize
		if start < lo {
			start = lo
		}
		b := buf[:hi-start]
		if _, e := r.ReadAt(b, start); e != nil && e != io.EOF {
			return 0, false, false, e
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			return start + int64(i), true, false, nil
		}
		scanned += int64(len(b))
		hi = start
	}
	return 0, false, false, nil
}

// nextNewline returns the index of the first '\n' in [lo,hi), found=false if
// none, exceeded=true if more than maxScan bytes were scanned without one.
func nextNewline(r io.ReaderAt, lo, hi, maxScan int64) (idx int64, found, exceeded bool, err error) {
	buf := make([]byte, blockSize)
	scanned := int64(0)
	for lo < hi {
		if scanned > maxScan {
			return 0, false, true, nil
		}
		end := lo + blockSize
		if end > hi {
			end = hi
		}
		b := buf[:end-lo]
		if _, e := r.ReadAt(b, lo); e != nil && e != io.EOF {
			return 0, false, false, e
		}
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return lo + int64(i), true, false, nil
		}
		scanned += int64(len(b))
		lo = end
	}
	return 0, false, false, nil
}

// completeEnd is the offset just past the last '\n' in [0,size) (0 if none):
// everything before it is complete lines, the rest is an unfinished line.
func completeEnd(r io.ReaderAt, size int64) (int64, error) {
	i, found, _, err := lastNewline(r, 0, size, size)
	if err != nil || !found {
		return 0, err
	}
	return i + 1, nil
}

func readRange(r io.ReaderAt, start, end int64) ([]string, error) {
	if end <= start {
		return []string{}, nil
	}
	b := make([]byte, end-start)
	if _, err := r.ReadAt(b, start); err != nil && err != io.EOF {
		return nil, err
	}
	b = bytes.TrimSuffix(b, []byte{'\n'})
	parts := bytes.Split(b, []byte{'\n'})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out, nil
}

// Tail returns up to n newest complete lines of a file of the given size,
// totalling at most maxBytes (older lines are dropped first). A newest line
// that alone exceeds maxBytes is returned on its own with More set.
func Tail(r io.ReaderAt, size int64, n, maxBytes int) (Result, error) {
	end, err := completeEnd(r, size)
	if err != nil {
		return Result{}, err
	}
	res := Result{Lines: []string{}, Start: end, End: end}
	start, count := end, 0
	for count < n && start > 0 {
		// The line ending at start-1 begins after the previous '\n'.
		idx, found, tooLong, err := lastNewline(r, 0, start-1, MaxLineBytes)
		s := int64(0)
		if found {
			s = idx + 1
		}
		if err != nil {
			return Result{}, err
		}
		if tooLong || start-1-s > MaxLineBytes {
			if count == 0 {
				return Result{}, ErrLineTooLarge
			}
			res.More = true
			break
		}
		if count > 0 && end-s > int64(maxBytes) {
			res.More = true
			break
		}
		start = s
		count++
		if end-s > int64(maxBytes) { // lone oversize line
			res.More = true
			break
		}
	}
	lines, err := readRange(r, start, end)
	if err != nil {
		return Result{}, err
	}
	res.Lines, res.Start = lines, start
	return res, nil
}

// After returns complete lines starting at byte offset off. An offset that is
// not at a line start discards the half line up to the next '\n'. Lines total
// at most maxBytes; one line larger than that is returned alone with More set.
func After(r io.ReaderAt, size, off int64, maxBytes int) (Result, error) {
	end, err := completeEnd(r, size)
	if err != nil {
		return Result{}, err
	}
	if off >= end {
		return Result{Lines: []string{}, Start: end, End: end}, nil
	}
	start := off
	if off > 0 {
		var prev [1]byte
		if _, err := r.ReadAt(prev[:], off-1); err != nil && err != io.EOF {
			return Result{}, err
		}
		if prev[0] != '\n' {
			idx, _, _, err := nextNewline(r, off, end, int64(end-off)+1)
			if err != nil {
				return Result{}, err
			}
			start = idx + 1 // exists: end-1 is a '\n' and off < end
		}
	}
	if start >= end {
		return Result{Lines: []string{}, Start: end, End: end}, nil
	}
	winEnd := start + int64(maxBytes)
	if winEnd > end {
		winEnd = end
	}
	// Largest prefix of the window ending on a '\n'.
	cut := int64(-1)
	if idx, found, _, err := lastNewline(r, start, winEnd, int64(winEnd-start)); err != nil {
		return Result{}, err
	} else if found {
		cut = idx + 1
	}
	more := false
	if cut < 0 { // first line does not fit the cap: it goes out alone
		idx, found, tooLong, err := nextNewline(r, start, end, MaxLineBytes)
		if err != nil {
			return Result{}, err
		}
		if tooLong || !found || idx-start > MaxLineBytes {
			return Result{}, ErrLineTooLarge
		}
		cut, more = idx+1, true
	} else if cut < end {
		more = true
	}
	lines, err := readRange(r, start, cut)
	if err != nil {
		return Result{}, err
	}
	return Result{Lines: lines, Start: start, End: cut, More: more}, nil
}
