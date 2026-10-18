package jobnotify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxKept bounds the ids remembered; a job older than the newest maxKept ended ones has long been told or never will be.
const maxKept = 5000

// state is wwd-notified.json: the jobs already told, and the time from which a reconnect looks for jobs it missed.
type state struct {
	Notified []string  `json:"notified"`
	Since    time.Time `json:"since"`
}

// store is the notifier's memory (spec §10: once per job, also across a daemon restart). The notifier is its only writer.
type store struct {
	mu   sync.Mutex
	path string
	st   state
	set  map[string]bool
}

// openStore reads path; a missing or unreadable file starts a fresh memory whose catch-up window begins at now, so the first run
// does not tell a whole history.
func openStore(path string, now time.Time, logf func(string, ...any)) *store {
	s := &store{path: path, set: map[string]bool{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		logf("jobnotify: %s cannot be read, starting with an empty memory: %v", path, err)
	default:
		if err := json.Unmarshal(b, &s.st); err != nil {
			s.st = state{}
			// keep the bad file for whoever looks, and start from an empty memory instead of failing the daemon
			logf("jobnotify: %s is damaged (%v), moved to %s.bad; starting with an empty memory", path, err, path)
			_ = os.Rename(path, path+".bad")
		}
	}
	if s.st.Since.IsZero() {
		s.st.Since = now
	}
	for _, id := range s.st.Notified {
		s.set[id] = true
	}
	return s
}

func (s *store) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[id]
}

func (s *store) since() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Since
}

// mark records id as told and writes the file. The file is written after
// the notice was handed over, so a crash in between tells a job twice at worst, never zero times.
func (s *store) mark(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set[id] {
		s.set[id] = true
		s.st.Notified = append(s.st.Notified, id)
		if n := len(s.st.Notified); n > maxKept {
			for _, old := range s.st.Notified[:n-maxKept] {
				delete(s.set, old)
			}
			s.st.Notified = append([]string(nil), s.st.Notified[n-maxKept:]...)
		}
	}
	return s.save()
}

// advance moves the catch-up window to at (never back) once a catch-up has looked at every state.
func (s *store) advance(at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !at.After(s.st.Since) {
		return nil
	}
	s.st.Since = at
	return s.save()
}

func (s *store) save() error {
	b, err := json.Marshal(s.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".wwd-notified-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
