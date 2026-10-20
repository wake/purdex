package jobexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The timed trim of the build cache (#3833): the cache is trimmed on the worker's own beat, not only when a go job starts, so that a host that
// is mostly idle (or whose worker is off) does not stay over the bound.

const tickCacheName = "4ccf42170c3cfd0d" // the key of one Go version's cache: 16 hex characters

type tickRig struct {
	e     *Exec
	cache string
	lock  string
}

// newTickRig makes an Exec with the bound of maxMB and an empty cache directory under its deps directory.
func newTickRig(t *testing.T, maxMB int) *tickRig {
	t.Helper()
	deps := t.TempDir()
	e := New(Config{WorkRoot: t.TempDir(), DepsDir: deps, GoCacheMaxMB: maxMB})
	cache := filepath.Join(deps, "gocache", tickCacheName)
	require.NoError(t, os.MkdirAll(cache, 0o755))
	return &tickRig{e: e, cache: cache, lock: cache + ".lock"}
}

// fill writes n entries of size bytes, spread over the two-hex-digit directories, each with its file time age before now; it returns the paths.
func (r *tickRig) fill(t *testing.T, prefix string, n, size int, age time.Duration) []string {
	t.Helper()
	at := time.Now().Add(-age)
	data := make([]byte, size)
	var paths []string
	for i := 0; i < n; i++ {
		p := filepath.Join(r.cache, fmt.Sprintf("%02x", i%256), fmt.Sprintf("%s-%d", prefix, i))
		if i < 256 {
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		}
		require.NoError(t, os.WriteFile(p, data, 0o644))
		require.NoError(t, os.Chtimes(p, at, at))
		paths = append(paths, p)
	}
	return paths
}

func (r *tickRig) size(t *testing.T) int64 {
	t.Helper()
	_, total, err := scanGoCache(context.Background(), r.cache)
	require.NoError(t, err)
	return total
}

// hold takes the shared lock of the cache, as a running go job does; it is let go with the test.
func (r *tickRig) hold(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(r.lock, os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_SH))
	t.Cleanup(func() { f.Close() })
}

// (a) An idle host: nobody holds the cache, it is over its bound, and one tick brings it under 80% of the bound with no limit of 2000 files
// (the job's own pass has one).
func TestTheTickTrimsAnIdleCacheUnderItsTargetWithoutTheJobBudget(t *testing.T) {
	r := newTickRig(t, 1)                     // 1 MiB bound; the target is 80% of it
	r.fill(t, "old", 6000, 2048, 5*time.Hour) // 12 MiB, six thousand entries: more than the 2000 a job's pass removes
	require.Greater(t, r.size(t), int64(10)<<20)

	r.e.tickGoCaches(context.Background())

	assert.LessOrEqual(t, r.size(t), int64(1<<20)/100*80, "the cache is at or under 80% of its bound after one tick")
}

// The oldest entries go first, and the newest stay.
func TestTheTickRemovesTheOldestEntriesFirst(t *testing.T) {
	r := newTickRig(t, 1)
	old := r.fill(t, "old", 300, 4096, 30*time.Hour)  // 1.2 MiB
	newer := r.fill(t, "new", 100, 4096, time.Minute) // 0.4 MiB: the bound is 1 MiB, so the old ones alone bring it under 80%
	r.e.tickGoCaches(context.Background())
	for _, p := range newer {
		assert.FileExists(t, p)
	}
	gone := 0
	for _, p := range old {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			gone++
		}
	}
	assert.Greater(t, gone, 0)
}

// (b) A job holds the cache: only the entries unused for two hours go, as in a job's own pass, and nothing else.
func TestTheTickOfAHeldCacheTakesOnlyWhatHasNotBeenUsedForAWhile(t *testing.T) {
	r := newTickRig(t, 1)
	idle := r.fill(t, "idle", 400, 4096, 3*time.Hour) // 1.6 MiB, idle for three hours
	busy := r.fill(t, "busy", 400, 4096, 20*time.Minute)
	r.hold(t)

	r.e.tickGoCaches(context.Background())

	for _, p := range busy {
		assert.FileExists(t, p, "an entry used a moment ago is a held cache's")
	}
	gone := 0
	for _, p := range idle {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			gone++
		}
	}
	assert.Greater(t, gone, 0, "the idle ones go while it is held")
	assert.Greater(t, r.size(t), int64(1<<20)/100*80, "and it stays over its target: the rest is for when it is free")
}

