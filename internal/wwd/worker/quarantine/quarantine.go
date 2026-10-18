// Package quarantine keeps jobs whose sandbox unit could not be proven stopped (run.Result.Err) across daemon restarts, and
// releases them only after the unit is verified gone. The runner leaves such a job's workspace and lock hold in place; this
// package writes a marker file for it, puts the hold back after a restart (Restore), and lets go of the keys and the directory
// only when the unit checker proves the unit is gone (Sweep) or a human says so (Clear).
//
// # The life of an entry
//
// An entry (one per owner = source/job id) is in one of three states:
//
//	none         no entry. A job that never ran here, or whose entry was retired.
//	running      Begin wrote its marker before the sandbox unit was started, and this process is running the command. The lock hold is the
//	             runner's. The unit may not exist yet, so "no unit" says nothing.
//	quarantined  its run ended (or this process died and another loaded the marker) and the unit is not proven gone. The keys are held
//	             here (Restore puts them back after a restart) until Sweep proves the unit gone or a human clears it.
//
// What each operation does, in each state. Every transition is made under the store's one lock, in the one place named in the right
// column; an operation not in the table is refused and changes nothing.
//
//	operation        none               running                          quarantined                      where
//	Begin            -> running         refused (ErrExists)              refused (ErrExists)              Begin
//	Add              -> quarantined     -> quarantined (marker rewritten) -> quarantined (rewritten)      Add
//	Settle           no-op              -> quarantined (memory only)     no-op                            Settle
//	Discard          no-op              -> none (marker, entry; not hold, not dir)  refused (ErrNotRunning) Discard
//	Sweep, unit gone n/a                skipped (never asked)            -> none (dir, marker, hold)      retire
//	Sweep, otherwise n/a                skipped                          unchanged                        retire
//	Clear            refused (no entry) refused (ErrRunning)             -> none (dir if safe, marker, hold) retire
//	Restore          n/a                skipped                          hold taken again (error if a key is held by another)  Restore
//	Open (load)      -                  -                                every readable marker           Open
//
// Invariants (the tests check each after every cell of the table):
//
//	(a) an entry in the store has its marker on disk; a quarantined entry has its hold, or Restore/Sweep keeps reporting that it has not;
//	(b) an entry that has been retired (Sweep, Clear) or discarded is not brought back by an operation that looked at it before: every
//	    operation acts on the entry it saw (its generation) and on nothing else;
//	(c) a running entry is never swept, cleared, or asked about its unit;
//	(d) the hold is released only when the unit is proven gone (Sweep) or a human says so (Clear); Discard, Settle and Add never release it.
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

// ErrExists: Begin found an entry for the owner (running or quarantined). A second run of a job that has a marker is refused rather than
// overwriting it: the marker may be the only record of a unit that is still going.
var ErrExists = errors.New("quarantine: an entry for this owner exists")

// ErrNotRunning: Discard found a quarantined entry. Only a run that is going on is discarded; a quarantined record is cleared by proof
// (Sweep) or by a human (Clear), never by a call that was meant for something else.
var ErrNotRunning = errors.New("quarantine: the entry is not a running one")

// ErrRunning: Clear found a running entry.
var ErrRunning = errors.New("quarantine: the job is running, it cannot be cleared")

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
	gen     uint64
	entries map[string]rec // by Owner
}

// rec is an entry with the generation Add gave it: Sweep and Clear work from a snapshot taken without the lock, and a later Add
// of the same owner must not be undone by them.
type rec struct {
	e   Entry
	gen uint64
	// live: the job this entry stands for is running in this process right now (Begin wrote it before the sandbox unit was started and
	// nothing has settled it yet). Sweep leaves a live entry alone: its unit may not exist yet, and "no unit" would be read as "gone".
	// An entry loaded from disk is never live: the process that wrote it is not this one.
	live bool
}

// Open prepares the marker directory dir and loads the markers in it. A marker that cannot be read, or whose file name does not
// match its owner, is logged and left alone (a human looks at it); it never becomes an entry.
func Open(dir string, t *locks.Table, c UnitChecker, now func() time.Time, opts ...Option) (*Store, error) {
	if dir == "" || t == nil || c == nil || now == nil {
		return nil, errors.New("quarantine: Open needs a directory, a lock table, a unit checker and a clock")
	}
	s := &Store{dir: dir, locks: t, checker: c, now: now, entries: map[string]rec{}}
	for _, o := range opts {
		o(s)
	}
	if s.workRoot != "" {
		s.workRoot = filepath.Clean(s.workRoot)
		if real, err := filepath.EvalSymlinks(s.workRoot); err != nil || real != s.workRoot {
			log.Printf("quarantine: work root %q is not a real, clean path (%v): directories will not be removed", s.workRoot, err)
			s.workRoot = ""
		}
	}
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("quarantine: marker directory %q is a symlink", dir)
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
			s.gen++
			s.entries[e.Owner] = rec{e: e, gen: s.gen}
		}
	}
	return s, nil
}

