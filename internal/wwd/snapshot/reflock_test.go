package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockRef leaves the lock file git takes for a loose ref, as another git process in the same clone does while it writes the ref; the
// returned func lets it go. The ref files of a clone with this ref are under <repo>/.git/refs/wwd/.
func lockRef(t *testing.T, repo, jobID string) (release func()) {
	t.Helper()
	dir := filepath.Join(repo, ".git", "refs", "wwd")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	lock := filepath.Join(dir, jobID+".lock")
	require.NoError(t, os.WriteFile(lock, nil, 0o644))
	return func() { _ = os.Remove(lock) }
}

// git itself waits 100 ms for a ref lock (core.filesRefLockTimeout) before it says "cannot lock ref", so a lock shorter than that never
// reaches wwd. A lock held longer is what wwd's own tries are for: Pin and Unpin try again (a few times, a short wait between) instead of
// failing the job's submission on the shared clone.
func TestPinAndUnpinWaitOutARefLockLongerThanGitsOwnWait(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 500 * time.Millisecond
	defer func() { refLockBackoff = old }()
	repo, _, snap := origin(t)

	// Pin runs a few git checks before it writes the ref, so the lock is held long enough to still be there when it does
	release := lockRef(t, repo, "j_lock")
	go func() { time.Sleep(1200 * time.Millisecond); release() }() // past git's own 100 ms, gone before the last try
	require.NoError(t, Pin(context.Background(), repo, "j_lock", snap))
	assert.Equal(t, snap, pinned(context.Background(), repo, "j_lock"))

	release = lockRef(t, repo, "j_lock")
	go func() { time.Sleep(1200 * time.Millisecond); release() }()
	require.NoError(t, Unpin(context.Background(), repo, "j_lock"))
	assert.Equal(t, "", pinned(context.Background(), repo, "j_lock"))
}

// A lock that stays is an error after the tries are used: the caller (the queue) keeps its own retry, so the wait is bounded.
func TestPinGivesUpOnALockThatStays(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 5 * time.Millisecond
	defer func() { refLockBackoff = old }()
	repo, _, snap := origin(t)
	release := lockRef(t, repo, "j_stuck")
	defer release()
	start := time.Now()
	err := Pin(context.Background(), repo, "j_stuck", snap)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock")
	assert.Less(t, time.Since(start), 10*time.Second)
}

// Only a lock is waited for: any other failure of the command is returned at once, with no tries (here a value that is no object), and
// the words git uses for a lock are the ones that count as one.
func TestOnlyALockErrorIsRetried(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 2 * time.Second // a retry would be seen as the time it takes
	defer func() { refLockBackoff = old }()
	repo, _, _ := origin(t)
	start := time.Now()
	_, err := gitRefWrite(context.Background(), "wwd.snapshot-pin", repo, "update-ref", PinRef("j_bad"), strings.Repeat("1", 40))
	require.Error(t, err)
	assert.False(t, isRefLockError(err), "%v", err)
	assert.Less(t, time.Since(start), time.Second)

	assert.True(t, isRefLockError(errors.New("fatal: cannot lock ref 'refs/wwd/j': Unable to create '/x/refs/wwd/j.lock': File exists.")))
	assert.True(t, isRefLockError(errors.New("fatal: Unable to create '/x/packed-refs.lock': File exists.")))
	assert.False(t, isRefLockError(errors.New("fatal: not a git repository")))
	assert.False(t, isRefLockError(nil))
}

