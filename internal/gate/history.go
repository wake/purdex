package gate

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

// History reads decisions.jsonl and decisions.jsonl.1 and returns the matching records, newest first by their time (a record is
// written when its command ends, so the file order is the order of the ends, not of the starts). A line that does not parse is
// skipped. No gate lock is taken: a record is appended whole, one line each. The current file is opened first, then the older one:
// a rotation between the two opens makes the older name point at the file already open as the current one (told by os.SameFile and
// read once), where the other order would lose the file that was just rotated.
func History(dir string, f HistoryFilter) ([]Decision, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var files []*os.File
	defer func() {
		for _, fh := range files {
			fh.Close()
		}
	}()
	for _, name := range []string{decisionsFile, decisionsFile + ".1"} {
		fh, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(files) > 0 {
			a, errA := files[0].Stat()
			b, errB := fh.Stat()
			if errA == nil && errB == nil && os.SameFile(a, b) {
				fh.Close()
				continue
			}
		}
		files = append(files, fh)
	}
	var all []Decision
	for _, fh := range files {
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
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, nil
}
