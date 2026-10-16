package watch

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/redact"
)

const (
	logPrefix     = "events-"
	logSuffix     = ".jsonl"
	logDayFormat  = "20060102"
	logKeepDays   = 30
	recordArgvMax = 200 // bytes of each word of a recorded command line
)

// Record is one line of the event log (events-YYYYMMDD.jsonl): what the detector found, with the command line scrubbed.
type Record struct {
	ID        string    `json:"id"` // host + "-" + a sortable unique id
	Kind      Kind      `json:"kind"`
	Label     Label     `json:"label,omitempty"`
	Host      string    `json:"host"`
	At        time.Time `json:"at"`
	Signature string    `json:"signature,omitempty"`
	Argv      []string  `json:"argv,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Session   string    `json:"session,omitempty"`
	PeakCores float64   `json:"peak_cores,omitempty"`
	PeakMB    uint64    `json:"peak_mb,omitempty"`
	DurationS float64   `json:"duration_s,omitempty"`
	New       bool      `json:"new,omitempty"`
	Pressure  int       `json:"pressure,omitempty"`
	PageRate  float64   `json:"page_rate,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Slot      string    `json:"slot,omitempty"`
	HolderPID int       `json:"holder_pid,omitempty"`
	Holders   []int     `json:"holders,omitempty"` // E3: the processes that hold the lock
	Handled   bool      `json:"handled"`           // set when a watcher has dealt with it (W3); a new record is never handled
}

// Recorder appends events to the daily log files in dir. Appending needs no daemon: the files are plain JSON lines.
type Recorder struct {
	mu      sync.Mutex
	dir     string
	host    string
	now     func() time.Time
	cleaned string // the day (UTC) of the last cleaning
}

// NewRecorder logs to dir for host.
func NewRecorder(dir, host string) *Recorder {
	return &Recorder{dir: dir, host: host, now: time.Now}
}

// Record writes the event and returns its id. The first record of a day also removes the logs older than 30 days.
func (r *Recorder) Record(ev Event) (string, error) {
	rec, err := r.RecordFull(ev)
	return rec.ID, err
}

// RecordFull is Record that returns the whole line it wrote (the delivery queue takes it from there). On an error the record is
// the zero value unless the line was written and only the close failed.
func (r *Recorder) RecordFull(ev Event) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return Record{}, err
	}
	today := r.now().UTC().Format(logDayFormat)
	if r.cleaned != today {
		r.cleaned = today
		r.clean()
	}
	rec := Record{
		ID: r.host + "-" + newID(r.now()), Kind: ev.Kind, Label: ev.Label, Host: r.host, At: ev.At,
		Signature: ev.Signature, Argv: redact.Argv(ev.Argv, recordArgvMax), Cwd: ev.Cwd, Session: ev.Session,
		PeakCores: ev.PeakCores, PeakMB: ev.PeakMB, DurationS: ev.Duration.Seconds(), New: ev.New,
		Pressure: ev.Pressure, PageRate: ev.PageRate, Detail: ev.Detail, Slot: ev.Slot, HolderPID: ev.HolderPID, Holders: ev.LockPIDs,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return Record{}, err
	}
	f, err := os.OpenFile(filepath.Join(r.dir, logPrefix+ev.At.UTC().Format(logDayFormat)+logSuffix), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return Record{}, werr
	}
	return rec, nil
}

// clean removes the log files whose day is more than 30 days before today.
func (r *Recorder) clean() {
	cut := r.now().UTC().AddDate(0, 0, -logKeepDays).Format(logDayFormat)
	names, _ := logFiles(r.dir) // best effort: a directory that cannot be read is found by the append that follows
	for _, name := range names {
		if day := strings.TrimSuffix(strings.TrimPrefix(name, logPrefix), logSuffix); day < cut {
			_ = os.Remove(filepath.Join(r.dir, name))
		}
	}
}

// logFiles are the event log file names in dir, oldest first. A directory that is not there is no files; one that cannot be read
// is an error.
func logFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, logPrefix) && strings.HasSuffix(n, logSuffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadLog reads the records in dir, oldest first, from since on (zero: all) and of kind (empty: all). A line that is not a record
// (a torn last line after a crash) is skipped; a missing directory is no records.
func ReadLog(dir string, since time.Time, kind Kind) ([]Record, error) {
	recs, _, err := ReadLogDiag(dir, since, kind)
	return recs, err
}

// ReadLogDiag is ReadLog that also says how many lines it skipped because they were not records. One torn line after a crash is
// expected; more than that, or a skipped line in the middle of a file, is damage the caller should show.
func ReadLogDiag(dir string, since time.Time, kind Kind) (recs []Record, skipped int, err error) {
	out := recs
	sinceDay := since.UTC().Format(logDayFormat)
	names, err := logFiles(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, name := range names {
		if !since.IsZero() && strings.TrimSuffix(strings.TrimPrefix(name, logPrefix), logSuffix) < sinceDay {
			continue
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return out, skipped, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var rec Record
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.ID == "" {
				skipped++
				continue
			}
			if (!since.IsZero() && rec.At.Before(since)) || (kind != "" && rec.Kind != kind) {
				continue
			}
			out = append(out, rec)
		}
		serr := sc.Err()
		f.Close()
		if serr != nil {
			return out, skipped, fmt.Errorf("%s: %w", name, serr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, skipped, nil
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newID is a ULID: 48 bits of milliseconds then 80 random bits in Crockford base32, so ids sort by time.
func newID(t time.Time) string {
	var b [26]byte
	ms := uint64(t.UnixMilli())
	for i := 9; i >= 0; i-- {
		b[i] = crockford[ms&31]
		ms >>= 5
	}
	var rnd [16]byte
	_, _ = rand.Read(rnd[:])
	for i := 0; i < 16; i++ {
		b[10+i] = crockford[rnd[i]&31]
	}
	return string(b[:])
}
