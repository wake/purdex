package jobexec

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"syscall"
	"time"
)

// The timed trim of the build caches (#3833). boundGoCache runs when a go job starts, at most once in two minutes and with a budget of 2000
// removals, which is enough for a host that starts many jobs and not for one that starts few and writes a lot with each (a host that mostly lints
// grew past its bound by 1 GB an hour). The worker's beat (the loop's sweepers, once a minute, also while the worker is switched off: the cache
// is on the disk whether or not the worker takes jobs) looks at every cache of the host once in goCacheTickEvery, in the background.
//
// What the tick is not: the job's pass. It never empties a cache (that is a rename of the directory, under the lock of the host's caches, and
// stays with the job that finds a cache it cannot trim), and it takes none of the locks that a starting job waits for except the exclusive one
// of the cache, in chunks.
const (
	// goCacheTickEvery is the least time between two looks of the tick at the caches.
	goCacheTickEvery = 15 * time.Minute
	// goCacheTickRetry is the wait before the tick looks again at a cache it could not bring to its target for want of the exclusive lock (#3879):
	// the lock is free between two jobs, and a tick that waits a quarter of an hour for the next one gets it about once an hour on a host
	// whose lint and test jobs follow each other. The beat is a minute, so this is the next beat.
	goCacheTickRetry = time.Minute
)

var (
	// goCacheTickChunk is how many entries the tick removes while it holds the exclusive lock of a cache: the lock is let go after each chunk, so a
	// job that waits for the shared lock is kept waiting about a second at most (10,000 files took 1.7 to 2.4 s on mlab). Variables for the tests.
	goCacheTickChunk = 5000
	// goCacheTickYield is the pause between two chunks, with the lock free.
	goCacheTickYield = 50 * time.Millisecond
	// goCacheTickBetween is called between two chunks, with the lock free (a test seam).
	goCacheTickBetween func()
)

// GoCacheTrimmer is the worker loop's sweeper that trims the build caches on a timer. It runs the trim in the background, so a round of the
// loop (a claim) is not held for the seconds a large trim takes.
type GoCacheTrimmer struct {
	e       *Exec
	running atomic.Bool
}

// GoCacheTrimmer returns the sweeper of this Exec's build caches.
func (e *Exec) GoCacheTrimmer() *GoCacheTrimmer { return &GoCacheTrimmer{e: e} }

// Sweep starts a trim when one is due and none is running. The live keys of the jobs are not needed.
func (t *GoCacheTrimmer) Sweep(ctx context.Context, _ []string) error {
	if t.e.cfg.DepsDir == "" || !t.e.goCacheTickDue() || !t.running.CompareAndSwap(false, true) {
		return nil
	}
	go func() {
		defer t.running.Store(false)
		t.e.tickGoCaches(ctx)
	}()
	return nil
}

// goCacheTickDue says whether the caches are to be looked at now.
func (e *Exec) goCacheTickDue() bool {
	s := &e.goCache
	s.mu.Lock()
	defer s.mu.Unlock()
	el := s.clock().Sub(s.tickAt)
	return s.tickAt.IsZero() || el < 0 || el >= goCacheTickEvery
}

// tickGoCaches looks at every cache under the deps directory (one per Go version), if the interval has passed, and trims each that is over its
// bound.
func (e *Exec) tickGoCaches(ctx context.Context) {
	if e.cfg.DepsDir == "" {
		return
	}
	s := &e.goCache
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	if !e.goCacheTickDue() {
		return
	}
	s.mu.Lock()
	now := s.clock()
	s.tickAt = now
	s.mu.Unlock()
	root := filepath.Join(e.cfg.DepsDir, "gocache")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	retry := false
	for _, en := range entries {
		if ctx.Err() != nil {
			return
		}
		if !en.IsDir() || filepath.Ext(en.Name()) == ".lock" || len(en.Name()) != 16 { // the key is 16 hex characters; ".old-…" and locks are not caches
			continue
		}
		if e.tickGoCache(ctx, filepath.Join(root, en.Name()), now) {
			retry = true
		}
	}
	if retry { // due again at the next beat instead of after the interval
		s.mu.Lock()
		s.tickAt = now.Add(goCacheTickRetry - goCacheTickEvery)
		s.mu.Unlock()
	}
}

// agePassDue says whether the age pass of the cache in dir is due (at most once an hour, shared with the job's pass), and gives its state.
func (e *Exec) agePassDue(dir string, now time.Time) (*goCacheDir, bool) {
	s := &e.goCache
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirs == nil {
		s.dirs = map[string]*goCacheDir{}
	}
	st := s.dirs[dir]
	if st == nil {
		st = &goCacheDir{}
		s.dirs[dir] = st
	}
	el := now.Sub(st.pruneAt)
	return st, st.pruneAt.IsZero() || el < 0 || el >= goCachePruneEvery
}

// agePassDone records that an age pass went through: only a pass that did is the time of the last one, which the job's pass shares.
func (e *Exec) agePassDone(st *goCacheDir, now time.Time) {
	e.goCache.mu.Lock()
	st.pruneAt = now
	e.goCache.mu.Unlock()
}