func markerName(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:16]) + ".json"
}

func readMarker(path string) (Entry, error) {
	if fi, err := os.Lstat(path); err != nil {
		return Entry{}, err
	} else if !fi.Mode().IsRegular() {
		return Entry{}, errors.New("not a regular file")
	}
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
	case !strings.HasSuffix(e.Owner, "/"+e.JobID):
		return errors.New("owner does not end in the job id")
	case e.Unit != "" && e.Unit != "wwd-"+e.JobID:
		return errors.New("unit is not the job's own unit")
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

// Add writes the marker atomically and makes the entry quarantined (none, running or quarantined before). It does not touch the hold: the
// runner keeps it. A host with no guard writes its markers with Add when a run has ended without proof; with a guard Settle does the
// state change and nothing needs to be written again.
func (s *Store) Add(e Entry) error { return s.put(e, false, false) }

// Begin writes the marker of a run that is about to start its sandbox unit, before the unit exists, and the entry is running. If this
// process dies while the unit runs, the marker is what gives the job's lock keys back to the next process (Restore) and keeps its
// workspace until the unit is gone (Sweep); a marker written after the run cannot do that. An entry that exists is not overwritten
// (ErrExists). The run ends with Discard (the unit is proven gone) or Settle/Add (it is not).
func (s *Store) Begin(e Entry) error { return s.put(e, true, true) }

// Settle says the run of a running entry is over without its unit being proven gone: the entry becomes quarantined, so that Sweep clears
// it once the unit is gone. Only memory changes (the marker has been on disk since Begin), so it cannot fail. Any other state: nothing.
func (s *Store) Settle(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.entries[owner]; ok && r.live {
		r.live = false
		s.entries[owner] = r
	}
}

// Discard forgets a running entry whose command ended with its unit proven gone: the marker and the entry go. The hold is the runner's
// and the directory the executor's, so neither is touched. No entry: nothing happens, and a marker file that was not loaded (it could not
// be read; kept on purpose for a human) is left where it is. A quarantined entry is refused (ErrNotRunning). If the marker cannot be
// removed the entry becomes quarantined (the run is over), so that Sweep can clear it once the unit is gone.
func (s *Store) Discard(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.entries[owner]
	if !ok {
		return nil
	}
	if !r.live {
		return ErrNotRunning
	}
	r.live = false
	s.entries[owner] = r
	if err := s.removeMarker(owner); err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}
	delete(s.entries, owner)
	return nil
}

// put is Begin (running, refusing an existing entry) and Add (quarantined, replacing it).
func (s *Store) put(e Entry, live, refuseExisting bool) error {
	if err := validate(e); err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}
	e = e.clone()
	if e.Since.IsZero() {
		e.Since = s.now()
	}
	s.mu.Lock() // marker and entry change together: a Sweep or Clear holding the older snapshot must not see one without the other
	defer s.mu.Unlock()
	if _, exists := s.entries[e.Owner]; exists && refuseExisting {
		return fmt.Errorf("%w: %s", ErrExists, e.Owner)
	}
	if err := s.writeMarker(e); err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}
	s.gen++
	s.entries[e.Owner] = rec{e: e, gen: s.gen, live: live}
	return nil
}

// Restore puts the hold of every entry back after a restart. Call it before anything else can take a lock: a key another owner
// already holds is reported in the error (the marker stays, the entry stays quarantined, and Sweep tries again), because until
// the hold is back the entry's other keys are not protected either (Acquire is all or none).
func (s *Store) Restore() error {
	var errs []error
	for _, h := range s.snapshot() {
		if h.live {
			continue // running in this process: the hold is the runner's
		}
		if err := s.reacquire(h.e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reacquire takes the entry's keys unless the owner already holds exactly them.
func (s *Store) reacquire(e Entry) error {
	_, err := s.locks.Acquire(e.Owner, e.Keys, nil)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, locks.ErrAlreadyHeld):
		want := map[string]bool{}
		for _, k := range e.Keys {
			want[k] = true
		}
		have := map[string]bool{}
		for k, owner := range s.locks.Held() {
			if owner == e.Owner {
				have[k] = true
			}
		}
		if len(want) != len(have) {
			return fmt.Errorf("quarantine: %s holds other keys than its marker names", e.Owner)
		}
		for k := range want {
			if !have[k] {
				return fmt.Errorf("quarantine: %s holds other keys than its marker names", e.Owner)
			}
		}
		return nil
	}
	return fmt.Errorf("quarantine: cannot restore the hold of %s: %w", e.Owner, err)
}

type snap struct {
	e    Entry
	gen  uint64
	live bool
}

func (s *Store) snapshot() []snap {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]snap, 0, len(s.entries))
	for _, r := range s.entries {
		out = append(out, snap{e: r.e.clone(), gen: r.gen, live: r.live})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].e.Owner < out[j].e.Owner })
	return out
}

