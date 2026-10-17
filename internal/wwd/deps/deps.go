// Package deps is the dependency cache of a worker (WWD spec §5 "Workspace"): the installed dependencies of a workspace (node_modules,
// vendor) are kept under a key made of the lockfiles' contents, and a workspace whose lockfiles are the same gets a copy-on-write clone
// of them (APFS clonefile on macOS, a reflink where the file system has one) instead of an install. The newest Keep entries are kept.
package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// dirOf is the directory of installed dependencies that goes with a lockfile, next to it.
var dirOf = map[string]string{
	"pnpm-lock.yaml":    "node_modules",
	"package-lock.json": "node_modules",
	"yarn.lock":         "node_modules",
	"composer.lock":     "vendor",
}

// Spec is what a workspace needs: the lockfiles (paths in the workspace, from the manifest's `deps`) and how to install when the cache has
// no entry for them. Install runs in the workspace (its directory is the argument) and is the caller's: it knows the package manager.
type Spec struct {
	Lockfiles []string
	// Fingerprint says how the dependencies are made: the install command and flags, the package manager's version, the Node (or PHP)
	// version whose ABI native modules are built for. It is part of the key, so the same lockfile installed another way is another
	// entry, and it is required: a cache that cannot tell installs apart would serve the wrong one.
	Fingerprint string
	Install     func(ctx context.Context, ws string) error
}

// Result says what Prepare did.
type Result struct {
	Key string // the cache key
	Hit bool   // the dependencies came from the cache, nothing was installed
}

// Cache is a directory of entries, one per key. Safe for several processes: an install holds the entry's lock exclusively (the second
// one waits and then finds the entry), a clone out of an entry holds it shared, and a prune only takes an entry nobody holds.
type Cache struct {
	Dir  string
	Keep int // entries kept (2 when not set)

	pollLock time.Duration // how often a waiting lock is tried again (a variable so that a test is quick)
}

// NewCache is a Cache in dir, made when it is not there.
func NewCache(dir string, keep int) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{Dir: dir, Keep: keep, pollLock: 100 * time.Millisecond}
	c.sweep()
	return c, nil
}

// testHook, when a test sets it, is called between a check and the use that follows it, with the name of the place.
var testHook func(stage string)

