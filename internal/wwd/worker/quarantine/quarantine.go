// Package quarantine keeps jobs whose sandbox unit could not be proven
// stopped (run.Result.Err) across daemon restarts, and releases them only
// after the unit is verified gone.
package quarantine

import (
	"context"
	"errors"
	"time"

	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// ErrNotImplemented is returned by the stub methods until the real
// implementation lands.
var ErrNotImplemented = errors.New("quarantine: not implemented")

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

// UnitChecker reports whether a sandbox unit is provably gone (absent, or
// inactive with proof). When it cannot tell, it returns (false, err).
type UnitChecker interface {
	Gone(ctx context.Context, unit string) (gone bool, err error)
}

// Option tunes Open without changing its positional signature.
type Option func(*Store)

// WithWorkRoot sets the job work root; Sweep only removes Dir when it is a
// direct, non-symlink child of this directory. Without it Sweep refuses.
func WithWorkRoot(root string) Option { return func(s *Store) { s.workRoot = root } }

// Store persists quarantine markers and their lock holds.
type Store struct {
	dir      string
	workRoot string
	locks    *locks.Table
	checker  UnitChecker
	now      func() time.Time
}

// Open prepares the marker directory dir.
func Open(dir string, t *locks.Table, c UnitChecker, now func() time.Time, opts ...Option) (*Store, error) {
	s := &Store{dir: dir, locks: t, checker: c, now: now}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Add writes the marker atomically; it does not touch the hold (the runner
// already keeps it).
func (s *Store) Add(e Entry) error { return ErrNotImplemented }

// Restore re-acquires holds for all markers at startup.
func (s *Store) Restore() error { return ErrNotImplemented }

// Sweep clears entries whose unit is verified gone.
func (s *Store) Sweep(ctx context.Context) (cleared int, err error) { return 0, ErrNotImplemented }

// Clear manually releases the named jobs.
func (s *Store) Clear(jobID ...string) error { return ErrNotImplemented }

// Held lists the current entries.
func (s *Store) Held() []Entry { return nil }
