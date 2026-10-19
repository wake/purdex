package deps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspace is a directory holding the lockfile with the given content.
func workspace(t *testing.T, lock string) string {
	t.Helper()
	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, "pnpm-lock.yaml"), []byte(lock), 0o644))
	return ws
}

// installer makes node_modules the way a package manager does (a file, a nested one and a relative symlink) and counts its calls.
type installer struct {
	calls atomic.Int32
	delay time.Duration
	fail  error
	skip  bool // does not make the directory
}

func (i *installer) install(_ context.Context, ws string) error {
	i.calls.Add(1)
	time.Sleep(i.delay)
	if i.fail != nil {
		return i.fail
	}
	if i.skip {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(ws, "node_modules/.pnpm/pkg@1/node_modules/pkg"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ws, "node_modules/.pnpm/pkg@1/node_modules/pkg/index.js"), []byte("module.exports = 1\n"), 0o755); err != nil {
		return err
	}
	return os.Symlink(".pnpm/pkg@1/node_modules/pkg", filepath.Join(ws, "node_modules/pkg"))
}

func newCache(t *testing.T, keep int) *Cache {
	t.Helper()
	c, err := NewCache(filepath.Join(t.TempDir(), "deps"), keep)
	require.NoError(t, err)
	c.pollLock = 5 * time.Millisecond
	return c
}

func checkInstalled(t *testing.T, ws string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, "node_modules/pkg/index.js")) // through the symlink
	require.NoError(t, err)
	assert.Equal(t, "module.exports = 1\n", string(b))
	target, err := os.Readlink(filepath.Join(ws, "node_modules/pkg"))
	require.NoError(t, err)
	assert.Equal(t, ".pnpm/pkg@1/node_modules/pkg", target, "a symlink stays a symlink")
	st, err := os.Stat(filepath.Join(ws, "node_modules/.pnpm/pkg@1/node_modules/pkg/index.js"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm(), "the mode is kept")
}

const fp = "pnpm 10.23 / node 24.14 / install --frozen-lockfile"

func spec(i *installer) Spec {
	return Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: fp, Install: i.install}
}

// The first workspace installs; the second one with the same lockfile gets a clone and installs nothing.
func TestPrepare_AMissInstallsAndAHitClones(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	ws1 := workspace(t, "lock: v1\n")
	r1, err := c.Prepare(context.Background(), ws1, spec(in))
	require.NoError(t, err)
	assert.False(t, r1.Hit)
	checkInstalled(t, ws1)

	ws2 := workspace(t, "lock: v1\n")
	r2, err := c.Prepare(context.Background(), ws2, spec(in))
	require.NoError(t, err)
	assert.True(t, r2.Hit)
	assert.Equal(t, r1.Key, r2.Key)
	assert.EqualValues(t, 1, in.calls.Load(), "one install for two workspaces")
	checkInstalled(t, ws2)

	// the clone is its own: a change in one workspace is not in the other or in the cache
	require.NoError(t, os.WriteFile(filepath.Join(ws2, "node_modules/.pnpm/pkg@1/node_modules/pkg/index.js"), []byte("changed\n"), 0o755))
	ws3 := workspace(t, "lock: v1\n")
	_, err = c.Prepare(context.Background(), ws3, spec(in))
	require.NoError(t, err)
	checkInstalled(t, ws3)
	checkInstalled(t, ws1)
}

// Another lockfile is another key.
func TestPrepare_AnotherLockfileIsAnotherKey(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "a\n"), spec(in))
	require.NoError(t, err)
	r2, err := c.Prepare(context.Background(), workspace(t, "b\n"), spec(in))
	require.NoError(t, err)
	assert.NotEqual(t, r1.Key, r2.Key)
	assert.False(t, r2.Hit)
	assert.EqualValues(t, 2, in.calls.Load())
}

