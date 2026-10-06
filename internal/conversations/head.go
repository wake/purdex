package conversations

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// headCap is how far into a transcript the head scan reads, from byte 0
// (§13.6: until the first human prompt, at most 16 MB). It is a var only so
// tests can lower it.
var headCap int64 = 16 << 20

// headBufSize is the read size of the head scan; a longer line is
// assembled from several reads.
const headBufSize = 64 << 10

// Head is what the head scan has found so far.
type Head struct {
	Cwd, FirstEntrypoint, FirstPrompt string
	Offset                            int64 // the byte after the last complete line examined
	Done                              bool  // FirstPrompt found, or headCap bytes examined
}

// headLine is the part of a transcript line the head scan looks at.
// message.content stays raw: humanPrompt decodes it for user lines only.
type headLine struct {
	Type             string `json:"type"`
	Cwd              string `json:"cwd"`
	Entrypoint       string `json:"entrypoint"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// ScanHead continues a head scan of f from prev.Offset (prev is the zero
// Head for a fresh scan). It reads complete lines only: a final line with no
// '\n' is left for the next scan, so Offset is always a line boundary. It
// stops at the first human prompt (Done, Offset = the end of that line), at
// headCap bytes from 0 (Done; a line cut by the cap is skipped), or at the
// last complete line (not Done). Fields already set in prev are kept; empty
// ones are filled in file order. n is the number of bytes read from f,
// read-ahead past Offset included. A done prev is returned as is, with n 0.
//
// f is read with ReadAt, so its file offset is neither used nor moved. On a
// read error, h holds what the complete lines before it gave, and is as
// valid to resume from as any other result.
func ScanHead(f *os.File, prev Head) (h Head, n int64, err error) {
	h = prev
	if h.Done {
		return h, 0, nil
	}
	if h.Offset >= headCap {
		h.Done = true
		return h, 0, nil
	}
	src := &countingReader{r: io.NewSectionReader(f, h.Offset, headCap-h.Offset)}
	br := bufio.NewReaderSize(src, headBufSize)
	var long []byte // a line longer than the buffer, assembled
	for {
		piece, rerr := br.ReadSlice('\n')
		if errors.Is(rerr, bufio.ErrBufferFull) {
			long = append(long, piece...)
			continue
		}
		if errors.Is(rerr, io.EOF) {
			break // piece (and long) is a line without '\n' yet, or one cut by the cap
		}
		if rerr != nil {
			return h, src.n, fmt.Errorf("conversations: read head: %w", rerr)
		}
		line := piece
		if len(long) > 0 {
			long = append(long, piece...)
			line = long
		}
		h.Offset += int64(len(line))
		if examineHeadLine(&h, line) {
			h.Done = true
			return h, src.n, nil
		}
		long = long[:0]
	}
	if prev.Offset+src.n >= headCap {
		h.Done = true // the cap was reached; a line it cuts is never examined
	}
	return h, src.n, nil
}

// examineHeadLine fills h's empty fields from one complete line and reports
// whether the line is a human prompt. A line that does not decode as a JSON
// object is skipped.
func examineHeadLine(h *Head, line []byte) bool {
	var l headLine
	if json.Unmarshal(line, &l) != nil {
		return false
	}
	if h.Cwd == "" {
		h.Cwd = l.Cwd
	}
	if h.FirstEntrypoint == "" {
		h.FirstEntrypoint = l.Entrypoint
	}
	text, ok := humanPrompt(&l)
	if !ok {
		return false
	}
	if h.FirstPrompt == "" {
		h.FirstPrompt = text
	}
	return true
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	m, err := c.r.Read(p)
	c.n += int64(m)
	return m, err
}
