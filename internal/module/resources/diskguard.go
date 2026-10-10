package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The disk guard (#2470). Before a heavy lease is granted, the volume that holds the Go build cache is looked at. Below
// diskLowWater it first trims the cache: the entries (files in the cache's two-hex-digit directories) that nobody has
// touched for cacheTrimAge, which is what Go's own trim does with a 5 day horizon, so a compile that is running is not
// affected. A trim runs at most once per cacheTrimEvery, never under stateMu, and a request waits for the trim it started
// (bounded by trimBudget) but never for another request's. A volume still under diskHardFloor afterwards is granted all the
// same, with a warning in the log and in the lease answer: refusing would not make room.

const (
	diskLowWater  = int64(15) << 30
	diskHardFloor = int64(3) << 30
	cacheTrimAge  = 2 * time.Hour
	// cacheTrimEvery is the least time between two trims; trimBudget bounds one walk.
	cacheTrimEvery = 10 * time.Minute
	trimBudget     = 90 * time.Second
	// cacheDirMemo is how long the resolved cache directory is remembered (`go env` forks).
	cacheDirMemo = 10 * time.Minute
	// maxGrantWarnings bounds the warnings kept with grants.
	maxGrantWarnings = 512
)

// diskGuardedKinds are the lease kinds that build or test: they are what fills the cache.
var diskGuardedKinds = map[string]bool{"test-full": true, "test-pkg": true, "build": true, "lint-full": true}

// diskGuard is the guard's state, under its own mutex (never stateMu).
type diskGuard struct {
	mu       sync.Mutex
	lastTrim time.Time
	trimming bool   // a walk is running: guarded grants wait for it (passOnce) and the walk ends with a pass of its own
	warn     string // the standing warning of the last look at the disk ("" = none)
	// grants is the warning each guarded lease was granted with (only the non-empty ones): the answer to a poll or a replay
	// is the fact about the grant, not the disk of the moment. In memory, bounded.
	grants     map[string]string
	grantOrder []string // the ids in grants, oldest first: what the bound drops
}

// enableDiskGuard installs the real free-space reader and cache locator. New does not: a test module has the guard off
// (diskFree nil) until it injects its own.
func (m *Module) enableDiskGuard() {
	if m.diskFree == nil {
		m.diskFree = freeBytesOf
	}
	if m.goCacheDir == nil {
		m.goCacheDir = memoCacheDir(resolveGoCacheDir, cacheDirMemo)
	}
}

// diskPreflight runs before the locked part of a pass. wait says the caller is a request that is about to be answered: it
// waits for the trim it starts, so its grant is made after it. The sampler and the sweeper pass false and never wait.
func (m *Module) diskPreflight(ctx context.Context, wait bool) {
	if m.diskFree == nil || m.goCacheDir == nil || m.store == nil {
		return
	}
	waiting, err := m.store.Waiting()
	if err != nil {
		return
	}
	heavy := false
	for _, w := range waiting {
		if diskGuardedKinds[w.Kind] {
			heavy = true
			break
		}
	}
	if !heavy {
		return
	}
	dir := m.goCacheDir()
	if dir == "" {
		return
	}
	free, err := m.diskFree(dir)
	if err != nil {
		return
	}
	if free < diskLowWater {
		started, done := m.startTrim(dir, free)
		if started && wait {
			select {
			case <-done:
			case <-ctx.Done():
			}
			if f, err := m.diskFree(dir); err == nil {
				free = f
			}
		} else if started {
			return // the free space is not known until the trim ends: the warning is left as it was
		}
	}
	m.setDiskWarning(dir, free)
}

// startTrim starts a trim in its own goroutine unless one started within cacheTrimEvery (longer than a walk's budget, so that also means none is running). started says this
// call started it; done closes when it ended.
func (m *Module) startTrim(dir string, free int64) (started bool, done <-chan struct{}) {
	now := m.now()
	m.disk.mu.Lock()
	if !m.disk.lastTrim.IsZero() && now.Sub(m.disk.lastTrim) < cacheTrimEvery { // also what keeps two walks apart: cacheTrimEvery is longer than trimBudget
		m.disk.mu.Unlock()
		return false, nil
	}
	m.disk.lastTrim, m.disk.trimming = now, true
	m.disk.mu.Unlock()
	// The wait group is added to under startMu, the lock markStopped takes before Stop / Close wait on it: a trim is never
	// added behind a Wait that has begun.
	m.startMu.Lock()
	if m.stopped {
		m.startMu.Unlock()
		m.disk.mu.Lock()
		m.disk.trimming = false
		m.disk.mu.Unlock()
		return false, nil
	}
	m.wg.Add(1)
	m.startMu.Unlock()
	ch := make(chan struct{})
	go func() {
		defer m.wg.Done()
		defer close(ch)
		defer func() {
			m.disk.mu.Lock()
			m.disk.trimming = false
			m.disk.mu.Unlock()
			// The guarded leases that waited for this walk are granted now, with the disk judged after it.
			if m.runCtx.Err() == nil {
				m.admissionPass(m.runCtx, "")
			}
		}()
		if m.trimHook != nil {
			m.trimHook()
		}
		ctx, cancel := context.WithTimeout(m.runCtx, trimBudget)
		defer cancel()
		freed, err := m.trimGoCache(ctx, dir, time.Now().Add(-cacheTrimAge)) // file mtimes are wall-clock, not the module's clock
		switch {
		case err != nil:
			m.logf("[resources] disk: %d MiB free under the %d GiB mark; trimming the Go build cache %s stopped after %d MiB: %v",
				free>>20, diskLowWater>>30, dir, freed>>20, err)
		default:
			m.logf("[resources] disk: %d MiB free under the %d GiB mark; trimmed the Go build cache %s, %d MiB freed",
				free>>20, diskLowWater>>30, dir, freed>>20)
		}
	}()
	return true, ch
}

// setDiskWarning records (and logs, once per standing condition) that the volume is under the hard floor, or clears it.
func (m *Module) setDiskWarning(dir string, free int64) {
	m.disk.mu.Lock()
	defer m.disk.mu.Unlock()
	if free >= diskHardFloor {
		m.disk.warn = ""
		return
	}

	msg := fmt.Sprintf("disk: low on disk, %d MiB free on the volume of the Go build cache %s (hard floor %d GiB); builds and tests may fail with no space left on device",
		free>>20, dir, diskHardFloor>>30)
	if m.disk.warn == "" {
		m.logf("[resources] %s", msg)
	}
	m.disk.warn = msg
}

// diskWarning is the standing warning, "" when there is none.
func (m *Module) diskWarning() string {
	m.disk.mu.Lock()
	defer m.disk.mu.Unlock()
	return m.disk.warn
}

// diskHolding says a trim is running: a guarded lease is not granted until it ends.
func (m *Module) diskHolding() bool {
	m.disk.mu.Lock()
	defer m.disk.mu.Unlock()
	return m.disk.trimming
}

// recordGrantWarning keeps, with a guarded lease just granted, the warning of the look at the disk that preceded the grant.
func (m *Module) recordGrantWarning(id, kind string) {
	if !diskGuardedKinds[kind] {
		return
	}
	m.disk.mu.Lock()
	defer m.disk.mu.Unlock()
	if m.disk.warn == "" {
		return
	}
	if m.disk.grants == nil {
		m.disk.grants = map[string]string{}
	}
	if _, had := m.disk.grants[id]; !had {
		m.disk.grantOrder = append(m.disk.grantOrder, id)
	}
	m.disk.grants[id] = m.disk.warn
	for len(m.disk.grantOrder) > maxGrantWarnings {
		delete(m.disk.grants, m.disk.grantOrder[0])
		m.disk.grantOrder = m.disk.grantOrder[1:]
	}
}

// warnFor is the warning a lease answer carries: the one it was granted with, if any.
func (m *Module) warnFor(row leaseRow) string {
	m.disk.mu.Lock()
	defer m.disk.mu.Unlock()
	return m.disk.grants[row.ID]
}

// hexDir says whether name is one of the cache's entry directories: exactly two lower-case hex digits.
func hexDir(name string) bool {
	if len(name) != 2 {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// trimGoCache deletes the regular files in dir's two-hex-digit subdirectories whose mtime is before cutoff, and returns the
// bytes it freed. Nothing else is touched: all access goes through an os.Root on dir, which refuses to leave it (a symlink
// to elsewhere is not followed, `..` does not work), a subdirectory is used only when Lstat says it is a real directory, an
// entry only when Lstat says it is a regular file, and Remove unlinks the name it is given without following it. A directory
// that is empty of cache or absent is not an error; a bad dir is. ctx ends the walk (the bytes freed so far are returned with
// the error).
func (m *Module) trimGoCache(ctx context.Context, dir string, cutoff time.Time) (int64, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return 0, fmt.Errorf("go build cache %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if home, err := os.UserHomeDir(); dir == string(filepath.Separator) || (err == nil && dir == filepath.Clean(home)) {
		return 0, fmt.Errorf("go build cache %q is not a cache directory", dir)
	}
	// The directory itself is not followed if it is a symlink (OpenRoot would): a GOCACHE pointing through a link to somewhere
	// else does not make that somewhere else a cache.
	if fi, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	} else if !fi.IsDir() {
		return 0, fmt.Errorf("go build cache %q is not a plain directory (a symlink, a file): not trimmed", dir)
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if !isGoCache(root) {
		return 0, fmt.Errorf("%s does not look like a Go build cache (no README written by go): not trimmed", dir)
	}
	top, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, d := range top {
		if err := ctx.Err(); err != nil {
			return freed, err
		}
		if !hexDir(d.Name()) {
			continue
		}
		if fi, err := root.Lstat(d.Name()); err != nil || !fi.IsDir() {
			continue // gone, a file, or a symlink: never followed
		}
		entries, err := fs.ReadDir(root.FS(), d.Name())
		if err != nil {
			continue
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return freed, err
			}
			name := d.Name() + "/" + e.Name()
			fi, err := root.Lstat(name)
			if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
				continue
			}
			if err := root.Remove(name); err == nil {
				freed += fi.Size()
			}
		}
	}
	return freed, nil
}

// goCacheReadmeHead is the first line of the README `go` writes into every build cache it creates (cmd/go/internal/cache).
const goCacheReadmeHead = "This directory holds cached build artifacts from the Go build system."

// isGoCache says whether the directory root was opened on is a Go build cache: it has a README that is a regular file (not a
// symlink or a directory) and starts with the line `go` writes. A directory that merely has two-hex-digit subdirectories (a
// project, a data folder, a GOCACHE pointed at the wrong place) is not one, and nothing in it is deleted.
func isGoCache(root *os.Root) bool {
	fi, err := root.Lstat("README")
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := root.Open("README")
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(goCacheReadmeHead))
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == goCacheReadmeHead
}

// resolveGoCacheDir is `go env GOCACHE`, or the platform's cache directory plus go-build when go cannot be run or says
// nothing usable ("off", a relative path).
func resolveGoCacheDir() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" && d != "off" && filepath.IsAbs(d) {
			return d
		}
	}
	if c, err := os.UserCacheDir(); err == nil {
		return filepath.Join(c, "go-build")
	}
	return ""
}

// memoCacheDir remembers fn's answer for ttl.
func memoCacheDir(fn func() string, ttl time.Duration) func() string {
	var (
		mu  sync.Mutex
		at  time.Time
		dir string
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if at.IsZero() || time.Since(at) > ttl {
			dir, at = fn(), time.Now()
		}
		return dir
	}
}

// freeBytesOf is the space available to this user on the volume that holds path, looked up at the nearest parent that
// exists (a cache that is not there yet is on its parent's volume).
func freeBytesOf(path string) (int64, error) {
	p := filepath.Clean(path)
	for {
		if _, err := os.Stat(p); err == nil {
			return statFreeBytes(p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, fmt.Errorf("no existing parent of %s", path)
		}
		p = parent
	}
}