// (c) The exclusive lock is not held for the whole trim: between two chunks it is let go, so that a job that waits for the shared lock gets in.
func TestTheTickLetsGoOfTheLockBetweenChunks(t *testing.T) {
	r := newTickRig(t, 1)
	r.fill(t, "old", 3000, 2048, 5*time.Hour)
	oldChunk, oldYield, oldBetween := goCacheTickChunk, goCacheTickYield, goCacheTickBetween
	goCacheTickChunk, goCacheTickYield = 500, time.Millisecond
	t.Cleanup(func() { goCacheTickChunk, goCacheTickYield, goCacheTickBetween = oldChunk, oldYield, oldBetween })
	var between, free atomic.Int32
	goCacheTickBetween = func() {
		between.Add(1)
		f, err := os.OpenFile(r.lock, os.O_CREATE|os.O_RDWR, 0o600)
		require.NoError(t, err)
		defer f.Close()
		if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) == nil { // a job that came in between two chunks
			free.Add(1)
		}
	}

	r.e.tickGoCaches(context.Background())

	assert.Greater(t, between.Load(), int32(1), "the trim was done in several chunks")
	assert.Equal(t, between.Load(), free.Load(), "and the lock was free between every two of them")
	assert.LessOrEqual(t, r.size(t), int64(1<<20)/100*80)
}

// (d) The tick looks at the size once in goCacheTickEvery, however often the worker's beat asks.
func TestTheTickLooksOnceInItsInterval(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	r.fill(t, "a", 600, 4096, 5*time.Hour) // 2.4 MiB
	r.e.tickGoCaches(context.Background())
	assert.LessOrEqual(t, r.size(t), int64(1<<20)/100*80)

	r.fill(t, "b", 600, 4096, 5*time.Hour) // over again
	now = now.Add(goCacheTickEvery - time.Minute)
	r.e.tickGoCaches(context.Background())
	assert.Greater(t, r.size(t), int64(1<<20), "not looked at yet")

	now = now.Add(2 * time.Minute)
	r.e.tickGoCaches(context.Background())
	assert.LessOrEqual(t, r.size(t), int64(1<<20)/100*80, "looked at after the interval")
}

// A cache within its bound is not touched (the trim is for an over-bound cache, down to 80%), and entries unused for longer than the age limit
// go on the hour as in a job's own pass.
func TestTheTickLeavesACacheWithinItsBoundAndRemovesTheAged(t *testing.T) {
	r := newTickRig(t, 10)
	aged := r.fill(t, "aged", 5, 1000, 48*time.Hour)
	fresh := r.fill(t, "fresh", 5, 1000, time.Hour)
	r.e.tickGoCaches(context.Background())
	for _, p := range aged {
		assert.NoFileExists(t, p)
	}
	for _, p := range fresh {
		assert.FileExists(t, p)
	}
}

// The tick does nothing without a deps directory, and does not make one.
func TestTheTickWithoutADepsDirectoryDoesNothing(t *testing.T) {
	e := New(Config{WorkRoot: t.TempDir()})
	e.tickGoCaches(context.Background())
}

// (e) Through the worker's beat: the sweeper is what the loop calls, and it trims in the background (the loop's round is not held for it).
func TestTheSweeperTrimsInTheBackground(t *testing.T) {
	r := newTickRig(t, 1)
	r.fill(t, "old", 600, 4096, 5*time.Hour)
	require.NoError(t, r.e.GoCacheTrimmer().Sweep(context.Background(), nil))
	assert.Eventually(t, func() bool { return r.size(t) <= int64(1<<20)/100*80 }, 5*time.Second, 10*time.Millisecond)
}

