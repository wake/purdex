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
