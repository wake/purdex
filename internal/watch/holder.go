package watch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HolderState is what this host holds of the watcher (spec §12.3): a session awake here, one resting here (resumable), or none.
type HolderState string

const (
	HolderNone    HolderState = "none"
	HolderAwake   HolderState = "awake"
	HolderResting HolderState = "resting"
)

// Holder is the content of holder.json: the watcher session this host holds, whether it is awake or resting, and when it last
// had a turn and a delivery (the rest countdown of patrol mode counts from the later of the two).
type Holder struct {
	State        HolderState `json:"state"`
	SessionID    string      `json:"session_id,omitempty"`
	Since        time.Time   `json:"since,omitempty"` // when it entered this state
	LastTurn     time.Time   `json:"last_turn,omitempty"`
	LastDelivery time.Time   `json:"last_delivery,omitempty"`
}

// valid says whether h is a state a file may hold: a watcher state has a session, "none" has none.
func (h Holder) valid() bool {
	switch h.State {
	case HolderNone:
		return h.SessionID == ""
	case HolderAwake, HolderResting:
		return h.SessionID != ""
	}
	return false
}

// HolderStore is holder.json (~/.config/pdx/watch/holder.json), written atomically. A file that cannot be read as a holder is
// set aside as ".corrupt" and the host reads as holding none, which is safe: the next discovery finds or starts a watcher.
type HolderStore struct {
	mu   sync.Mutex
	path string
	cur  Holder
}

// OpenHolder reads the file at path (a missing file is "none"). The error, with a usable store, says what was set aside.
func OpenHolder(path string) (*HolderStore, error) {
	s := &HolderStore{path: path, cur: Holder{State: HolderNone}}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		var h Holder
		if jerr := json.Unmarshal(b, &h); jerr != nil || !h.valid() {
			_ = os.Rename(path, path+".corrupt")
			if jerr == nil {
				jerr = errors.New("not a valid holder")
			}
			return s, errors.New("watch holder " + path + ": " + jerr.Error() + " (set aside as .corrupt)")
		}
		s.cur = h
	case !os.IsNotExist(err):
		return s, err
	}
	return s, nil
}

// Get is the holder now.
func (s *HolderStore) Get() Holder {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Update changes the holder with fn and writes it; a result that is not a valid holder, or a write that fails, leaves it as it
// was.
func (s *HolderStore) Update(fn func(*Holder)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	fn(&next)
	if !next.valid() {
		return errors.New("watch holder: the new state is not valid")
	}
	if next == s.cur {
		return nil
	}
	b, err := json.MarshalIndent(next, "", " ")
	if err != nil {
		return err
	}
	if s.path != "" {
		renamed, err := writeAtomic(s.path, b)
		if renamed {
			s.cur = next // the new file is what a restart reads: memory follows it even when its durability could not be confirmed
		}
		return err
	}
	s.cur = next
	return nil
}

// writeAtomic puts data in path through a temp file, an fsync and a rename (and an fsync of the directory). renamed says the new
// content is in place, whatever the error: a failed fsync of the directory comes after the rename.
func writeAtomic(path string, data []byte) (renamed bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".holder-*.tmp")
	if err != nil {
		return false, err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return false, werr
	}
	return true, syncDir(dir)
}

// NewMemoryHolder is a store that keeps nothing on disk (a daemon without a data dir).
func NewMemoryHolder() *HolderStore { return &HolderStore{cur: Holder{State: HolderNone}} }
