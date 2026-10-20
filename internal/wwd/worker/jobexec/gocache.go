package jobexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
)

// The shared build cache of the go tasks (#3669). A job's HOME is its workspace, so Go's own build cache would be made new and thrown away
// with every job, and every job would compile the standard library and the whole tree from nothing. A task that declares go.sum (the same
// declaration that gives it its module cache, deps_gomod.go) is given one GOCACHE per Go version on the host instead, under the deps
// directory, which every such job reads and writes. Go's build cache is made for that: it is content addressed (an entry is named by the hash
// of what produced it, the toolchain and the flags included), several go processes at once share one, and an entry is written whole through a
// temporary file and a rename, so a job that is killed leaves no half entry. It is not shared between hosts.
//
// What the shared cache is not: a trust boundary. Go reads an entry by name and by size, not by a hash of its content, so a job could in
// principle write an entry another one would use; the jobs of one worker are one team's code, and the dependency cache clones one job's
// installed tree into the next just the same. A cache that is doubted is removed (the directory under <deps_dir>/gocache): the worker makes it
// again and the price is one cold build.
//
// The test results in the cache are not used: GOFLAGS gets -count=1 (depsJobEnv), because a "(cached)" ok knows only the files and variables
// the test read, not tmux, a socket or an executable, and the reuse of whole jobs is the result cache's.

const (
	// defaultGoCacheMaxMB bounds one Go version's build cache: over it, it is trimmed from the oldest entry (#3748), and a cache that cannot
	// be trimmed is emptied before the next job when no job uses it.
	defaultGoCacheMaxMB = 10240
	// defaultGoCachePruneHours is how long an entry may go unused before it is removed. Go touches an entry's file time when it uses it (at
	// most once an hour), so an entry whose time is older than this was not asked for since; Go's own trim waits 5 days.
	defaultGoCachePruneHours = 24
	// goCacheTrimPct is where a trim of an over-bound cache stops: a share of the bound, so that the next job does not trim again.
	goCacheTrimPct = 80
	// goCachePruneEvery is the least time between two age passes (Go's file times are an hour coarse).
	goCachePruneEvery = time.Hour
	// goCacheKeep is how many Go versions' caches are kept; an older one is removed when a newer one is made.
	goCacheKeep = 2
	// goCacheLook is the least time between two looks at the size of a cache (a walk of its entries is not free).
	goCacheLook = 2 * time.Minute
)

// goCacheUse is the hold a job has on the cache it was given: the shared lock on <dir>.lock from Prepare to Cleanup.
type goCacheUse struct {
	dir  string
	lock *os.File
}

func (u *goCacheUse) release() {
	if u.lock != nil {
		_ = u.lock.Close() // the lock goes with the descriptor
	}
}

// goCacheState is what an Exec remembers of the build caches.
type goCacheState struct {
	mu       sync.Mutex
	lookAt   time.Time // when the cache last looked at was looked at
	pruneAt  time.Time // when the last age pass was
	trimming bool      // a trim of an over-bound cache began and has not reached its target (it takes several passes when it is long)
	now      func() time.Time
	oversize bool // the last look found the cache over twice its bound
	// test seams: hook is called at a named step of making a cache; versionOverride is the go version text a test gives instead of asking go
	hook            func(stage string)
	versionOverride string
}