// Many git processes writing and deleting refs of different jobs in one clone at the same time all succeed (the deleting ones take the
// packed-refs lock, which is the one they fight over).
func TestConcurrentPinsAndUnpinsInOneCloneAllSucceed(t *testing.T) {
	repo, _, snap := origin(t)
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			ctx := context.Background()
			if err := Pin(ctx, repo, id, snap); err != nil {
				errs <- fmt.Errorf("pin %s: %w", id, err)
				return
			}
			if err := Unpin(ctx, repo, id); err != nil {
				errs <- fmt.Errorf("unpin %s: %w", id, err)
			}
		}(fmt.Sprintf("j_c%d", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// The worker's fetch of a job's ref from the bundle waits out a lock on that ref in the same way (the clone may be shared).
func TestFetchBundleWaitsOutARefLockLongerThanGitsOwnWait(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 500 * time.Millisecond
	defer func() { refLockBackoff = old }()
	repo, _, snap := origin(t)
	require.NoError(t, Pin(context.Background(), repo, "j_f", snap))
	bundle := filepath.Join(t.TempDir(), "j.bundle")
	gitOut(t, repo, "bundle", "create", bundle, PinRef("j_f"))
	clone := t.TempDir()
	gitOut(t, clone, "init", "-q", ".")

	f, err := os.Open(bundle)
	require.NoError(t, err)
	defer f.Close()
	release := lockRef(t, clone, "j_f")
	go func() { time.Sleep(1200 * time.Millisecond); release() }()
	require.NoError(t, fetchBundle(context.Background(), clone, PinRef("j_f"), f))
	assert.Equal(t, snap, pinned(context.Background(), clone, "j_f"))
}

// A context that ends while the tries wait is the answer, not the lock error before it: the caller tells a cancelled call from a git fault
// by errors.Is, and the lock error is kept in the text.
func TestACancelDuringTheWaitIsTheContextsError(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 5 * time.Second
	defer func() { refLockBackoff = old }()
	repo, _, snap := origin(t)
	release := lockRef(t, repo, "j_cancel")
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Pin(ctx, repo, "j_cancel", snap)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "cannot lock ref")
	assert.Less(t, time.Since(start), 4*time.Second, "the wait ended with the context, not after the backoff")
}

// The lock error is recognised by the English words of the tool that raised it, so a ref write runs with the C locale (no other command of
// the package does: a checkout keeps the locale its filters expect) whatever locale the service was started in (LC_ALL wins over LANG and LC_MESSAGES, and LANGUAGE is ignored under C). The last value of a
// name is the one the child gets.
func TestTheToolRunsInTheCLocaleWhateverTheParentUses(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	last := map[string]string{}
	for _, kv := range refWriteEnv() {
		name, val, _ := strings.Cut(kv, "=")
		last[name] = val
	}
	assert.Equal(t, "C", last["LC_ALL"])
	for _, kv := range gitEnv() {
		assert.NotEqual(t, "LC_ALL=C", kv, "only a ref write is pinned to the C locale")
	}
}

// A wait whose timer and whose context are ready together is still the context's error: select picks either branch, so the wait checks the
// context after it, and a cancelled caller does not start another git.
func TestWaitRefLock_ACancelledContextWinsOverAReadyTimer(t *testing.T) {
	old := refLockBackoff
	refLockBackoff = 0
	defer func() { refLockBackoff = old }()
	for i := 0; i < 200; i++ {
		require.ErrorIs(t, waitRefLock(&endsDuringWait{Context: context.Background()}), context.Canceled)
	}
	assert.NoError(t, waitRefLock(context.Background()))
}

// endsDuringWait is a context that is live when the wait starts (the first Err is nil) and ended once it is over, with Done already closed:
// the timer and the end are ready together.
type endsDuringWait struct {
	context.Context
	calls int
}

func (c *endsDuringWait) Err() error {
	c.calls++
	if c.calls == 1 {
		return nil
	}
	return context.Canceled
}

func (c *endsDuringWait) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// The messages git really prints when a fetch into a ref finds the ref's lock held (2.53 on Linux batches the ref update and drops the
// lock-file detail), and some that must not be taken for a held lock.
func TestIsRefLockErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"git 2.54 fetch", "error: cannot lock ref 'refs/wwd/j_f': Unable to create '/x/.git/refs/wwd/j_f.lock': File exists.", true},
		{"git 2.53 fetch", "git fetch -q --no-tags /tmp/b.bundle +refs/wwd/j_f:refs/wwd/j_f: exit status 1: error: fetching ref refs/wwd/j_f failed: reference already exists", true},
		{"git 2.53 update-ref", "fatal: update_ref failed for ref 'refs/wwd/j_f': cannot lock ref 'refs/wwd/j_f': Unable to create '/x/j_f.lock': File exists.", true},
		{"already exists elsewhere", "fatal: destination path 'x' already exists and is not an empty directory", false},
		{"reference already exists without fetching", "fatal: reference already exists", false},
		{"fetching failed for another reason", "error: fetching ref refs/wwd/j_f failed: some other reason", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, isRefLockError(errors.New(c.msg)), c.name)
	}
}
