package watch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The ack log (host-load-control spec §12.3): the watcher marks an event handled with a note, and an event is pending or handled,
// nothing else (there is no skip). The log is a small file beside the records, one JSON line per handled event, first ack wins;
// an event that is not in it is pending.

const (
	ackFile     = "acks.jsonl"
	ackKeep     = 31 * 24 * time.Hour // a little over the 30 days of records
	maxNoteSize = 500
)

// Ack is one handled event: the watcher's note, when, who gave the order (an address) and, for an ack that came from another host,
// which host.
type Ack struct {
	ID       string    `json:"id"`
	Note     string    `json:"note"`
	At       time.Time `json:"at"`
	By       string    `json:"by,omitempty"`
	FromHost string    `json:"from_host,omitempty"`
}

// AckOutcome is what happened to one id of an ack.
type AckOutcome string

const (
	AckHandled  AckOutcome = "handled"   // marked handled now
	AckAlready  AckOutcome = "already"   // it was handled before: the first note stays
	AckNotFound AckOutcome = "not_found" // no such event on this host
)

// AckResult is the outcome for one id.
type AckResult struct {
	ID      string     `json:"id"`
	Outcome AckOutcome `json:"outcome"`
}

// ValidateNote says whether note can be a handled note: one line of valid text, not blank, at most 500 bytes.
func ValidateNote(note string) error {
	switch {
	case strings.TrimSpace(note) == "":
		return errors.New("the note is required")
	case len(note) > maxNoteSize:
		return fmt.Errorf("the note is %d bytes; at most %d", len(note), maxNoteSize)
	case !utf8.ValidString(note):
		return errors.New("the note is not valid UTF-8")
	}
	for _, r := range note {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return errors.New("the note must be one line, without control characters")
		}
	}
	return nil
}

// AckStore is the ack log of one host.
type AckStore struct {
	mu   sync.Mutex
	dir  string
	now  func() time.Time
	acks map[string]Ack
}

// OpenAcks reads the ack log in dir. A line that is not an ack is skipped; a file that cannot be read is an error and the store is
// empty (the daemon goes on: every event reads as pending, which is the safe side).
func OpenAcks(dir string, now func() time.Time) (*AckStore, error) {
	s := &AckStore{dir: dir, now: now, acks: map[string]Ack{}}
	acks, err := ReadAcks(dir)
	if err != nil {
		return s, err
	}
	s.acks = acks
	return s, nil
}

// ReadAcks reads the ack log in dir without a store (the CLI reads it with the daemon down). A missing file is no acks.
func ReadAcks(dir string) (map[string]Ack, error) {
	out := map[string]Ack{}
	if dir == "" {
		return out, nil
	}
	f, err := os.Open(filepath.Join(dir, ackFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var a Ack
		if json.Unmarshal(sc.Bytes(), &a) != nil || a.ID == "" {
			continue
		}
		if _, dup := out[a.ID]; !dup { // the first ack of an event stands
			out[a.ID] = a
		}
	}
	return out, sc.Err()
}

// Get is the ack of an event.
func (s *AckStore) Get(id string) (Ack, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.acks[id]
	return a, ok
}

// Handled says whether the event was acked.
func (s *AckStore) Handled(id string) bool { _, ok := s.Get(id); return ok }

// All is a copy of the acks.
func (s *AckStore) All() map[string]Ack {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Ack, len(s.acks))
	for k, v := range s.acks {
		out[k] = v
	}
	return out
}

// Ack marks ids handled with the note, who and from-host in a (its ID and At are set here). known says whether an id is an event of
// this host. An id that was handled keeps its first note; an unknown one is reported and not recorded. The new acks are written in
// one append, and when that fails nothing is acked, in the file or in memory.
func (s *AckStore) Ack(ids []string, known func(id string) bool, a Ack) ([]AckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.At = s.now()
	res := make([]AckResult, 0, len(ids))
	var fresh []Ack
	pending := map[string]bool{}
	for _, id := range ids {
		switch {
		case s.acks[id].ID != "" || pending[id]:
			res = append(res, AckResult{ID: id, Outcome: AckAlready})
		case !known(id):
			res = append(res, AckResult{ID: id, Outcome: AckNotFound})
		default:
			n := a
			n.ID = id
			fresh = append(fresh, n)
			pending[id] = true
			res = append(res, AckResult{ID: id, Outcome: AckHandled})
		}
	}
	if len(fresh) == 0 {
		return res, nil
	}
	if s.dir == "" { // no data dir: kept in memory only
		for _, n := range fresh {
			s.acks[n.ID] = n
		}
		return res, nil
	}
	// The whole file is made again and put in place in one step (a temp file and a rename): a write that fails leaves the old file, so
	// a batch is in the file whole or not at all. The file holds a month of acks at most.
	all := make([]Ack, 0, len(s.acks)+len(fresh))
	for _, n := range s.acks {
		all = append(all, n)
	}
	all = append(all, fresh...)
	if err := s.writeAll(all); err != nil {
		return nil, err
	}
	for _, n := range fresh {
		s.acks[n.ID] = n
	}
	return res, nil
}

// writeAll puts acks in the file, oldest first.
func (s *AckStore) writeAll(acks []Ack) error {
	sort.SliceStable(acks, func(i, j int) bool {
		if acks[i].At.Equal(acks[j].At) {
			return acks[i].ID < acks[j].ID
		}
		return acks[i].At.Before(acks[j].At)
	})
	var b strings.Builder
	for _, a := range acks {
		line, err := json.Marshal(a)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	_, err := writeAtomic(filepath.Join(s.dir, ackFile), []byte(b.String()))
	return err
}

// Prune forgets the acks older than ackKeep (the events they were about are out of the log) and rewrites the file.
func (s *AckStore) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-ackKeep)
	kept := map[string]Ack{}
	var list []Ack
	for id, a := range s.acks {
		if !a.At.Before(cut) {
			kept[id] = a
			list = append(list, a)
		}
	}
	if len(kept) == len(s.acks) || s.dir == "" {
		return
	}
	if err := s.writeAll(list); err != nil {
		return // the old file stays whole; the next start tries again
	}
	s.acks = kept
}
