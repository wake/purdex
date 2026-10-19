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
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
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
	// Dirs are directories of installed dependencies besides the one next to each lockfile: a workspace member's node_modules, which a
	// monorepo's one lockfile installs under every member. Each is a path in the workspace whose last element is node_modules or vendor, and
	// each must exist once the install is done. They are part of the key.
	Dirs []string
	// Inputs are workspace files, besides the lockfiles, that an install reads and so decide what it makes: the package manifests, the package
	// manager's configuration, a workspace's definition. Each is hashed into the key (a file that is not there counts as absent), so that two
	// workspaces with one lockfile and different manifests or scripts do not share an entry.
	Inputs []string
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
	extra := append([]string(nil), spec.Dirs...)
	sort.Strings(extra)
	for _, d := range extra {
		if b := path.Base(d); (b != "node_modules" && b != "vendor") || !filepath.IsLocal(d) || path.IsAbs(d) || strings.Contains(d, `\`) || path.Clean(d) != d {
			return "", nil, fmt.Errorf("deps: %q is not a directory of dependencies this cache knows", d)
		}
		if err := plainPath(ws, d); err != nil {
			return "", nil, err
		}
		fmt.Fprintf(h, "dir\x00%d\x00%s\x00", len(d), d)
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	inputs := append([]string(nil), spec.Inputs...)
	sort.Strings(inputs)
	for _, f := range inputs {
		if !filepath.IsLocal(f) || path.IsAbs(f) || strings.Contains(f, `\`) || path.Clean(f) != f {
			return "", nil, fmt.Errorf("deps: %q is not a file this cache can hash", f)
		}
		if err := plainPath(ws, path.Dir(f)); err != nil {
			return "", nil, err
		}
		b, err := root.ReadFile(f)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(h, "input\x00%d\x00%s\x00absent\x00", len(f), f)
		case err != nil:
			return "", nil, fmt.Errorf("deps: %w", err)
		default:
			fmt.Fprintf(h, "input\x00%d\x00%s\x00%d\x00", len(f), f, len(b))
			h.Write(b)
		}
	}
	sort.Strings(dirs)
	return hex.EncodeToString(h.Sum(nil))[:24], dirs, nil
}

// Key is the cache key of the workspace's lockfiles under spec: what Prepare files them under. A caller that wants to know the key before it
// installs (to remember that an install of it has failed) asks here.
func (c *Cache) Key(ws string, spec Spec) (string, error) {
	key, _, err := plan(ws, spec)
	return key, err
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
	root, err := os.OpenRoot(ws)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	for _, d := range dirs {
		if st, err := os.Lstat(filepath.Join(ws, d)); err != nil || !st.IsDir() {
			return Result{}, fmt.Errorf("deps: the install did not make %s", d)
		}
		if err := cloneDir(ctx, root, ws, d, tmp, true); err != nil {
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
		if err := cloneDir(ctx, root, ws, d, entry, false); err != nil {
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

// errNoCloneAt says that clonefileat is not possible here.
var errNoCloneAt = errors.New("deps: no clone between open directories")

// cloneDir clones the dependency directory d (a path relative to both sides) between the workspace and the cache. The side that is the
// workspace is reached through root and opened as a directory first, and so is the cache's side; the clone is then made between the
// two open directories (clonefileat), so a workspace directory swapped for a link while it runs changes nothing: the descriptor names the
// inode that was opened. toCache says which way: from the workspace into dir (the temp entry) or from dir (the entry) into the workspace.
// Where clonefileat cannot be used the tree is copied with cp, by path: a swap while it runs is then only found by the caller's check
// afterwards.
func cloneDir(ctx context.Context, root *os.Root, ws, d, dir string, toCache bool) error {
	parent, name := path.Dir(d), path.Base(d)
	cacheParent := filepath.Join(dir, parent)
	if toCache {
		if err := os.MkdirAll(cacheParent, 0o755); err != nil {
			return err
		}
	}
	wsParent, err := root.OpenFile(parent, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer wsParent.Close()
	cache, err := os.Open(cacheParent)
	if err != nil {
		return err
	}
	defer cache.Close()
	src, dst := cache, wsParent
	if toCache {
		src, dst = wsParent, cache
	}
	err = errNoCloneAt
	if !noCloneAt {
		if testHook != nil {
			testHook("before-clone")
		}
		err = cloneAt(src, dst, name)
	}
	if !errors.Is(err, errNoCloneAt) {
		return err
	}
	return copyThroughRoots(ctx, root, d, dir, toCache)
}

// noCloneAt, when a test sets it, makes cloneDir take the fallback, as on a file system without clones.
var noCloneAt bool

// copyThroughRoots is the copy where clonefileat is not possible (another file system, two volumes, not macOS): the tree is copied one
// entry at a time in this process, both sides through os.Root handles opened first, so no path is resolved while it runs that a swap could
// redirect. Files are copied by content (no copy-on-write: it takes the disk space and the time), directories and symlinks are made as
// they were, modes are set on the open file. Anything that is not a directory, a regular file or a symlink is an error.
func copyThroughRoots(ctx context.Context, root *os.Root, d, dir string, toCache bool) error {
	cacheD := filepath.Join(dir, filepath.FromSlash(d))
	var src, dst *os.Root
	var err error
	if toCache {
		if src, err = root.OpenRoot(d); err != nil {
			return err
		}
		if err = os.MkdirAll(cacheD, 0o755); err != nil {
			src.Close()
			return err
		}
		dst, err = os.OpenRoot(cacheD)
	} else {
		if src, err = os.OpenRoot(cacheD); err != nil {
			return err
		}
		if err = root.Mkdir(d, 0o755); err != nil {
			src.Close()
			return err
		}
		dst, err = root.OpenRoot(d)
	}
	if err != nil {
		src.Close()
		return err
	}
	defer src.Close()
	defer dst.Close()
	if testHook != nil {
		testHook("before-copy") // the handles are open: what the names do from here on must not matter
	}
	if err := copyDir(ctx, src, dst, "."); err != nil {
		return fmt.Errorf("deps: copy %s: %w", d, err)
	}
	return nil
}

// readBatch is how many entries of a directory are read at a time: a directory of a million entries is then never in memory whole (fs.WalkDir
// reads and sorts all of a directory's entries before it visits the first). (A variable so that a test can make it small.)
var readBatch = 256

// copyDir copies the directory rel of src into the directory rel of dst (which is there, empty), depth first, entry by entry. The type of
// an entry is the one the directory read gave (no lstat by path); modes are read from open files. The directory's own mode is set last,
// through its open handle: a directory that is not writable could not be filled.
func copyDir(ctx context.Context, src, dst *os.Root, rel string) error {
	dir, err := src.Open(rel)
	if err != nil {
		return err
	}
	defer dir.Close()
	st, err := dir.Stat()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		des, rerr := dir.ReadDir(readBatch)
		for _, de := range des {
			p := path.Join(rel, de.Name())
			switch {
			case de.IsDir():
				if err := dst.Mkdir(p, 0o755); err != nil {
					return err
				}
				if err := copyDir(ctx, src, dst, p); err != nil {
					return err
				}
			case de.Type().IsRegular():
				if err := copyFile(src, dst, p); err != nil {
					return err
				}
			case de.Type()&fs.ModeSymlink != 0:
				target, err := src.Readlink(p)
				if err != nil {
					return err
				}
				if err := dst.Symlink(target, p); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%s is not a directory, a file or a symlink", p)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	f, err := dst.OpenFile(rel, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(st.Mode().Perm())
}

// copyFile copies the file p from src to dst, with the mode of the source (read from its open file) set on the open copy.
func copyFile(src, dst *os.Root, p string) error {
	in, err := src.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	mode := st.Mode().Perm()
	out, err := dst.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