func (s *goCacheState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (e *Exec) goCacheMax() int64 {
	mb := e.cfg.GoCacheMaxMB
	if mb <= 0 {
		mb = defaultGoCacheMaxMB
	}
	return int64(mb) << 20
}

// hasGoSum says whether the task declares go.sum.
func hasGoSum(lockfiles []string) bool {
	for _, lf := range lockfiles {
		if path.Base(lf) == "go.sum" {
			return true
		}
	}
	return false
}

// prepareGoCache gives the job (the directory base) the shared build cache of this host's Go, and holds it for the job. It returns the
// directory, or "" when the job should build in its own workspace as it did before: the worker has no deps directory, the cache cannot be
// made, or it is over twice its bound while others hold it (so that a busy host cannot grow it without end). A failure here is never the
// job's: it only builds cold.
func (e *Exec) prepareGoCache(ctx context.Context, base string) string {
	if e.cfg.DepsDir == "" {
		return ""
	}
	ver := e.goCache.versionOverride // a test's other Go
	if ver == "" {
		var err error
		if ver, err = e.toolVersion(ctx, "go"); err != nil {
			return ""
		}
	}
	sum := sha256.Sum256([]byte(ver + "\x00" + runtime.GOOS + "/" + runtime.GOARCH))
	root := filepath.Join(e.cfg.DepsDir, "gocache")
	dir := filepath.Join(root, hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Printf("jobexec: the build cache %s: %v", root, err)
		return ""
	}
	// One lock for making, dropping and bounding the caches of the host, across the worker processes: a cache that is just made is held (its
	// shared lock) before the lock is let go, so that another worker that makes a newer cache cannot drop it, or empty it, in between.
	meta, err := os.OpenFile(filepath.Join(root, ".meta.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ""
	}
	defer meta.Close()
	if err := syscall.Flock(int(meta.Fd()), syscall.LOCK_EX); err != nil {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("jobexec: the build cache %s: %v", dir, err)
		return ""
	}
	if h := e.goCache.hook; h != nil {
		h("made")
	}
	e.pruneGoCaches(root, dir)
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ""
	}
	if !e.boundGoCache(ctx, dir, lock) {
		lock.Close()
		return ""
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		lock.Close()
		return ""
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() { // a directory that is not there is not one to give to a job (the Linux sandbox would not start)
		lock.Close()
		return ""
	}
	e.goJobs.Store(base, &goCacheUse{dir: dir, lock: lock})
	return dir
}

// goCacheRemoveFile removes one entry of the cache (a test seam).
var goCacheRemoveFile = os.Remove

// goCacheFile is one entry of the cache as the walk saw it.
type goCacheFile struct {
	path string
	mod  time.Time
	size int64
}

// scanGoCache lists the regular files under dir with their sizes and times, and their sum. Go's own bookkeeping at the top (trim.txt, README)
// is not an entry. A link is not followed and a file that goes away while it is read counts as nothing. errOverBound: more entries than
// storeScanLimit (the list is then not complete); any other error is that ctx ended.
func scanGoCache(ctx context.Context, dir string) (files []goCacheFile, total int64, err error) {
	count := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if werr != nil {
			return nil
		}
		if count++; count > storeScanLimit {
			return errOverBound
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if rel, rerr := filepath.Rel(dir, p); rerr == nil && (rel == "trim.txt" || rel == "README") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		files = append(files, goCacheFile{p, info.ModTime(), info.Size()})
		total += info.Size()
		return nil
	})
	return files, total, err
}

// goCacheMaxRemovals bounds the files one pass removes, so that a pass in the preparation of a job costs about 0.4 s at most (10,000 files
// took 1.7 to 2.4 s on mlab); what is left is for the next pass, two minutes on. A variable for the tests.
var goCacheMaxRemovals = 2000

// removeGoCacheFiles removes the listed entries one by one, in order, while the sum *total is over target (a negative target removes all of
// them) and *budget lasts (each try uses one), and takes what was freed off *total. A file that is already gone counts as removed (another
// worker, or Go's own trim, was first); any other failure leaves it, and it stays in the sum. It returns whether it went through the list or
// stopped at the target (false: it stopped for the budget).
func removeGoCacheFiles(files []goCacheFile, total *int64, target int64, budget *int) bool {
	for _, f := range files {
		if *total <= target {
			return true
		}
		if *budget <= 0 {
			return false
		}
		*budget--
		if err := goCacheRemoveFile(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			*total -= f.size
		}
	}
	return true
}

// boundGoCache keeps the cache in dir within its bound, at most once in goCacheLook, in one walk of the entries. An age pass (at most once
// an hour) removes the entries unused for longer than the limit; over the bound, the oldest entries go until it is at 80% of it. Neither needs the
// cache to be free: Go's own trim removes files while other go processes run, and a file that went away is a miss. Only a cache that could not be
// trimmed under its bound is emptied, and that needs the exclusive lock (moved aside and made again, removed in the background); while jobs
// hold it, it is not given to a new job when over twice the bound (false). The lock is left unlocked.
func (e *Exec) boundGoCache(ctx context.Context, dir string, lock *os.File) bool {
	s := &e.goCache
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if !s.lookAt.IsZero() && now.Sub(s.lookAt) < goCacheLook {
		return !s.oversize
	}
	s.lookAt = now
	max := e.goCacheMax()
	s.oversize = false
	files, total, err := scanGoCache(ctx, dir)
	tooMany := errors.Is(err, errOverBound) // too many entries to list: not trimmed, treated as over the bound
	if err != nil && !tooMany {
		return true // ctx ended
	}
	budget := goCacheMaxRemovals
	if !tooMany {
		if s.pruneAt.IsZero() || now.Sub(s.pruneAt) >= goCachePruneEvery {
			cutoff := now.Add(-e.goCachePrune())
			var aged, kept []goCacheFile
			for _, f := range files {
				if f.mod.Before(cutoff) {
					aged = append(aged, f)
				} else {
					kept = append(kept, f)
				}
			}
			if removeGoCacheFiles(aged, &total, -1, &budget) {
				s.pruneAt = now // the pass went through; one that stopped for its budget is taken up again by the next look
			}
			files = kept // (an aged file that would not go is not tried again below: it stays in the size, so the bound still sees it)
		}
		target := max / 100 * goCacheTrimPct
		if total > max {
			s.trimming = true
		}
		if s.trimming && budget > 0 {
			sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
			removeGoCacheFiles(files, &total, target, &budget)
			if total <= target {
				s.trimming = false
			}
		}
	}
	// Over the bound for want of budget is not a cache that cannot be trimmed: the next pass goes on. A cache is emptied (or refused to a new
	// job) only when the removals that were tried did not bring it under its bound.
	over := tooMany || (total > max && budget > 0)
	if !over {
		return true
	}
	log.Printf("jobexec: the build cache %s could not be trimmed under its bound (%d MB)", dir, max>>20)
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		aside := fmt.Sprintf("%s.old-%d-%d", dir, os.Getpid(), time.Now().UnixNano())
		log.Printf("jobexec: the build cache %s is over its bound (%d MB): emptied", dir, max>>20)
		if err := os.Rename(dir, aside); err != nil {
			return true
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false
		}
		go removeAsideFn(aside)
		return true
	}
	// in use by other jobs: it cannot be emptied now; over twice the bound it is not given to a new job
	if tooMany || total > 2*max {
		s.oversize = true
		return false
	}
	return true
}

