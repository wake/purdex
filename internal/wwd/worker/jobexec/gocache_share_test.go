package jobexec

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #3879: the timed trim of a cache that a job holds. After a trim that ran with the exclusive lock the cache holds only what was written in
// the last hour or so, so a fallback that takes only entries unused for two hours finds none: the cache grew past its bound until a tick
// happened to find the lock free (about once an hour on mlab) and nothing in the log said so.

func wasRemoved(paths []string) int {
	n := 0
	for _, p := range paths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			n++
		}
	}
	return n
}

// captureLog collects the log lines of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// holdUntilReleased is hold with a release the test calls when the job is to end.
func (r *tickRig) holdUntilReleased(t *testing.T) (release func()) {
	t.Helper()
	f, err := os.OpenFile(r.lock, os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_SH))
	t.Cleanup(func() { f.Close() })
	return func() { f.Close() }
}

func TestTheShareAgeFallsFromTwoHoursAtTheBoundToTheFloorOver(t *testing.T) {
	max := int64(1000)
	assert.Equal(t, goCacheShareMinAge, goCacheShareAge(500, max), "within the bound")
	assert.Equal(t, goCacheShareMinAge, goCacheShareAge(1000, max), "at the bound")
	assert.Equal(t, goCacheShareFloor, goCacheShareAge(1250, max), "a quarter over")
	assert.Equal(t, goCacheShareFloor, goCacheShareAge(5000, max), "far over: never below the floor")
	mid := goCacheShareAge(1125, max)
	assert.Less(t, mid, goCacheShareMinAge)
	assert.Greater(t, mid, goCacheShareFloor)
	assert.Equal(t, 75*time.Minute, goCacheShareFloor, "an hour of Go's file-time step and a quarter hour more")
}

// (a) The case of mlab: a held cache, over its bound, whose entries were all written within the last two hours. The old fallback removed
// nothing; now the entries idle for more than the floor go, the newer ones stay.
func TestAHeldCacheOverItsBoundGivesUpEntriesIdleForMoreThanTheFloor(t *testing.T) {
	r := newTickRig(t, 1)
	older := r.fill(t, "older", 300, 4096, 100*time.Minute) // 1.2 MiB, idle 100 min: under two hours, over the floor
	fresh := r.fill(t, "fresh", 100, 4096, 30*time.Minute)  // 0.4 MiB
	r.hold(t)
	require.Greater(t, r.size(t), int64(1<<20))

	r.e.tickGoCaches(context.Background())

	assert.Greater(t, wasRemoved(older), 0, "idle for 100 minutes while the cache is far over: they go")
	assert.Zero(t, wasRemoved(fresh), "entries idle for less than the floor are what a job may be about to open")
}

// (b) Nothing idle for less than the floor goes, however far over the bound.
func TestAHeldCacheNeverGivesUpEntriesIdleForLessThanTheFloor(t *testing.T) {
	r := newTickRig(t, 1)
	young := r.fill(t, "young", 1000, 4096, 70*time.Minute) // 4 MiB, four times the bound
	r.hold(t)

	r.e.tickGoCaches(context.Background())

	assert.Zero(t, wasRemoved(young))
}

// Just over the bound the two hours still hold: a cache that is only a little over is not taken from the entries used within them.
func TestAHeldCacheJustOverItsBoundKeepsTheTwoHours(t *testing.T) {
	r := newTickRig(t, 1)
	mid := r.fill(t, "mid", 270, 4096, 90*time.Minute) // 1.08 MiB: 8% over
	r.hold(t)
	require.Greater(t, r.size(t), int64(1<<20))

	r.e.tickGoCaches(context.Background())

	assert.Zero(t, wasRemoved(mid), "8% over, the age is still more than 90 minutes")
}

// (c) A tick that finds the cache over its bound says so, also when it removed nothing.
func TestATickOverTheBoundLogsAlsoWhenItRemovedNothing(t *testing.T) {
	r := newTickRig(t, 1)
	r.fill(t, "young", 1000, 4096, 10*time.Minute)
	r.hold(t)
	buf := captureLog(t)

	r.e.tickGoCaches(context.Background())

	out := buf.String()
	assert.Contains(t, out, "on the timer", out)
	assert.Contains(t, out, "0 entries", out)
	assert.Contains(t, out, "held", out, "whether the exclusive lock was had")
	assert.Contains(t, out, "bound 1 MB", out)
}

func TestATickOfACacheWithinItsBoundSaysNothing(t *testing.T) {
	r := newTickRig(t, 10)
	r.fill(t, "a", 10, 4096, 10*time.Minute)
	buf := captureLog(t)

	r.e.tickGoCaches(context.Background())

	assert.False(t, strings.Contains(buf.String(), "build cache"), buf.String())
}

