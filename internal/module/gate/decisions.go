package gatemod

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/gate"
)

const (
	defaultLimit = 200
	maxLimit     = 1000
)

// DecisionsReply is the body of GET /api/gate/decisions.
type DecisionsReply struct {
	Records []gate.Decision `json:"records"`
	Skipped int             `json:"skipped"`
}

// handleDecisions serves GET /api/gate/decisions?class=heavy|light&since=<RFC 3339>&limit=<n>: the gate's records, newest first.
func (m *Module) handleDecisions(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r, "reads the gate's decisions") {
		return
	}
	q := r.URL.Query()
	var class gate.Class
	switch c := q.Get("class"); c {
	case "":
	case string(gate.ClassHeavy), string(gate.ClassLight):
		class = gate.Class(c)
	default:
		writeErr(w, http.StatusBadRequest, "bad_class", "class is heavy or light")
		return
	}
	var since time.Time
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_since", "since is an RFC 3339 time")
			return
		}
		since = t
	}
	limit := defaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "bad_limit", "limit is a positive number")
			return
		}
		limit = min(n, maxLimit)
	}
	recs, skipped, err := readDecisions(m.gateDir(), class, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, DecisionsReply{Records: recs, Skipped: skipped})
}

// readDecisions reads decisions.jsonl and decisions.jsonl.1 (the older) and returns up to limit records, newest first, that are of
// class (empty: both) and not before since (zero: all), with the number of lines that did not parse. No gate lock is taken: a
// record is appended whole, one line each. The current file is opened first, then the older one: a rotation between the two opens
// makes the older name point at the file already open as the current one (told by os.SameFile and read once), where the other order
// would lose the file that was just rotated. A file that exists and cannot be read is an error, not an empty history.
func readDecisions(dir string, class gate.Class, since time.Time, limit int) ([]gate.Decision, int, error) {
	var files []*os.File // the newest first
	for _, name := range []string{"decisions.jsonl", "decisions.jsonl.1"} {
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		defer f.Close()
		if len(files) > 0 {
			a, errA := files[0].Stat()
			b, errB := f.Stat()
			if errA == nil && errB == nil && os.SameFile(a, b) {
				continue
			}
		}
		files = append(files, f)
	}
	out := make([]gate.Decision, 0, min(limit, 64))
	skipped := 0
	for i := 0; i < len(files) && len(out) < limit; i++ {
		lines, bad, err := readLines(files[i])
		if err != nil {
			return nil, 0, err
		}
		skipped += bad
		for j := len(lines) - 1; j >= 0 && len(out) < limit; j-- {
			var d gate.Decision
			if err := json.Unmarshal(lines[j], &d); err != nil || d.Time.IsZero() {
				skipped++
				continue
			}
			if class != "" && d.Class != class {
				continue
			}
			if !since.IsZero() && d.Time.Before(since) {
				continue
			}
			out = append(out, d)
		}
	}
	return out, skipped, nil
}

// readLines is the non-empty lines of f in file order; bad counts the lines too long to hold.
func readLines(f *os.File) (lines [][]byte, bad int, err error) {
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := br.ReadSlice('\n')
		switch {
		case err == bufio.ErrBufferFull: // longer than the buffer (a record is about a kilobyte): read on to its end, and drop it
			bad++
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
		case len(line) > 1 || (len(line) == 1 && line[0] != '\n'):
			lines = append(lines, append([]byte(nil), line...))
		}
		if err == io.EOF {
			return lines, bad, nil
		}
		if err != nil && err != bufio.ErrBufferFull {
			return nil, 0, err
		}
	}
}