// Only the newest Keep entries stay; using an entry makes it the newest.
func TestPrepare_OnlyTheNewestEntriesStay(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	keys := map[string]string{}
	for _, l := range []string{"one\n", "two\n"} {
		r, err := c.Prepare(context.Background(), workspace(t, l), spec(in))
		require.NoError(t, err)
		keys[l] = r.Key
		time.Sleep(20 * time.Millisecond) // modification times apart
	}
	_, err := c.Prepare(context.Background(), workspace(t, "one\n"), spec(in)) // a hit: "one" is newer than "two" now
	require.NoError(t, err)
	r3, err := c.Prepare(context.Background(), workspace(t, "three\n"), spec(in)) // a third: the oldest, "two", goes
	require.NoError(t, err)
	_, errOne := os.Stat(filepath.Join(c.Dir, keys["one\n"]))
	_, errTwo := os.Stat(filepath.Join(c.Dir, keys["two\n"]))
	_, errThree := os.Stat(filepath.Join(c.Dir, r3.Key))
	assert.NoError(t, errOne, "used last but one: stays")
	assert.True(t, os.IsNotExist(errTwo), "the oldest is gone")
	assert.NoError(t, errThree)
}

// Workspaces that ask for the same key at once install once, and all get the dependencies.
func TestPrepare_ConcurrentCallsInstallOnce(t *testing.T) {
	c, in := newCache(t, 2), &installer{delay: 200 * time.Millisecond}
	var wg sync.WaitGroup
	wss := make([]string, 4)
	errs := make([]error, 4)
	hits := make([]bool, 4)
	for i := range wss {
		wss[i] = workspace(t, "same\n")
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := c.Prepare(context.Background(), wss[i], spec(in))
			errs[i], hits[i] = err, r.Hit
		}(i)
	}
	wg.Wait()
	for i := range wss {
		require.NoError(t, errs[i])
		checkInstalled(t, wss[i])
	}
	assert.EqualValues(t, 1, in.calls.Load(), "one install")
	n := 0
	for _, h := range hits {
		if h {
			n++
		}
	}
	assert.Equal(t, 3, n, "the other three were hits")
}

// An install that fails leaves no entry, and the next call tries again.
func TestPrepare_AFailedInstallLeavesNoEntry(t *testing.T) {
	c := newCache(t, 2)
	bad := &installer{fail: errors.New("registry down")}
	_, err := c.Prepare(context.Background(), workspace(t, "x\n"), spec(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry down")
	des, _ := os.ReadDir(c.Dir)
	for _, de := range des {
		assert.True(t, filepath.Ext(de.Name()) == ".lock", "only a lock file is left: %s", de.Name())
	}
	good := &installer{}
	r, err := c.Prepare(context.Background(), workspace(t, "x\n"), spec(good))
	require.NoError(t, err)
	assert.False(t, r.Hit)
	assert.EqualValues(t, 1, good.calls.Load())
}

// An install that did not make the directory is an error, not an empty entry.
// A monorepo's one lockfile installs under its members too: the directories named in Spec.Dirs are installed, kept and cloned with the
// root's, are part of the key, and each must be there when the install is done.
func TestPrepare_AWorkspaceMembersDependenciesAreKeptAndCloned(t *testing.T) {
	c := newCache(t, 2)
	member := func(ctx context.Context, ws string) error {
		in := &installer{}
		if err := in.install(ctx, ws); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(ws, "spa/node_modules"), 0o755); err != nil {
			return err
		}
		return os.Symlink("../../node_modules/pkg", filepath.Join(ws, "spa/node_modules/pkg"))
	}
	sp := Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Dirs: []string{"spa/node_modules"}, Fingerprint: fp, Install: member}
	ws1 := workspace(t, "lock: v1\n")
	require.NoError(t, os.MkdirAll(filepath.Join(ws1, "spa"), 0o755))
	r1, err := c.Prepare(context.Background(), ws1, sp)
	require.NoError(t, err)
	assert.False(t, r1.Hit)

	ws2 := workspace(t, "lock: v1\n")
	require.NoError(t, os.MkdirAll(filepath.Join(ws2, "spa"), 0o755))
	r2, err := c.Prepare(context.Background(), ws2, sp)
	require.NoError(t, err)
	assert.True(t, r2.Hit)
	link, err := os.Readlink(filepath.Join(ws2, "spa/node_modules/pkg"))
	require.NoError(t, err, "the member's node_modules came with the clone")
	assert.Equal(t, "../../node_modules/pkg", link)

	// the same lockfile without the member is another key: a set made without it is not served
	ws3 := workspace(t, "lock: v1\n")
	rootOnly, err := c.Prepare(context.Background(), ws3, spec(&installer{}))
	require.NoError(t, err)
	assert.NotEqual(t, r1.Key, rootOnly.Key)

	// a member that the install did not make is an error, and so is a directory the cache does not know
	ws4 := workspace(t, "other\n")
	_, err = c.Prepare(context.Background(), ws4, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Dirs: []string{"spa/node_modules"}, Fingerprint: fp, Install: (&installer{}).install})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not make")
	for _, bad := range []string{"../x/node_modules", "/abs/node_modules", "spa/lib", "spa//node_modules", `a\b/node_modules`, "spa/../node_modules"} {
		_, err = c.Prepare(context.Background(), workspace(t, "z\n"), Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Dirs: []string{bad}, Fingerprint: fp, Install: (&installer{}).install})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "not a directory of dependencies", bad)
	}
}

