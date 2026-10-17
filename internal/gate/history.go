package gate

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// HistoryFilter picks the records of commands that were offered to an assist host: ran there, were taken back, or failed to start
// there. Host "" takes every host; a zero Since takes all; Limit 0 takes 50.
type HistoryFilter struct {
	Host  string
	Since time.Time
	Limit int
}

// SentOut says whether a record is of a command that was offered to an assist host.
func (d Decision) SentOut() bool { return d.Tried != "" || d.Host != "" }

// History reads decisions.jsonl.1 and decisions.jsonl and returns the matching records, newest first. A line that does not parse
// is skipped. No gate lock is taken: a record is appended whole, one line each.
func History(dir string, f HistoryFilter) ([]Decision, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var all []Decision
	for _, name := range []string{decisionsFile + ".1", decisionsFile} {
		fh, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var d Decision
			if json.Unmarshal(sc.Bytes(), &d) != nil || d.Time.IsZero() || !d.SentOut() {
				continue
			}
			if f.Host != "" && d.Host != f.Host && d.Tried != f.Host {
				continue
			}
			if !f.Since.IsZero() && d.Time.Before(f.Since) {
				continue
			}
			all = append(all, d)
		}
		err = sc.Err()
		fh.Close()
		if err != nil {
			return nil, err
		}
	}
	out := make([]Decision, 0, min(len(all), f.Limit))
	for i := len(all) - 1; i >= 0 && len(out) < f.Limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}