// Held lists the entries, oldest owner name first. The slice and the keys in it are copies.
func (s *Store) Held() []Entry {
	snaps := s.snapshot()
	out := make([]Entry, len(snaps))
	for i, h := range snaps {
		out[i] = h.e
	}
	return out
}

// Quarantined reports whether dir is the directory of an entry, so the generic leftover sweep skips it.
func (s *Store) Quarantined(dir string) bool {
	dir = filepath.Clean(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.entries {
		if filepath.Clean(r.e.Dir) == dir {
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
	if real, err := filepath.EvalSymlinks(dir); err != nil || real != dir {
		return fmt.Errorf("%q does not resolve to itself (%v)", dir, err)
	}
	return nil
}

// drop removes the snapshot's marker and forgets the entry, then releases its hold, all under the store lock and only if the entry
// has not been replaced meanwhile. The marker goes first: if it cannot be removed the entry and its hold stay, and the next Sweep
// tries again.
// retire ends an entry under the store's lock: the entry must still be the one h saw (same generation) and, for Sweep, not running; the
// directory goes (only if it passes safeDir; a human's Clear carries on without removing an unsafe one, Sweep stops), then the marker,
// then the entry and the hold. It is the only place an entry is removed with its hold, so (b), (c) and (d) are decided here.
func (s *Store) retire(h snap, human bool) (retired bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.entries[h.e.Owner]
	if !ok || r.gen != h.gen {
		return false, nil // retired or replaced since it was looked at
	}
	if r.live {
		if human {
			return false, fmt.Errorf("%w: %s", ErrRunning, h.e.JobID)
		}
		return false, nil
	}
	if derr := s.safeDir(h.e.Dir); derr == nil {
		if rerr := os.RemoveAll(h.e.Dir); rerr != nil {
			return false, fmt.Errorf("remove %s: %w", h.e.Dir, rerr)
		}
	} else if !human {
		log.Printf("quarantine: refusing to clear %s: %v", h.e.Owner, derr)
		return false, nil
	}
	if err := s.removeMarker(h.e.Owner); err != nil {
		return false, err
	}
	delete(s.entries, h.e.Owner)
	s.locks.ReleaseOwner(h.e.Owner)
	return true, nil
}

// Sweep clears every quarantined entry whose unit the checker proves gone (see retire). A running entry is not looked at. An entry
// without a unit, a checker error, an unsafe directory or a missing work root keeps everything.
func (s *Store) Sweep(ctx context.Context) (cleared int, err error) {
	var errs []error
	for _, h := range s.snapshot() {
		if ctx.Err() != nil {
			return cleared, ctx.Err()
		}
		if h.live {
			continue
		}
		if err := s.reacquire(h.e); err != nil { // a conflict that has since gone away
			log.Print(err)
		}
		if h.e.Unit == "" {
			continue
		}
		gone, cerr := s.checker.Gone(ctx, h.e.Unit)
		if cerr != nil || !gone {
			if cerr != nil {
				log.Printf("quarantine: cannot tell whether %s is gone, keeping %s: %v", h.e.Unit, h.e.Owner, cerr)
			}
			continue
		}
		ok, rerr := s.retire(h, false)
		if rerr != nil {
			errs = append(errs, rerr)
			continue
		}
		if ok {
			cleared++
		}
	}
	return cleared, errors.Join(errs...)
}

// Clear is the human release of quarantined entries: the hold, the marker and, when it passes safeDir, the directory go for each named
// job. A job whose command is running is refused, and a job that is not in the store is reported, after the others have been handled.
func (s *Store) Clear(jobID ...string) error {
	byJob := map[string][]snap{}
	for _, h := range s.snapshot() {
		byJob[h.e.JobID] = append(byJob[h.e.JobID], h)
	}
	var errs []error
	for _, id := range jobID {
		es := byJob[id]
		if len(es) == 0 {
			errs = append(errs, fmt.Errorf("quarantine: no entry for job %q", id))
			continue
		}
		for _, h := range es {
			if _, err := s.retire(h, true); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