func TestPrepare_AnInstallWithoutTheDirectoryIsAnError(t *testing.T) {
	c := newCache(t, 2)
	_, err := c.Prepare(context.Background(), workspace(t, "y\n"), spec(&installer{skip: true}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not make")
}

func TestPrepare_RefusesALockfileItDoesNotKnow(t *testing.T) {
	c := newCache(t, 2)
	ws := workspace(t, "z\n")
	for _, f := range []string{"", "../pnpm-lock.yaml", "/etc/passwd", "go.sum", "sub\\pnpm-lock.yaml"} {
		_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{f}, Fingerprint: fp, Install: (&installer{}).install})
		assert.Error(t, err, "%q", f)
	}
	_, err := c.Prepare(context.Background(), ws, Spec{})
	assert.Error(t, err, "no lockfile")
	_, err = c.Prepare(context.Background(), t.TempDir(), spec(&installer{}))
	assert.Error(t, err, "the lockfile is not in the workspace")
}

// A lockfile in a subdirectory brings its own directory with it.
func TestPrepare_ALockfileInASubdirectory(t *testing.T) {
	c := newCache(t, 2)
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "spa/pnpm-lock.yaml"), []byte("l\n"), 0o644))
	in := func(_ context.Context, w string) error {
		require.NoError(t, os.MkdirAll(filepath.Join(w, "spa/node_modules/a"), 0o755))
		return os.WriteFile(filepath.Join(w, "spa/node_modules/a/x"), []byte("x"), 0o644)
	}
	_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}, Fingerprint: fp, Install: in})
	require.NoError(t, err)
	ws2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws2, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws2, "spa/pnpm-lock.yaml"), []byte("l\n"), 0o644))
	r, err := c.Prepare(context.Background(), ws2, Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}, Fingerprint: fp})
	require.NoError(t, err)
	assert.True(t, r.Hit, "a hit needs no way to install")
	_, err = os.Stat(filepath.Join(ws2, "spa/node_modules/a/x"))
	assert.NoError(t, err)
}

// A waiting install ends when its context does.
func TestPrepare_AWaitingCallEndsWithItsContext(t *testing.T) {
	c := newCache(t, 2)
	ws := workspace(t, "w\n")
	key, _, err := plan(ws, spec(&installer{}))
	require.NoError(t, err)
	holder, err := c.lock(context.Background(), key, syscall.LOCK_EX)
	require.NoError(t, err)
	defer holder.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = c.Prepare(ctx, ws, spec(&installer{}))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// An entry a clone is reading from (its lock held shared) is not removed from under it: the prune waits.
func TestPrune_WaitsForAnEntryThatIsBeingRead(t *testing.T) {
	c, in := newCache(t, 1), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "old\n"), spec(in))
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	reader, err := c.lock(context.Background(), r1.Key, syscall.LOCK_SH)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		_, _ = c.Prepare(context.Background(), workspace(t, "new\n"), spec(in)) // Keep is 1: the old one is to go
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the prune did not wait for the reader")
	case <-time.After(300 * time.Millisecond):
	}
	_, err = os.Stat(filepath.Join(c.Dir, r1.Key))
	assert.NoError(t, err, "still there while it is read")
	reader.Close()
	<-done
	_, err = os.Stat(filepath.Join(c.Dir, r1.Key))
	assert.True(t, os.IsNotExist(err), "gone once the reader is done")
	_, err = os.Stat(filepath.Join(c.Dir, r1.Key+".lock"))
	assert.NoError(t, err, "the lock file stays: a process that has it open and one that opens it next must lock the same file")
}

