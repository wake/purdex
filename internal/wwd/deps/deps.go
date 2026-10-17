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
	Install   func(ctx context.Context, ws string) error
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
	return &Cache{Dir: dir, Keep: keep, pollLock: 100 * time.Millisecond}, nil
}

// plan reads the lockfiles of the workspace and returns the key and the directories (relative to the workspace) that go with them.
func plan(ws string, spec Spec) (key string, dirs []string, err error) {
	if len(spec.Lockfiles) == 0 {
		return "", nil, errors.New("deps: no lockfile")
	}
	files := append([]string(nil), spec.Lockfiles...)
	sort.Strings(files)
	h := sha256.New()
	fmt.Fprintf(h, "%s/%s\n", runtime.GOOS, runtime.GOARCH) // an install made on one platform is not for another
	seen := map[string]bool{}
	for _, f := range files {
		d, ok := dirOf[path.Base(f)]
		if !ok || !filepath.IsLocal(f) || path.IsAbs(f) || strings.Contains(f, `\`) {
			return "", nil, fmt.Errorf("deps: %q is not a lockfile this cache knows", f)
		}
		b, err := os.ReadFile(filepath.Join(ws, f))
		if err != nil {
			return "", nil, fmt.Errorf("deps: %w", err)
		}
		dir := path.Join(path.Dir(f), d)
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
	if _, err := os.Stat(entry); err == nil { // another worker made it while this one waited
		lock.Close()
		hit, err := c.cloneOut(ctx, key, entry, ws, dirs)
		if err == nil && !hit {
			err = errors.New("deps: the entry went away")
		}
		return Result{Key: key, Hit: hit}, err
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
	}
	if err := os.Rename(tmp, entry); err != nil { // the entry appears whole or not at all
		return Result{}, err
	}
	c.prune(key)
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
	if _, err := os.Stat(entry); err != nil { // pruned between the check and the lock
		return false, nil
	}
	for _, d := range dirs {
		dst := filepath.Join(ws, d)
		if err := os.RemoveAll(dst); err != nil {
			return false, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, err
		}
		if err := clone(ctx, filepath.Join(entry, d), dst); err != nil {
			_ = os.RemoveAll(dst)
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
		_ = os.RemoveAll(filepath.Join(c.Dir, e.key))
		lock.Close()
		_ = os.Remove(filepath.Join(c.Dir, e.key+".lock"))
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