// (d) A trim that could not get the cache back to its target for want of the lock is tried again at the next beat, not after fifteen minutes;
// one that got there goes back to the interval.
func TestATrimThatNeededTheLockIsTriedAgainAtTheNextBeat(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	r.fill(t, "young", 1000, 4096, 10*time.Minute) // over, and nothing of it may go while a job holds the cache
	release := r.holdUntilReleased(t)

	r.e.tickGoCaches(context.Background())
	require.Greater(t, r.size(t), int64(1<<20))

	now = now.Add(goCacheTickRetry + time.Second)
	assert.True(t, r.e.goCacheTickDue(), "still over and the lock was not had: the next beat looks again")

	release() // the job ends
	r.e.tickGoCaches(context.Background())
	assert.LessOrEqual(t, r.size(t), int64(1<<20)/100*80, "the free lock lets any entry go")

	now = now.Add(goCacheTickRetry + time.Second)
	assert.False(t, r.e.goCacheTickDue(), "at its target: back to the interval")
	now = now.Add(goCacheTickEvery)
	assert.True(t, r.e.goCacheTickDue())
}

// A clock that steps back after a retry was scheduled does not push the retry out (codex R2): it is a flag, not an earlier time of the last look.
func TestARetryStaysDueWhenTheClockStepsBack(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	r.fill(t, "young", 1000, 4096, 10*time.Minute)
	r.hold(t)
	r.e.tickGoCaches(context.Background())
	require.True(t, r.e.goCache.tickRetry)

	now = now.Add(-10 * time.Minute)
	assert.True(t, r.e.goCacheTickDue(), "10 minutes back: later than the look by a negative time, so due at once")
	now = now.Add(10*time.Minute + goCacheTickRetry + time.Second)
	assert.True(t, r.e.goCacheTickDue())
}

// A lock that cannot be taken for a reason other than a job holding it (here the lock file is a directory) is not a held cache: it is said, with
// the reason, and the tick does not come back every minute for it (codex R2).
func TestALockFaultIsNotARetryAndIsSaid(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	require.NoError(t, os.MkdirAll(r.lock, 0o755)) // OpenFile of it for writing fails
	old := r.fill(t, "old", 300, 4096, 3*time.Hour)
	r.fill(t, "young", 700, 4096, 10*time.Minute)
	buf := captureLog(t)

	r.e.tickGoCaches(context.Background())

	assert.Greater(t, wasRemoved(old), 0, "without a lock the idle ones still go, as for a held cache")
	assert.Contains(t, buf.String(), "other than a job holding it", buf.String())
	assert.False(t, r.e.goCache.tickRetry, "no retry every minute for a fault")
	now = now.Add(goCacheTickRetry + time.Second)
	assert.False(t, r.e.goCacheTickDue())
}

// (d2) A trim of several chunks that had the exclusive lock for the first and lost it to a job for the last still wants the next beat (codex R1):
// what decides is the lock the last chunk found, not whether one had it earlier.
func TestATrimThatLostTheLockForItsLastChunkIsTriedAgainAtTheNextBeat(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	r.fill(t, "old", 100, 4096, 5*time.Hour)      // 0.4 MiB, idle for hours
	r.fill(t, "young", 900, 4096, 10*time.Minute) // 3.6 MiB: only a free cache gives these up
	oldChunk, oldYield, oldBetween := goCacheTickChunk, goCacheTickYield, goCacheTickBetween
	goCacheTickChunk, goCacheTickYield = 50, time.Millisecond
	t.Cleanup(func() { goCacheTickChunk, goCacheTickYield, goCacheTickBetween = oldChunk, oldYield, oldBetween })
	var held *os.File
	goCacheTickBetween = func() { // a job starts after the first chunk and keeps the cache
		if held != nil {
			return
		}
		f, err := os.OpenFile(r.lock, os.O_CREATE|os.O_RDWR, 0o600)
		require.NoError(t, err)
		require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_SH))
		held = f
	}
	t.Cleanup(func() {
		if held != nil {
			held.Close()
		}
	})

	r.e.tickGoCaches(context.Background())

	require.NotNil(t, held, "the trim took more than one chunk")
	require.Greater(t, r.size(t), int64(1<<20)/100*80, "the young entries stayed")
	now = now.Add(goCacheTickRetry + time.Second)
	assert.True(t, r.e.goCacheTickDue(), "the first chunk had the lock, the last did not: look again at the next beat")
}

// (e) Jobs that come and go: shared locks all the time, never a free moment. The cache is brought back under its bound within a few beats.
func TestACacheNeverFreeIsBroughtUnderItsBoundWithinFewBeats(t *testing.T) {
	r := newTickRig(t, 1)
	now := time.Now()
	r.e.goCache.now = func() time.Time { return now }
	r.fill(t, "grown", 1200, 4096, 100*time.Minute) // 4.7 MiB written over the last two hours: the mlab case
	r.hold(t)

	for beat := 0; beat < 5 && r.size(t) > int64(1<<20); beat++ {
		r.e.tickGoCaches(context.Background())
		now = now.Add(time.Minute + goCacheTickRetry)
	}

	assert.LessOrEqual(t, r.size(t), int64(1<<20), "back within the bound without ever having had the exclusive lock")
}