// A lockfile path that goes through a symlink out of the workspace is refused, and nothing outside is read, copied or removed.
func TestPrepare_ASymlinkOutOfTheWorkspaceInThePathIsRefused(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "pnpm-lock.yaml"), []byte("outside\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "node_modules/keep"), 0o755))
	ws := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "link")))
	_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{"link/pnpm-lock.yaml"}, Fingerprint: fp, Install: in.install})
	require.Error(t, err)
	assert.EqualValues(t, 0, in.calls.Load(), "nothing was installed")
	_, serr := os.Stat(filepath.Join(outside, "node_modules/keep"))
	assert.NoError(t, serr, "nothing outside was removed")

	// the lockfile itself a link to a file outside: its content would be read into the key
	ws3 := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(outside, "pnpm-lock.yaml"), filepath.Join(ws3, "pnpm-lock.yaml")))
	_, err = c.Prepare(context.Background(), ws3, spec(in))
	assert.Error(t, err, "a lockfile that is a symlink")

	// the same when the entry exists (a hit would clone out and remove the destination first)
	wsOK := workspace(t, "outside\n")
	_, err = c.Prepare(context.Background(), wsOK, spec(in))
	require.NoError(t, err)
	ws2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws2, "pnpm-lock.yaml"), []byte("outside\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(ws2, "node_modules"))) // the dependency directory itself is a link out
	_, err = c.Prepare(context.Background(), ws2, spec(in))
	assert.Error(t, err)
	_, serr = os.Stat(filepath.Join(outside, "node_modules/keep"))
	assert.NoError(t, serr, "nothing outside was removed by a hit")
}

// The same lockfile installed by another recipe (another package manager version, Node ABI, flags) is another entry.
func TestPrepare_AnotherInstallerFingerprintIsAnotherKey(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "same\n"), spec(in))
	require.NoError(t, err)
	s2 := spec(in)
	s2.Fingerprint = "pnpm 10.23 / node 25.0 / install --frozen-lockfile"
	r2, err := c.Prepare(context.Background(), workspace(t, "same\n"), s2)
	require.NoError(t, err)
	assert.NotEqual(t, r1.Key, r2.Key)
	assert.False(t, r2.Hit)
	assert.EqualValues(t, 2, in.calls.Load())

	// the fingerprint is required: a cache that cannot tell installs apart would serve the wrong one
	s3 := spec(in)
	s3.Fingerprint = ""
	_, err = c.Prepare(context.Background(), workspace(t, "same\n"), s3)
	assert.Error(t, err)
}

// The boundary between fields is part of the key: a lockfile name and content, or a fingerprint, cannot be shifted into each other.
func TestPlan_FieldsAreDelimited(t *testing.T) {
	ws1 := workspace(t, "ab")
	ws2 := workspace(t, "b")
	k1, _, err := plan(ws1, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: "x"})
	require.NoError(t, err)
	k2, _, err := plan(ws2, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: "ax"})
	require.NoError(t, err)
	assert.NotEqual(t, k1, k2)
}

// An entry that was only half removed (a prune killed midway) is not a hit: it is thrown away and installed again.
func TestPrepare_AHalfRemovedEntryIsNotAHit(t *testing.T) {
	c, in := newCache(t, 2), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "half\n"), spec(in))
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(c.Dir, r1.Key, "node_modules"))) // what a killed RemoveAll of the old kind leaves: the entry root, a directory gone
	ws := workspace(t, "half\n")
	r2, err := c.Prepare(context.Background(), ws, spec(in))
	require.NoError(t, err)
	assert.False(t, r2.Hit, "installed again")
	assert.EqualValues(t, 2, in.calls.Load())
	checkInstalled(t, ws)
	r3, err := c.Prepare(context.Background(), workspace(t, "half\n"), spec(in))
	require.NoError(t, err)
	assert.True(t, r3.Hit, "and the new entry is whole")
}

