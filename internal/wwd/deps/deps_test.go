package deps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func spec(i *installer) Spec {
	return Spec{Lockfiles: []string{"pnpm-lock.yaml"}, Install: i.install}
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
		_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{f}, Install: (&installer{}).install})
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
	_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}, Install: in})
	require.NoError(t, err)
	ws2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws2, "spa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws2, "spa/pnpm-lock.yaml"), []byte("l\n"), 0o644))
	r, err := c.Prepare(context.Background(), ws2, Spec{Lockfiles: []string{"spa/pnpm-lock.yaml"}})
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
	_, err := c.Prepare(context.Background(), ws, Spec{Lockfiles: []string{"link/pnpm-lock.yaml"}, Install: in.install})
	require.Error(t, err)
	assert.EqualValues(t, 0, in.calls.Load(), "nothing was installed")
	_, serr := os.Stat(filepath.Join(outside, "node_modules/keep"))
	assert.NoError(t, serr, "nothing outside was removed")

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