func (e *Exec) goCachePrune() time.Duration {
	h := e.cfg.GoCachePruneHours
	if h <= 0 {
		h = defaultGoCachePruneHours
	}
	return time.Duration(h) * time.Hour
}

// pruneGoCaches removes the caches of older Go versions beyond the newest goCacheKeep (by modification time of the directory), each only
// when no job holds it; one that is held is left, and the next job looks again (the directory is a few entries, so it is looked at every time).
func (e *Exec) pruneGoCaches(root, current string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type d struct {
		name string
		mod  time.Time
	}
	var dirs []d
	for _, en := range entries {
		if !en.IsDir() || filepath.Ext(en.Name()) == ".lock" || len(en.Name()) != 16 { // the key is 16 hex characters; ".old-…" and locks are not caches
			continue
		}
		if info, err := en.Info(); err == nil {
			dirs = append(dirs, d{en.Name(), info.ModTime()})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for i, c := range dirs {
		if i < goCacheKeep || filepath.Join(root, c.name) == current {
			continue
		}
		p := filepath.Join(root, c.name)
		lock, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil { // nobody holds it
			aside := fmt.Sprintf("%s.old-%d-%d", p, os.Getpid(), time.Now().UnixNano())
			if os.Rename(p, aside) == nil {
				go removeAsideFn(aside)
			}
			// the lock file stays: a process that already opened it and waits would lock an inode nobody else uses (a split lock)
		}
		lock.Close()
	}
}

// sandboxOf is the sandbox of the job in directory base: the worker's own, and a go job's shared build cache (outside the workspace, which
// is all a sandboxed command may write) let through. The worker's own sandbox is not changed.
func (e *Exec) sandboxOf(base string) *run.Sandbox {
	if v, ok := e.goJobs.Load(base); ok {
		return depsSandbox(e.cfg.Sandbox, v.(*goCacheUse).dir)
	}
	return e.cfg.Sandbox
}

// releaseGoCache lets go of the job's hold on the build cache, if it has one.
func (e *Exec) releaseGoCache(base string) {
	if v, had := e.goJobs.LoadAndDelete(base); had {
		v.(*goCacheUse).release()
	}
}