// An eviction renames the entry away first, so a kill during the removal never leaves something that looks like an entry.
func TestPrune_LeavesNoEntryShapedRemnants(t *testing.T) {
	c, in := newCache(t, 1), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "e1\n"), spec(in))
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	_, err = c.Prepare(context.Background(), workspace(t, "e2\n"), spec(in))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(c.Dir, r1.Key))
	assert.True(t, os.IsNotExist(err))
	des, _ := os.ReadDir(c.Dir)
	for _, de := range des {
		assert.NotContains(t, de.Name(), ".gone-", "the removed entry's remnants are cleaned: %s", de.Name())
	}
}

// What a crash leaves behind (a temporary directory of an install, a tombstone of an eviction) is removed once it is old; a young one,
// which another process may be using, is left.
func TestSweep_RemovesOldTemporaryAndTombstoneDirectories(t *testing.T) {
	c := newCache(t, 2)
	old := time.Now().Add(-2 * tmpMaxAge)
	for _, n := range []string{"aaaa.tmp-123", "bbbb.gone-456"} {
		require.NoError(t, os.MkdirAll(filepath.Join(c.Dir, n, "node_modules/x"), 0o755))
		require.NoError(t, os.Chtimes(filepath.Join(c.Dir, n), old, old))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(c.Dir, "cccc.tmp-789/node_modules"), 0o755)) // young
	c.sweep()
	for _, n := range []string{"aaaa.tmp-123", "bbbb.gone-456"} {
		_, err := os.Stat(filepath.Join(c.Dir, n))
		assert.True(t, os.IsNotExist(err), n)
	}
	_, err := os.Stat(filepath.Join(c.Dir, "cccc.tmp-789"))
	assert.NoError(t, err, "a young one stays")
}

// Two inputs whose bytes would be the same without the lengths (the first fingerprint plus a lockfile's entry is the second fingerprint)
// are two keys.
func TestPlan_AFingerprintCannotSwallowALockfileEntry(t *testing.T) {
	ws1 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws1, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws1, "pnpm-lock.yaml"), []byte("AAA"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws1, "spa/pnpm-lock.yaml"), []byte("BB"), 0o644))
	ws2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws2, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws2, "spa/pnpm-lock.yaml"), []byte("BB"), 0o644))
	k1, _, err := plan(ws1, Spec{Lockfiles: []string{"pnpm-lock.yaml", "spa/pnpm-lock.yaml"}, Fingerprint: "q"})
	require.NoError(t, err)
	k2, _, err := plan(ws2, Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}, Fingerprint: "qpnpm-lock.yaml\x003\x00AAA"})
	require.NoError(t, err)
	assert.NotEqual(t, k1, k2)
}

// A removal that fails or stops midway leaves a tombstone, never a directory with the entry's name.
func TestPrune_AFailedRemovalLeavesNoEntryShapedDirectory(t *testing.T) {
	old := removeAll
	removeAll = func(string) error { return errors.New("killed") }
	t.Cleanup(func() { removeAll = old })
	c, in := newCache(t, 1), &installer{}
	r1, err := c.Prepare(context.Background(), workspace(t, "k1\n"), spec(in))
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	_, err = c.Prepare(context.Background(), workspace(t, "k2\n"), spec(in))
	require.NoError(t, err)
	_, serr := os.Stat(filepath.Join(c.Dir, r1.Key))
	assert.True(t, os.IsNotExist(serr), "no directory with the evicted key's name")
	matches, _ := filepath.Glob(filepath.Join(c.Dir, r1.Key+".gone-*"))
	assert.Len(t, matches, 1, "a tombstone is what is left")
	// and the key is a miss again, installed fresh
	r3, err := c.Prepare(context.Background(), workspace(t, "k1\n"), spec(in))
	require.NoError(t, err)
	assert.False(t, r3.Hit)
}

// A workspace with spa/pnpm-lock.yaml, and an installer that makes spa/node_modules.
func spaWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "spa/pnpm-lock.yaml"), []byte("spa lock\n"), 0o644))
	return ws
}

func spaInstall(_ context.Context, ws string) error {
	if err := os.MkdirAll(filepath.Join(ws, "spa/node_modules/x"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ws, "spa/node_modules/x/index.js"), []byte("x"), 0o644)
}

var spaSpec = Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}, Fingerprint: fp, Install: spaInstall}

