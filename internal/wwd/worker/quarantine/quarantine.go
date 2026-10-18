// Package quarantine keeps jobs whose sandbox unit could not be proven stopped (run.Result.Err) across daemon restarts, and
// releases them only after the unit is verified gone. The runner leaves such a job's workspace and lock hold in place; this
// package writes a marker file for it, puts the hold back after a restart (Restore), and lets go of the keys and the directory
// only when the unit checker proves the unit is gone (Sweep) or a human says so (Clear).
package quarantine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// Entry is one quarantined job.
type Entry struct {
	Owner   string // Ended.Source + "/" + Ended.Job.ID (owner registered in locks.Table)
	JobID   string
	Origin  string
	ClaimID string
	Dir     string   // job directory <WorkRoot>/<hash>/ = filepath.Dir(Ended.Root)
	Keys    []string // Ended.Job.Locks
	Unit    string   // Linux sandbox unit "wwd-"+JobID; "" on macOS (no sandbox)
	Since   time.Time
}

// UnitChecker reports whether a sandbox unit is provably gone (absent, or inactive with proof). When it cannot tell, it returns
// (false, err).
type UnitChecker interface {
	Gone(ctx context.Context, unit string) (gone bool, err error)
}

// Option tunes Open without changing its positional signature.
type Option func(*Store)

// WithWorkRoot sets the job work root. Directories are removed only when they are a direct, non-symlink child of it; without it
// nothing is ever removed (fail closed) and no hold is released by Sweep.
func WithWorkRoot(root string) Option { return func(s *Store) { s.workRoot = root } }

// Store persists quarantine markers and their lock holds.
type Store struct {
	dir      string
	workRoot string
	locks    *locks.Table
	checker  UnitChecker
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]Entry // by Owner
}

// Open prepares the marker directory dir and loads the markers in it. A marker that cannot be read, or whose file name does not
// match its owner, is logged and left alone (a human looks at it); it never becomes an entry.
func Open(dir string, t *locks.Table, c UnitChecker, now func() time.Time, opts ...Option) (*Store, error) {
	if dir == "" || t == nil || c == nil || now == nil {
		return nil, errors.New("quarantine: Open needs a directory, a lock table, a unit checker and a clock")
	}
	s := &Store{dir: dir, locks: t, checker: c, now: now, entries: map[string]Entry{}}
	for _, o := range opts {
		o(s)
	}
	if s.workRoot != "" {
		s.workRoot = filepath.Clean(s.workRoot)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("quarantine: %w", err)
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("quarantine: %w", err)
	}
	for _, de := range names {
		name := de.Name()
		switch {
		case strings.Contains(name, ".json.tmp-"):
			os.Remove(filepath.Join(dir, name)) // a write that was cut off: the rename never happened
		case strings.HasSuffix(name, ".json"):
			e, err := readMarker(filepath.Join(dir, name))
			if err != nil {
				log.Printf("quarantine: ignoring marker %s: %v", name, err)
				continue
			}
			s.entries[e.Owner] = e
		}
	}
	return s, nil
}

func markerName(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:16]) + ".json"
}

func readMarker(path string) (Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{}, err
	}
	if err := validate(e); err != nil {
		return Entry{}, err
	}
	if filepath.Base(path) != markerName(e.Owner) {
		return Entry{}, errors.New("file name does not match the owner")
	}
	return e, nil
}

func validate(e Entry) error {
	switch {
	case e.Owner == "":
		return errors.New("empty owner")
	case e.JobID == "":
		return errors.New("empty job id")
	case e.Dir == "":
		return errors.New("empty directory")
	}
	return nil
}

func (e Entry) clone() Entry {
	e.Keys = append([]string(nil), e.Keys...)
	return e
}