// A cache of more entries than can be listed is not left alone (codex R1): the timer removes the entries unused for longer than the age limit as
// it walks, so an idle host's cache comes back under the limit of the listing. It does not empty it, and it keeps the entries used lately.
func TestTheTickOfACacheTooBigToListRemovesTheAgedWithoutAList(t *testing.T) {
	r := newTickRig(t, 1)
	old := storeScanLimit
	storeScanLimit = 100
	t.Cleanup(func() { storeScanLimit = old })
	aged := r.fill(t, "aged", 300, 100, 48*time.Hour)
	fresh := r.fill(t, "fresh", 300, 100, time.Hour)
	_, _, err := scanGoCache(context.Background(), r.cache)
	require.ErrorIs(t, err, errOverBound, "the cache is past what can be listed")

	r.e.tickGoCaches(context.Background())

	for _, p := range aged {
		assert.NoFileExists(t, p)
	}
	for _, p := range fresh {
		assert.FileExists(t, p)
	}
	assert.DirExists(t, r.cache)
}

// The time of the last age pass is shared with the job's pass, so it is the time of a pass that went through: one that ctx ended half way
// leaves it as it was, and the job that starts next does the pass (codex R2).
func TestAnAgePassTheContextEndedHalfWayIsNotTheLastOne(t *testing.T) {
	r := newTickRig(t, 10)
	r.fill(t, "aged", 20, 100, 48*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	removed := 0
	old := goCacheRemoveFile
	goCacheRemoveFile = func(p string) error {
		removed++
		if removed == 3 {
			cancel()
		}
		return os.Remove(p)
	}
	t.Cleanup(func() { goCacheRemoveFile = old })

	r.e.tickGoCaches(ctx)

	r.e.goCache.mu.Lock()
	st := r.e.goCache.dirs[r.cache]
	r.e.goCache.mu.Unlock()
	require.NotNil(t, st)
	assert.True(t, st.pruneAt.IsZero(), "an interrupted pass is not the last pass")
	assert.Less(t, removed, 20)

	// a pass that goes through is
	goCacheRemoveFile = old
	r.e.goCache.tickAt = time.Time{}
	r.e.tickGoCaches(context.Background())
	r.e.goCache.mu.Lock()
	defer r.e.goCache.mu.Unlock()
	assert.False(t, r.e.goCache.dirs[r.cache].pruneAt.IsZero())
}

// The age pass of a cache too big to list is on the same hour as any other (codex critic): once it went through, the next ticks within the
// hour do not walk the whole directory again, and a walk the context ended can be tried again.
func TestTheAgePassOfACacheTooBigToListIsOnceAnHour(t *testing.T) {
	r := newTickRig(t, 1)
	old := storeScanLimit
	storeScanLimit = 100
	t.Cleanup(func() { storeScanLimit = old })
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	first := r.fill(t, "first", 150, 100, 48*time.Hour)
	r.e.tickGoCaches(context.Background())
	for _, p := range first {
		assert.NoFileExists(t, p)
	}

	second := r.fill(t, "second", 150, 100, 48*time.Hour) // aged again, within the hour
	now = now.Add(goCacheTickEvery)
	r.e.tickGoCaches(context.Background())
	for _, p := range second {
		assert.FileExists(t, p, "the hour of the age pass has not passed: no second walk")
	}

	now = now.Add(goCachePruneEvery)
	r.e.tickGoCaches(context.Background())
	for _, p := range second {
		assert.NoFileExists(t, p, "and after the hour it is walked again")
	}
}

func TestAWalkOfACacheTooBigToListThatTheContextEndedIsNotTheLastPass(t *testing.T) {
	r := newTickRig(t, 1)
	old := storeScanLimit
	storeScanLimit = 100
	t.Cleanup(func() { storeScanLimit = old })
	r.fill(t, "aged", 300, 100, 48*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	oldRm := goCacheRemoveFile
	goCacheRemoveFile = func(p string) error {
		if n++; n == 5 {
			cancel()
		}
		return os.Remove(p)
	}
	t.Cleanup(func() { goCacheRemoveFile = oldRm })
	r.e.tickGoCaches(ctx)
	r.e.goCache.mu.Lock()
	st := r.e.goCache.dirs[r.cache]
	r.e.goCache.mu.Unlock()
	require.NotNil(t, st)
	assert.True(t, st.pruneAt.IsZero(), "a walk the context ended is not the last pass")
}