// swapSpa turns spa/ of the workspace into a link to outside (what a process an install left behind could do between a check and a use).
func swapSpa(t *testing.T, ws, outside string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(filepath.Join(ws, "spa")))
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "spa")))
}

// The workspace's own directory is turned into a link after the check: each use of the path that follows must not reach outside.
func TestPrepare_AParentSwappedForALinkAfterTheCheckReachesNothingOutside(t *testing.T) {
	t.Cleanup(func() { testHook = nil })
	mkOutside := func() string {
		o := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(o, "node_modules/keep"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(o, "pnpm-lock.yaml"), []byte("spa lock\n"), 0o644)) // the same bytes: a read that follows the link succeeds
		return o
	}
	kept := func(o string) bool { _, err := os.Stat(filepath.Join(o, "node_modules/keep")); return err == nil }

	t.Run("the lockfile is read through the root", func(t *testing.T) {
		c, outside, ws := newCache(t, 2), mkOutside(), spaWorkspace(t)
		testHook = func(stage string) {
			if stage == "plan-read" {
				swapSpa(t, ws, outside)
				testHook = nil
			}
		}
		_, err := c.Prepare(context.Background(), ws, spaSpec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes", "stopped at the read, not later for another reason")
	})

	t.Run("a hit removes through the root", func(t *testing.T) {
		c, outside := newCache(t, 2), mkOutside()
		_, err := c.Prepare(context.Background(), spaWorkspace(t), spaSpec) // the entry
		require.NoError(t, err)
		ws := spaWorkspace(t)
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "spa/node_modules"), 0o755))
		testHook = func(stage string) {
			if stage == "clone-out" {
				swapSpa(t, ws, outside)
				testHook = nil
			}
		}
		_, err = c.Prepare(context.Background(), ws, spaSpec)
		assert.Error(t, err)
		assert.True(t, kept(outside), "spa/node_modules outside was not removed")
	})

	t.Run("a clone out that was redirected is found out", func(t *testing.T) {
		c, outside := newCache(t, 2), mkOutside()
		_, err := c.Prepare(context.Background(), spaWorkspace(t), spaSpec)
		require.NoError(t, err)
		ws := spaWorkspace(t)
		testHook = func(stage string) {
			if stage == "copied-out" {
				swapSpa(t, ws, outside)
				testHook = nil
			}
		}
		_, err = c.Prepare(context.Background(), ws, spaSpec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})

	t.Run("a copy into the cache that was redirected is not kept", func(t *testing.T) {
		c, outside, ws := newCache(t, 2), mkOutside(), spaWorkspace(t)
		testHook = func(stage string) {
			if stage == "copied-in" {
				swapSpa(t, ws, outside)
				testHook = nil
			}
		}
		_, err := c.Prepare(context.Background(), ws, spaSpec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
		des, _ := os.ReadDir(c.Dir)
		for _, de := range des {
			assert.Equal(t, ".lock", filepath.Ext(de.Name()), "no entry was made: %s", de.Name())
		}
	})
}

// A temporary directory whose key is locked (an install or a clone in progress) is not swept, however old; once the lock is free it is.
func TestSweep_LeavesWhatALockedKeyIsUsing(t *testing.T) {
	c := newCache(t, 2)
	old := time.Now().Add(-2 * tmpMaxAge)
	tmp := filepath.Join(c.Dir, "kkkk.tmp-1")
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "node_modules/x"), 0o755))
	require.NoError(t, os.Chtimes(tmp, old, old))
	held, err := c.lock(context.Background(), "kkkk", syscall.LOCK_EX)
	require.NoError(t, err)
	c.sweep()
	_, serr := os.Stat(tmp)
	assert.NoError(t, serr, "its key is locked: a live install")
	held.Close()
	c.sweep()
	_, serr = os.Stat(tmp)
	assert.True(t, os.IsNotExist(serr))
}