// writeMarker writes the marker through a temporary file, fsync and rename, so a crash leaves either the old file or the whole
// new one, never half of it.
func (s *Store) writeMarker(e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	final := filepath.Join(s.dir, markerName(e.Owner))
	f, err := os.CreateTemp(s.dir, markerName(e.Owner)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(s.dir); err == nil { // make the rename itself durable
		d.Sync()
		d.Close()
	}
	return nil
}

func (s *Store) removeMarker(owner string) error {
	err := os.Remove(filepath.Join(s.dir, markerName(owner)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Add writes the marker atomically. It does not touch the hold: the runner keeps it.
func (s *Store) Add(e Entry) error {
	if err := validate(e); err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}
	e = e.clone()
	if e.Since.IsZero() {
		e.Since = s.now()
	}
	if err := s.writeMarker(e); err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}
	s.mu.Lock()
	s.entries[e.Owner] = e
	s.mu.Unlock()
	return nil
}

// Restore puts the hold of every entry back after a restart. A key another owner already holds is logged and the marker stays:
// the entry is still quarantined, and Sweep releases whatever it holds when the unit is gone.
func (s *Store) Restore() error {
	for _, e := range s.snapshot() {
		_, err := s.locks.Acquire(e.Owner, e.Keys, nil)
		switch {
		case err == nil, errors.Is(err, locks.ErrAlreadyHeld):
		default:
			log.Printf("quarantine: cannot restore the hold of %s: %v", e.Owner, err)
		}
	}
	return nil
}

func (s *Store) snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Owner < out[j].Owner })
	return out
}

// Held lists the entries, oldest owner name first. The slice and the keys in it are copies.
func (s *Store) Held() []Entry { return s.snapshot() }

// Quarantined reports whether dir is the directory of an entry, so the generic leftover sweep skips it.
func (s *Store) Quarantined(dir string) bool {
	dir = filepath.Clean(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if filepath.Clean(e.Dir) == dir {
			return true
		}
	}
	return false
}

// safeDir checks that dir has the shape of a job directory: clean, a direct child of the work root, and not a symlink. A
// directory that is already gone passes (there is nothing to remove).
func (s *Store) safeDir(dir string) error {
	if s.workRoot == "" {
		return errors.New("no work root configured")
	}
	if dir == "" || filepath.Clean(dir) != dir || !filepath.IsAbs(dir) {
		return fmt.Errorf("directory %q is not a clean absolute path", dir)
	}
	if filepath.Dir(dir) != s.workRoot || filepath.Base(dir) == "." {
		return fmt.Errorf("directory %q is not a direct child of the work root", dir)
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%q is a symlink or not a directory", dir)
	}
	return nil
}

// drop removes the entry's marker and forgets it, then releases its hold. The marker goes first: if it cannot be removed the
// entry and its hold stay, and the next Sweep tries again.
func (s *Store) drop(e Entry) error {
	if err := s.removeMarker(e.Owner); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.entries, e.Owner)
	s.mu.Unlock()
	s.locks.ReleaseOwner(e.Owner)
	return nil
}

// Sweep clears every entry whose unit the checker proves gone: the directory is removed (only when it passes safeDir), then the
// marker, then the hold. An entry without a unit, a checker error, an unsafe directory or a missing work root keeps everything.
func (s *Store) Sweep(ctx context.Context) (cleared int, err error) {
	var errs []error
	for _, e := range s.snapshot() {
		if ctx.Err() != nil {
			return cleared, ctx.Err()
		}
		if e.Unit == "" {
			continue
		}
		gone, cerr := s.checker.Gone(ctx, e.Unit)
		if cerr != nil || !gone {
			if cerr != nil {
				log.Printf("quarantine: cannot tell whether %s is gone, keeping %s: %v", e.Unit, e.Owner, cerr)
			}
			continue
		}
		if derr := s.safeDir(e.Dir); derr != nil {
			log.Printf("quarantine: refusing to clear %s: %v", e.Owner, derr)
			continue
		}
		if rerr := os.RemoveAll(e.Dir); rerr != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", e.Dir, rerr))
			continue
		}
		if derr := s.drop(e); derr != nil {
			errs = append(errs, derr)
			continue
		}
		cleared++
	}
	return cleared, errors.Join(errs...)
}

// Clear is the human release: the hold and the marker go for each named job. Its directory is removed too when it passes safeDir,
// and otherwise left for the leftover sweep. A job that is not in the store is reported, after the others have been handled.
func (s *Store) Clear(jobID ...string) error {
	byJob := map[string][]Entry{}
	for _, e := range s.snapshot() {
		byJob[e.JobID] = append(byJob[e.JobID], e)
	}
	var errs []error
	for _, id := range jobID {
		es := byJob[id]
		if len(es) == 0 {
			errs = append(errs, fmt.Errorf("quarantine: no entry for job %q", id))
			continue
		}
		for _, e := range es {
			if s.safeDir(e.Dir) == nil {
				if err := os.RemoveAll(e.Dir); err != nil {
					errs = append(errs, fmt.Errorf("remove %s: %w", e.Dir, err))
					continue
				}
			}
			if err := s.drop(e); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