// plan reads the lockfiles of the workspace and returns the key and the directories (relative to the workspace) that go with them.
func plan(ws string, spec Spec) (key string, dirs []string, err error) {
	if len(spec.Lockfiles) == 0 {
		return "", nil, errors.New("deps: no lockfile")
	}
	if spec.Fingerprint == "" {
		return "", nil, errors.New("deps: no fingerprint of the install")
	}
	files := append([]string(nil), spec.Lockfiles...)
	sort.Strings(files)
	root, err := os.OpenRoot(ws)
	if err != nil {
		return "", nil, fmt.Errorf("deps: %w", err)
	}
	defer root.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%s/%s\n", runtime.GOOS, runtime.GOARCH) // an install made on one platform is not for another
	fmt.Fprintf(h, "fingerprint\x00%d\x00%s\x00", len(spec.Fingerprint), spec.Fingerprint)
	seen := map[string]bool{}
	for _, f := range files {
		d, ok := dirOf[path.Base(f)]
		if !ok || !filepath.IsLocal(f) || path.IsAbs(f) || strings.Contains(f, `\`) {
			return "", nil, fmt.Errorf("deps: %q is not a lockfile this cache knows", f)
		}
		if err := plainPath(ws, f); err != nil {
			return "", nil, err
		}
		if testHook != nil {
			testHook("plan-read")
		}
		b, err := root.ReadFile(f) // through the root: a directory turned into a link after the check does not lead out
		if err != nil {
			return "", nil, fmt.Errorf("deps: %w", err)
		}
		dir := path.Join(path.Dir(f), d)
		if err := plainPath(ws, dir); err != nil {
			return "", nil, err
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(b))
		h.Write(b)
	}
	sort.Strings(dirs)
	return hex.EncodeToString(h.Sum(nil))[:24], dirs, nil
}

// Prepare puts the dependencies of the workspace in place: cloned from the cache when it has the entry, else installed (once, however
// many workspaces ask at the same time) and then kept.
func (c *Cache) Prepare(ctx context.Context, ws string, spec Spec) (Result, error) {
	key, dirs, err := plan(ws, spec)
	if err != nil {
		return Result{}, err
	}
	entry := filepath.Join(c.Dir, key)
	if hit, err := c.cloneOut(ctx, key, entry, ws, dirs); err != nil || hit {
		return Result{Key: key, Hit: hit}, err
	}
	if spec.Install == nil {
		return Result{}, errors.New("deps: no entry in the cache and no way to install")
	}
	lock, err := c.lock(ctx, key, syscall.LOCK_EX)
	if err != nil {
		return Result{}, err
	}
	defer lock.Close()
	if _, err := os.Stat(entry); err == nil {
		if entryWhole(entry, dirs) { // another worker made it while this one waited
			lock.Close()
			hit, err := c.cloneOut(ctx, key, entry, ws, dirs)
			if err == nil && !hit {
				err = errors.New("deps: the entry went away")
			}
			return Result{Key: key, Hit: hit}, err
		}
		c.discard(entry) // something that looks like an entry and is not whole (a removal that was killed midway): installed again
	}
	if err := spec.Install(ctx, ws); err != nil {
		return Result{}, fmt.Errorf("deps: install: %w", err)
	}
	tmp, err := os.MkdirTemp(c.Dir, key+".tmp-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	for _, d := range dirs {
		if st, err := os.Lstat(filepath.Join(ws, d)); err != nil || !st.IsDir() {
			return Result{}, fmt.Errorf("deps: the install did not make %s", d)
		}
		if err := clone(ctx, filepath.Join(ws, d), filepath.Join(tmp, d)); err != nil {
			return Result{}, err
		}
		if testHook != nil {
			testHook("copied-in")
		}
		if err := plainPath(ws, d); err != nil { // the path was swapped for a link while cp ran: what was copied may be from outside
			return Result{}, err
		}
	}
	if err := os.Rename(tmp, entry); err != nil { // the entry appears whole or not at all
		return Result{}, err
	}
	c.prune(key)
	c.sweep()
	return Result{Key: key}, nil
}

// cloneOut clones the entry's directories into the workspace, under a shared lock. hit is false when there is no entry.
func (c *Cache) cloneOut(ctx context.Context, key, entry, ws string, dirs []string) (hit bool, err error) {
	if _, err := os.Stat(entry); err != nil {
		return false, nil
	}
	lock, err := c.lock(ctx, key, syscall.LOCK_SH)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if !entryWhole(entry, dirs) { // pruned between the check and the lock, or not whole
		return false, nil
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if testHook != nil {
		testHook("clone-out")
	}
	for _, d := range dirs {
		// the removal and the making of the parent go through the root, which does not follow a link out of the workspace
		if err := root.RemoveAll(d); err != nil {
			return false, err
		}
		if dir := path.Dir(d); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return false, err
			}
		}
		if err := clone(ctx, filepath.Join(entry, d), filepath.Join(ws, d)); err != nil {
			_ = root.RemoveAll(d)
			return false, err
		}
		if testHook != nil {
			testHook("copied-out")
		}
		if err := plainPath(ws, d); err != nil { // cp cannot be given a root: a path swapped for a link while it ran is found out here
			_ = root.RemoveAll(d)
			return false, err
		}
	}
	now := time.Now()
	_ = os.Chtimes(entry, now, now) // the entry was used: it is the newest
	return true, nil
}

// lock takes the lock file of a key (how: LOCK_SH or LOCK_EX), waiting for it, until the context ends.
func (c *Cache) lock(ctx context.Context, key string, how int) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(c.Dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(c.pollLock):
		}
	}
}

// tryLock takes the exclusive lock of a key if it is free right now.
func (c *Cache) tryLock(key string) (*os.File, bool) {
	f, err := os.OpenFile(filepath.Join(c.Dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return f, true
}

// prune removes the entries past the newest Keep, except keep (the one just made); an entry a clone is reading from is waited for.
func (c *Cache) prune(keep string) {
	n := c.Keep
	if n <= 0 {
		n = 2
	}
	des, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type ent struct {
		key string
		mod time.Time
	}
	var es []ent
	for _, de := range des {
		if !de.IsDir() || strings.Contains(de.Name(), ".") {
			continue // .lock files and .tmp- directories are not entries
		}
		if info, err := de.Info(); err == nil {
			es = append(es, ent{de.Name(), info.ModTime()})
		}
	}
	sort.Slice(es, func(i, j int) bool { return es[i].mod.After(es[j].mod) })
	for i, e := range es {
		if i < n || e.key == keep {
			continue
		}
		lock, err := c.lock(context.Background(), e.key, syscall.LOCK_EX) // a clone out of it holds the lock shared: wait for it
		if err != nil {
			continue
		}
		c.discard(filepath.Join(c.Dir, e.key))
		lock.Close() // the lock file stays: one process may have it open and the next opens it by name, and they must find the same file
	}
}

// discard takes an entry out of the cache: it is renamed to a name that is not an entry's first (a rename is whole or not at all), and
// then removed; a removal that fails or is killed leaves a tombstone, which sweep removes later, never something that looks like an entry.
// The caller holds the entry's lock exclusively.
func (c *Cache) discard(entry string) {
	gone := fmt.Sprintf("%s.gone-%d", entry, time.Now().UnixNano())
	if err := os.Rename(entry, gone); err != nil {
		return
	}
	_ = removeAll(gone)
}

// removeAll is os.RemoveAll; a variable so that a test can make a removal fail (a process killed midway is a removal that stops).
var removeAll = os.RemoveAll

// entryWhole says that the entry has every directory a use of it needs.
func entryWhole(entry string, dirs []string) bool {
	for _, d := range dirs {
		if st, err := os.Lstat(filepath.Join(entry, d)); err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

// tmpMaxAge is how old a temporary directory of an install or a tombstone of an eviction must be before sweep removes it: a live
// install in another process has a young one. (A variable so that a test can use it.)
var tmpMaxAge = 6 * time.Hour

// sweep removes what a crash left: temporary directories (<key>.tmp-*) and tombstones (<key>.gone-*) older than tmpMaxAge.
func (c *Cache) sweep() {
	des, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	for _, de := range des {
		n := de.Name()
		if !de.IsDir() || !(strings.Contains(n, ".tmp-") || strings.Contains(n, ".gone-")) {
			continue
		}
		info, err := de.Info()
		if err != nil || time.Since(info.ModTime()) <= tmpMaxAge {
			continue
		}
		// a live install or clone holds its key's lock the whole time, whatever the directory's time says: only a free key is swept
		key, _, _ := strings.Cut(n, ".")
		lock, ok := c.tryLock(key)
		if !ok {
			continue
		}
		_ = removeAll(filepath.Join(c.Dir, n))
		lock.Close()
	}
}

// clone copies the tree src to dst (which is not there) by copy-on-write where the file system can, keeping symlinks as links.
func clone(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	args := []string{"-a", "--reflink=auto", src, dst}
	if runtime.GOOS == "darwin" {
		args = []string{"-c", "-R", src, dst} // clonefile(2), a plain copy where it cannot
	}
	cmd := execstat.CommandContext(ctx, "wwd.deps-clone", "cp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deps: cp %s: %w: %s", src, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// plainPath says that no part of rel (a path in the workspace ws) is a symlink: the manifest is the submitter's, and a lockfile or a
// dependency directory reached through a link could read, copy into, or (a hit removes the destination first) delete what is outside the
// workspace. A part that is not there yet is fine.
func plainPath(ws, rel string) error {
	cur := ws
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("deps: %w", err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("deps: %q goes through a symlink", rel)
		}
	}
	return nil
}