// The attack on a copy that is made by path: the workspace's directory is swapped for a link to outside when the copy starts and swapped
// back when it is done, so that a check afterwards finds nothing. The copy is made between directories opened before, so it goes to and
// comes from the inode that was opened, whatever the names did meanwhile. (clonefileat is macOS; elsewhere the copy is cp, by path.)
func TestPrepare_ASwapAndSwapBackDuringTheCopyChangesNothing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the copy between open directories is clonefileat, macOS only")
	}
	t.Cleanup(func() { testHook = nil })
	swapAway := func(ws, outside string) {
		require.NoError(t, os.Rename(filepath.Join(ws, "spa"), filepath.Join(ws, "spa.orig")))
		require.NoError(t, os.Symlink(outside, filepath.Join(ws, "spa")))
	}
	swapBack := func(ws string) {
		require.NoError(t, os.Remove(filepath.Join(ws, "spa")))
		require.NoError(t, os.Rename(filepath.Join(ws, "spa.orig"), filepath.Join(ws, "spa")))
	}

	t.Run("a copy out of the cache goes into the workspace's directory, not outside", func(t *testing.T) {
		c := newCache(t, 2)
		_, err := c.Prepare(context.Background(), spaWorkspace(t), spaSpec)
		require.NoError(t, err)
		outside, ws := t.TempDir(), spaWorkspace(t)
		testHook = func(stage string) {
			switch stage {
			case "before-clone":
				swapAway(ws, outside)
			case "copied-out":
				swapBack(ws)
				testHook = nil
			}
		}
		r, err := c.Prepare(context.Background(), ws, spaSpec)
		require.NoError(t, err)
		assert.True(t, r.Hit)
		_, serr := os.Stat(filepath.Join(ws, "spa/node_modules/x/index.js"))
		assert.NoError(t, serr, "it went where it was meant to")
		des, _ := os.ReadDir(outside)
		assert.Empty(t, des, "nothing was written outside")
	})

	t.Run("a copy into the cache is of the workspace's directory, not of what the link shows", func(t *testing.T) {
		c, ws := newCache(t, 2), spaWorkspace(t)
		outside := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(outside, "node_modules/poison"), 0o755))
		testHook = func(stage string) {
			switch stage {
			case "before-clone":
				swapAway(ws, outside)
			case "copied-in":
				swapBack(ws)
				testHook = nil
			}
		}
		r, err := c.Prepare(context.Background(), ws, spaSpec)
		require.NoError(t, err)
		_, serr := os.Stat(filepath.Join(c.Dir, r.Key, "spa/node_modules/x/index.js"))
		assert.NoError(t, serr, "the entry holds what the install made")
		_, serr = os.Stat(filepath.Join(c.Dir, r.Key, "spa/node_modules/poison"))
		assert.True(t, os.IsNotExist(serr), "and nothing from outside")
	})
}

// The fallback (no clonefileat: another file system, two volumes, not macOS) is a copy made through root handles, not cp by path: the
// same swap-and-swap-back during it changes nothing, on every platform, and the tree arrives as it was (modes, nested files, symlinks).
func TestPrepare_TheCopyFallbackIsThroughHandlesToo(t *testing.T) {
	noCloneAt = true
	t.Cleanup(func() { noCloneAt = false; testHook = nil })

	// the tree comes through whole
	c, in := newCache(t, 2), &installer{}
	ws1 := workspace(t, "fallback\n")
	_, err := c.Prepare(context.Background(), ws1, spec(in))
	require.NoError(t, err)
	ws2 := workspace(t, "fallback\n")
	r, err := c.Prepare(context.Background(), ws2, spec(in))
	require.NoError(t, err)
	assert.True(t, r.Hit)
	checkInstalled(t, ws2) // content, the symlink as a symlink, the mode 0755

	swapAway := func(ws, outside string) {
		require.NoError(t, os.Rename(filepath.Join(ws, "spa"), filepath.Join(ws, "spa.orig")))
		require.NoError(t, os.Symlink(outside, filepath.Join(ws, "spa")))
	}
	swapBack := func(ws string) {
		require.NoError(t, os.Remove(filepath.Join(ws, "spa")))
		require.NoError(t, os.Rename(filepath.Join(ws, "spa.orig"), filepath.Join(ws, "spa")))
	}

	t.Run("out of the cache", func(t *testing.T) {
		c := newCache(t, 2)
		_, err := c.Prepare(context.Background(), spaWorkspace(t), spaSpec)
		require.NoError(t, err)
		outside, ws := t.TempDir(), spaWorkspace(t)
		testHook = func(stage string) {
			switch stage {
			case "before-copy":
				swapAway(ws, outside)
			case "copied-out":
				swapBack(ws)
				testHook = nil
			}
		}
		r, err := c.Prepare(context.Background(), ws, spaSpec)
		require.NoError(t, err)
		assert.True(t, r.Hit)
		_, serr := os.Stat(filepath.Join(ws, "spa/node_modules/x/index.js"))
		assert.NoError(t, serr)
		des, _ := os.ReadDir(outside)
		assert.Empty(t, des, "nothing was written outside")
	})

	t.Run("into the cache", func(t *testing.T) {
		c, ws := newCache(t, 2), spaWorkspace(t)
		outside := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(outside, "node_modules/poison"), 0o755))
		testHook = func(stage string) {
			switch stage {
			case "before-copy":
				swapAway(ws, outside)
			case "copied-in":
				swapBack(ws)
				testHook = nil
			}
		}
		r, err := c.Prepare(context.Background(), ws, spaSpec)
		require.NoError(t, err)
		_, serr := os.Stat(filepath.Join(c.Dir, r.Key, "spa/node_modules/x/index.js"))
		assert.NoError(t, serr)
		_, serr = os.Stat(filepath.Join(c.Dir, r.Key, "spa/node_modules/poison"))
		assert.True(t, os.IsNotExist(serr), "nothing from outside")
	})
}