// tickGoCache is the age pass (on the hour) and the trim of one cache. It says whether the cache is still over its target for want of the
// exclusive lock, so that the tick looks again soon (goCacheTickRetry): with the lock any entry may go, so a cache that has it and is still
// over has nothing more to give and is left to the interval.
func (e *Exec) tickGoCache(ctx context.Context, dir string, now time.Time) (retry bool) {
	files, total, err := scanGoCache(ctx, dir)
	if err != nil {
		if errors.Is(err, errOverBound) {
			if st, due := e.agePassDue(dir, now); due && e.pruneAgedWithoutList(ctx, dir, now) {
				e.agePassDone(st, now)
			}
		}
		return false
	}
	before := total
	removed := 0

	st, agePass := e.agePassDue(dir, now)
	if agePass { // as in a job's pass: an entry unused for longer than the limit goes, free or held
		cutoff := now.Add(-e.goCachePrune() - goCacheMtimeStep)
		var kept []goCacheFile
		for _, f := range files {
			if !f.mod.Before(cutoff) {
				kept = append(kept, f)
				continue
			}
			if ctx.Err() != nil {
				return false
			}
			if err := goCacheRemoveFile(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
				total -= f.size
				removed++
			} else {
				kept = append(kept, f)
			}
		}
		files = kept
		e.agePassDone(st, now) // the pass went through (a pass that ctx ended returned above)
	}

	max := e.goCacheMax()
	exclusiveAny, lastShared := false, false
	over := total > max
	target := max / 100 * goCacheTrimPct
	if over {
		var n int
		n, exclusiveAny, lastShared = e.trimInChunks(ctx, dir, files, &total, target, max, now)
		removed += n
	}
	// a cache over its bound is said so every time, also when nothing could go: that is the line the host's keeper looks for
	if removed > 0 || over {
		mode := "held"
		if exclusiveAny {
			mode = "exclusive"
		}
		what := "trimmed"
		if over && removed == 0 {
			what = "over its bound and not trimmed"
		}
		log.Printf("jobexec: the build cache %s %s on the timer: %d entries, %d MB freed, now %d MB (bound %d MB, %s)",
			dir, what, removed, (before-total)>>20, total>>20, max>>20, mode)
	}
	return over && lastShared && total > target && ctx.Err() == nil
}

// trimInChunks removes the oldest entries until the cache is at the target, goCacheTickChunk at a time. While the cache is free (the exclusive
// lock is had without waiting) any entry may go; while a job holds it, only an entry unused for goCacheShareAge (which falls the further the
// cache is over max, to goCacheShareFloor). The lock is let go
// between two chunks, with a pause, so a job that waits for the shared lock gets in; the next chunk takes it again if it can. It returns how many
// entries it removed, whether it held the exclusive lock at any time, and whether its last chunk went without it (a job holds the cache by then,
// whatever an earlier chunk had: that is the case where a look soon may find the lock free).
func (e *Exec) trimInChunks(ctx context.Context, dir string, files []goCacheFile, total *int64, target, max int64, now time.Time) (removed int, exclusiveAny, lastShared bool) {
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		defer lock.Close()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	i := 0
	for i < len(files) && *total > target && ctx.Err() == nil {
		idleCutoff := now.Add(-goCacheShareAge(*total, max)) // as it comes down, the age rises again
		exclusive := err == nil && syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
		exclusiveAny = exclusiveAny || exclusive
		lastShared = !exclusive
		n := 0
		for i < len(files) && *total > target && n < goCacheTickChunk {
			f := files[i]
			if !exclusive && !f.mod.Before(idleCutoff) {
				i = len(files) // oldest first: the rest are newer, and a held cache's
				break
			}
			i++
			n++
			if err := goCacheRemoveFile(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
				*total -= f.size
				removed++
			}
		}
		if exclusive {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}
		if i < len(files) && *total > target {
			if h := goCacheTickBetween; h != nil {
				h()
			}
			select {
			case <-ctx.Done():
				return removed, exclusiveAny, lastShared
			case <-time.After(goCacheTickYield):
			}
		}
	}
	return removed, exclusiveAny, lastShared
}

// pruneAgedWithoutList is the timer's way with a cache of more entries than scanGoCache will list (storeScanLimit): the oldest-first trim needs
// the list, but the age rule does not, so the walk removes the entries unused for longer than the limit as it goes. A host that takes few jobs
// writes nothing while it is idle, so what is left ages out and the cache comes back under the limit of the listing; a job that starts finds a
// cache that is over the bound and empties it when it is free (boundGoCache), which the timer never does. It says whether the walk went through
// (the hour of the age pass counts from one that did, as in the listed case).
func (e *Exec) pruneAgedWithoutList(ctx context.Context, dir string, now time.Time) bool {
	cutoff := now.Add(-e.goCachePrune() - goCacheMtimeStep)
	removed, freed := 0, int64(0)
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if werr != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "fuzz" && p == filepath.Join(dir, "fuzz") {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if rel, rerr := filepath.Rel(dir, p); rerr == nil && (rel == "trim.txt" || rel == "README") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := goCacheRemoveFile(p); err == nil || errors.Is(err, fs.ErrNotExist) {
			removed++
			freed += info.Size()
		}
		return nil
	})
	log.Printf("jobexec: the build cache %s has more entries than can be listed: the timer removed %d entries unused for longer than the limit (%d MB freed), the rest is for the next job that starts", dir, removed, freed>>20)
	return walkErr == nil // a walk that ctx ended is not a pass that went through
}
