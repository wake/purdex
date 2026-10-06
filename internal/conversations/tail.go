package conversations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// TailWindow is how much of a transcript's end the tail read parses (§13.6).
const TailWindow = 256 << 10

// Tail is what the last TailWindow bytes of a transcript say.
type Tail struct{ LastEntrypoint, CustomTitle, AITitle string }

// tailLine is the part of a transcript line the tail read looks at.
type tailLine struct {
	Type        string `json:"type"`
	Entrypoint  string `json:"entrypoint"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
}

// ReadTail parses the last TailWindow bytes of a file of the given size:
// [max(0, size-TailWindow), size), read with ReadAt. When the window does
// not start at byte 0, the byte before it is read too, and everything up to
// and including the first '\n' is dropped: a line the window's edge cuts is
// skipped (§13.6), while a line that starts exactly at the window start
// (the byte before it is that '\n') is kept whole. Every remaining piece,
// the final one without '\n' included, is parsed and skipped when it is not
// a JSON object. LastEntrypoint is the last entrypoint; CustomTitle and
// AITitle are the last non-empty (TrimSpace'd) customTitle of a
// custom-title line and aiTitle of an ai-title line. n is the number of
// bytes read, the byte before the window included; a file that shrank below
// size gives what is left.
func ReadTail(f *os.File, size int64) (t Tail, n int64, err error) {
	if size <= 0 {
		return t, 0, nil
	}
	start := max(0, size-TailWindow)
	from := start
	if start > 0 {
		from = start - 1
	}
	buf := make([]byte, size-from)
	m, err := f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return t, int64(m), fmt.Errorf("conversations: read tail: %w", err)
	}
	buf = buf[:m]
	if start > 0 {
		// buf[0] is the byte before the window: when it is '\n', only it is
		// dropped.
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return t, int64(m), nil
		}
		buf = buf[i+1:]
	}
	for len(buf) > 0 {
		line := buf
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			buf = nil
		}
		applyTailLine(&t, line)
	}
	return t, int64(m), nil
}

func applyTailLine(t *Tail, line []byte) {
	var l tailLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	if l.Entrypoint != "" {
		t.LastEntrypoint = l.Entrypoint
	}
	switch l.Type {
	case "custom-title":
		if v := strings.TrimSpace(l.CustomTitle); v != "" {
			t.CustomTitle = v
		}
	case "ai-title":
		if v := strings.TrimSpace(l.AITitle); v != "" {
			t.AITitle = v
		}
	}
}