// The fallback reads a directory in batches, so a directory of a million entries is not held in memory whole: with a small batch every
// entry of a big, flat, and nested tree still arrives, in modes and all, and a cancelled context stops the copy at once.
func TestPrepare_TheCopyFallbackReadsInBatchesAndStopsWhenCancelled(t *testing.T) {
	noCloneAt = true
	oldBatch := readBatch
	readBatch = 7
	t.Cleanup(func() { noCloneAt = false; readBatch = oldBatch; testHook = nil })

	big := func(ctx context.Context, ws string) error {
		for i := 0; i < 100; i++ {
			if err := os.WriteFile(filepath.Join(ws, "node_modules", "f"+strconv.Itoa(i)), []byte(strconv.Itoa(i)), 0o640); err != nil {
				return err
			}
		}
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "node_modules/a/b/c"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "node_modules/a/b/c/deep"), []byte("deep"), 0o600))
		require.NoError(t, os.Chmod(filepath.Join(ws, "node_modules/a/b"), 0o500)) // a directory that cannot be written: its mode comes last
		return nil
	}
	mk := func(install func(context.Context, string) error) (string, Spec) {
		ws := workspace(t, "batches\n")
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "node_modules"), 0o755))
		return ws, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: fp, Install: install}
	}
	c := newCache(t, 2)
	ws1, sp := mk(big)
	_, err := c.Prepare(context.Background(), ws1, sp)
	require.NoError(t, err)
	ws2, _ := mk(nil)
	r, err := c.Prepare(context.Background(), ws2, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: fp})
	require.NoError(t, err)
	assert.True(t, r.Hit)
	for i := 0; i < 100; i++ {
		b, err := os.ReadFile(filepath.Join(ws2, "node_modules", "f"+strconv.Itoa(i)))
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(i), string(b))
	}
	st, err := os.Stat(filepath.Join(ws2, "node_modules/f3"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), st.Mode().Perm())
	st, err = os.Stat(filepath.Join(ws2, "node_modules/a/b"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o500), st.Mode().Perm(), "a directory's mode is set after it was filled")
	b, err := os.ReadFile(filepath.Join(ws2, "node_modules/a/b/c/deep"))
	require.NoError(t, err)
	assert.Equal(t, "deep", string(b))
	for _, d := range []string{ws1, ws2, c.Dir} { // so that the temp dirs can be removed
		_ = os.Chmod(filepath.Join(d, "node_modules/a/b"), 0o755)
	}
	for _, e := range mustReadDir(t, c.Dir) {
		_ = os.Chmod(filepath.Join(c.Dir, e, "node_modules/a/b"), 0o755)
	}

	// a cancelled context stops the copy before it starts on the tree
	ws3, _ := mk(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Prepare(ctx, ws3, Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Fingerprint: fp})
	assert.ErrorIs(t, err, context.Canceled)
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}
