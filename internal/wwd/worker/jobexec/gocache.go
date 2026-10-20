package jobexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	// defaultGoCacheMaxMB bounds one Go version's build cache: over it, and with no job using it, it is emptied before the next job.
	defaultGoCacheMaxMB = 10240
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
	now      func() time.Time
	pruned   map[string]bool // the cache directories made or seen by this process, so that dropping the old ones is done once each
	oversize bool            // the last look found the cache over twice its bound
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
	ver, err := e.toolVersion(ctx, "go")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(ver + "\x00" + runtime.GOOS + "/" + runtime.GOARCH))
	root := filepath.Join(e.cfg.DepsDir, "gocache")
	dir := filepath.Join(root, hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("jobexec: the build cache %s: %v", dir, err)
		return ""
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
	e.goJobs.Store(base, &goCacheUse{dir: dir, lock: lock})
	return dir
}

// boundGoCache keeps the cache in dir within its bound, at most once in goCacheLook: with no job using it (the exclusive lock is free) it is
// moved aside and made again, and removed in the background; with jobs using it, a cache over twice its bound is not given (false). The
// lock is left unlocked.
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
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		big, err := storeTooBig(ctx, dir, max)
		if err != nil || !big {
			return true
		}
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
	if big, err := storeTooBig(ctx, dir, 2*max); err == nil && big {
		s.oversize = true
		return false
	}
	return true
}

// pruneGoCaches removes the caches of older Go versions beyond the newest goCacheKeep (by modification time of the directory), each only
// when no job holds it. It looks once per cache directory per process.
func (e *Exec) pruneGoCaches(root, current string) {
	s := &e.goCache
	s.mu.Lock()
	if s.pruned == nil {
		s.pruned = map[string]bool{}
	}
	if s.pruned[current] {
		s.mu.Unlock()
		return
	}
	s.pruned[current] = true
	s.mu.Unlock()
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
			_ = os.Remove(p + ".lock")
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
