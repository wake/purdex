package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SignatureInfo is what state.json keeps of one command signature.
type SignatureInfo struct {
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
}

type stateFile struct {
	Version    int                      `json:"version"`
	Signatures map[string]SignatureInfo `json:"signatures"`
	Orphans    map[string]time.Time     `json:"orphans"`
	Reported   map[string]time.Time     `json:"reported"` // kind + " " + holder key → when it was marked
}

// There is one writer: the daemon's watcher (a restart re-execs the same process, so two never overlap). A second process that
// flushed from an older snapshot would overwrite the first's changes; the CLI reads the event log only and never opens the state.
//
// StateStore is the persistent Memory (~/.config/pdx/watch/state.json): the signatures seen, when each orphaned slot was first
// seen orphaned, and the E3/E4 events already made. Changes stay in memory and go to the file at Flush (the caller flushes once
// per pass); the write is atomic (a temp file renamed over the old one), so a crash leaves the old or the new file whole.
type StateStore struct {
	mu    sync.Mutex
	path  string
	dirty bool
	file  stateFile
	now   func() time.Time
}

// DefaultDir is the watcher's directory: state.json and the event logs live there.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "pdx", "watch")
}

// OpenState reads the state file at path (a missing file is an empty state). A file that cannot be read as state is renamed to
// "<path>.corrupt" and the error is returned together with a usable empty store: the watcher goes on, and the caller logs it.
func OpenState(path string) (*StateStore, error) {
	s := &StateStore{path: path, now: time.Now}
	s.file = stateFile{Version: 1}
	var err error
	b, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		var f stateFile
		if jerr := json.Unmarshal(b, &f); jerr != nil {
			err = fmt.Errorf("watch state %s: %w (set aside as .corrupt)", path, jerr)
			_ = os.Rename(path, path+".corrupt")
		} else {
			s.file = f
		}
	case !os.IsNotExist(rerr):
		err = fmt.Errorf("watch state %s: %w", path, rerr)
	}
	if s.file.Signatures == nil {
		s.file.Signatures = map[string]SignatureInfo{}
	}
	if s.file.Orphans == nil {
		s.file.Orphans = map[string]time.Time{}
	}
	if s.file.Reported == nil {
		s.file.Reported = map[string]time.Time{}
	}
	s.file.Version = 1
	return s, err
}

// Signatures is a copy of the signatures seen (the log and tests).
func (s *StateStore) Signatures() map[string]SignatureInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]SignatureInfo, len(s.file.Signatures))
	for k, v := range s.file.Signatures {
		out[k] = v
	}
	return out
}

// Sighting implements Memory.
func (s *StateStore) Sighting(sig string, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.file.Signatures[sig]
	if !ok {
		v.First = at
	}
	v.Last = at
	v.Count++
	s.file.Signatures[sig] = v
	s.dirty = true
	return !ok
}

// OrphanSince implements Memory.
func (s *StateStore) OrphanSince(key string, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.file.Orphans[key]; ok {
		return t
	}
	s.file.Orphans[key] = now
	s.dirty = true
	return now
}

// KeepOrphans implements Memory.
func (s *StateStore) KeepOrphans(keys map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.file.Orphans {
		if !keys[k] {
			delete(s.file.Orphans, k)
			s.dirty = true
		}
	}
}

// Reported implements Memory.
func (s *StateStore) Reported(kind Kind, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.file.Reported[string(kind)+" "+key]
	return ok
}

// MarkReported implements Memory.
func (s *StateStore) MarkReported(kind Kind, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(kind) + " " + key
	if _, ok := s.file.Reported[k]; ok {
		return
	}
	s.file.Reported[k] = s.now()
	s.dirty = true
}

// KeepReported implements Memory.
func (s *StateStore) KeepReported(keys map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.file.Reported {
		if i := strings.IndexByte(k, ' '); i < 0 || !keys[k[i+1:]] {
			delete(s.file.Reported, k)
			s.dirty = true
		}
	}
}

// Flush writes the state if it changed since the last Flush.
func (s *StateStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	b, err := json.MarshalIndent(s.file, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync() // the content is on disk before the name points at it
	}
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), s.path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := syncDir(filepath.Dir(s.path)); err != nil { // the rename itself must survive a power cut; dirty stays, the next pass writes again
		return err
	}
	s.dirty = false
	return nil
}

// syncDir is a variable so a test can fail it.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	serr := d.Sync()
	if cerr := d.Close(); serr == nil {
		serr = cerr
	}
	return serr
}
