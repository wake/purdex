package fleetmod

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bad = `printf '@@f\n@@os\nBogus\n@@end\n'`

func stopWithin(t *testing.T, s *session) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("stop did not return")
	}
}

// Frames then silence: offline/timeout at 5 s with the session kept, killed and restarted only at 30 s.
func TestSilenceIsOfflineAt5sAndKilledAt30s(t *testing.T) {
	path, dir := fakeSSH(t, `cat "$D/frames.txt"; exec sleep 600`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	defer stopWithin(t, s)

	eventually(t, "first reading", func() bool { return sk.readingCount() == 1 && sk.last() == "ok/" })
	pid := pidOf(t, dir, "pid")

	fc.Advance(5 * time.Second)
	eventually(t, "offline", func() bool { return sk.last() == "offline/timeout" })
	time.Sleep(200 * time.Millisecond)
	assert.True(t, alive(pid), "the session is kept at 5 s")
	assert.Equal(t, 1, startsOf(dir))

	fc.Advance(25 * time.Second)
	eventually(t, "killed at 30 s", func() bool { return !alive(pid) })
	eventually(t, "backoff armed", func() bool { return fc.armed() == 1 })
	fc.Advance(5 * time.Second)
	eventually(t, "restarted", func() bool { return startsOf(dir) == 2 })
}

// exit 255 with a permission error is error/auth; stderr never reaches the sink, and the log gets it once an hour.
func TestExit255AuthStderrOnlyInTheLogOncePerHour(t *testing.T) {
	path, dir := fakeSSH(t, `echo "wake@host: Permission denied (publickey)." >&2; exit 255`)
	fc, sk := newFakeClock(), &recSink{}
	var mu sync.Mutex
	var logs []string
	logf := func(f string, a ...any) { mu.Lock(); logs = append(logs, f); mu.Unlock() }
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, logf), "mserv")
	defer stopWithin(t, s)

	eventually(t, "auth", func() bool { return sk.last() == "error/auth" })
	eventually(t, "logged", func() bool { mu.Lock(); defer mu.Unlock(); return len(logs) == 1 })
	eventually(t, "armed", func() bool { return fc.armed() == 1 })
	fc.Advance(5 * time.Second)
	eventually(t, "second try", func() bool { return startsOf(dir) == 2 })
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	assert.Len(t, logs, 1, "same host and code: one line per hour")
	mu.Unlock()
	for _, st := range sk.snapshot() {
		assert.NotContains(t, st, "Permission")
	}
}

// The wait between starts is 5, 10, 20 s, and a good frame sends it back to 5 s.
func TestBackoffDoublesAndResetsOnAGoodFrame(t *testing.T) {
	path, dir := fakeSSH(t, `if [ -f "$D/good" ]; then cat "$D/frames.txt"; exit 0; fi
echo "ssh: connect to host x port 22: Connection refused" >&2; exit 255`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	defer stopWithin(t, s)

	starts := func(n int) { eventually(t, "starts", func() bool { return startsOf(dir) == n }) }
	// a failing run makes two timers (its silence watch, then the wait), a good one three
	made := func(n int) { eventually(t, "wait armed", func() bool { return fc.created() == n }) }
	stays := func(n int) { time.Sleep(150 * time.Millisecond); assert.Equal(t, n, startsOf(dir)) }

	starts(1)
	assert.Eventually(t, func() bool { return sk.last() == "offline/unreachable" }, 5*time.Second, 10*time.Millisecond)
	made(2)
	fc.Advance(5 * time.Second)
	starts(2)
	made(4)
	fc.Advance(5 * time.Second)
	stays(2) // the second wait is 10 s
	fc.Advance(5 * time.Second)
	starts(3)
	made(6)
	fc.Advance(19 * time.Second)
	stays(3) // the third wait is 20 s
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good"), nil, 0o644))
	fc.Advance(1 * time.Second)
	starts(4)
	eventually(t, "good frame", func() bool { return sk.readingCount() == 1 })
	made(9)
	fc.Advance(5 * time.Second) // reset: 5 s again, not 40
	starts(5)
}

// A grandchild (a ProxyCommand) holding the pipe must not keep stop from returning, nor outlive it.
func TestStopFreesAPipeHeldByAGrandchild(t *testing.T) {
	path, dir := fakeSSH(t, `sleep 600 &
echo $! > "$D/gc"
cat "$D/frames.txt"
wait`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	eventually(t, "reading", func() bool { return sk.readingCount() == 1 })
	gc, sshPid := pidOf(t, dir, "gc"), pidOf(t, dir, "pid")
	require.True(t, alive(gc))

	stopWithin(t, s)
	eventually(t, "grandchild gone", func() bool { return !alive(gc) })
	eventually(t, "ssh gone", func() bool { return !alive(sshPid) })
}

// A grandchild holding only stderr (stdout is already closed) must still be killed with the group.
func TestStopKillsAGrandchildHoldingOnlyStderr(t *testing.T) {
	path, dir := fakeSSH(t, `sleep 600 >/dev/null &
echo $! > "$D/gc"
cat "$D/frames.txt"
exec 1>&-
wait`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	eventually(t, "reading", func() bool { return sk.readingCount() == 1 })
	gc, sshPid := pidOf(t, dir, "gc"), pidOf(t, dir, "pid")
	time.Sleep(100 * time.Millisecond) // stdout is closed: the reader is done
	require.True(t, alive(gc))

	stopWithin(t, s)
	eventually(t, "grandchild gone", func() bool { return !alive(gc) })
	eventually(t, "ssh gone", func() bool { return !alive(sshPid) })
}

// lateClock is a fakeClock whose reading right after lateNext() is the true time and every reading after it is `by` later:
// a frame stamped on arrival and taken `by` after, i.e. a stale one.
type lateClock struct {
	*fakeClock
	mu      sync.Mutex
	pending bool
	skew    time.Duration
	by      time.Duration
}

func (c *lateClock) lateNext() { c.mu.Lock(); c.pending = true; c.mu.Unlock() }

func (c *lateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		c.pending, c.skew = false, c.by
		return c.fakeClock.Now()
	}
	return c.fakeClock.Now().Add(c.skew)
}

// A frame dropped as stale proves nothing: it does not move the silence clock, so the host still reads offline 5 s after
// the last good frame.
func TestStaleFrameDoesNotResetTheSilenceClock(t *testing.T) {
	path, dir := fakeSSH(t, `cat "$D/frames.txt"
while [ ! -f "$D/go" ]; do sleep 0.05; done
cat "$D/frame1.txt"; exec sleep 600`)
	fc, sk := newFakeClock(), &recSink{}
	lc := &lateClock{fakeClock: fc, by: 3 * time.Second} // the default MaxAge is 2 s
	cfg := sessionFor(t, path, fc, sk, nil)
	cfg.Clock = lc
	s := startSession(t.Context(), cfg, "mserv")
	defer stopWithin(t, s)

	eventually(t, "first reading", func() bool { return sk.readingCount() == 1 })
	fc.Advance(4 * time.Second) // 1 s before the offline mark
	lc.lateNext()               // (the skew set here is permanent: every later reading of the clock is 3 s late) the next reading of the clock is the reader stamping the second frame
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), nil, 0o644))
	time.Sleep(300 * time.Millisecond) // the second frame arrived and was dropped
	assert.Equal(t, 1, sk.readingCount(), "a stale frame reached the sink")

	fc.Advance(time.Second) // 5 s since the last good frame
	eventually(t, "offline", func() bool { return sk.last() == "offline/timeout" })
}

// A grandchild that holds only stderr and ignores TERM is killed too: the group gets KILL after the grace.
func TestStopKillsAStderrGrandchildThatIgnoresTerm(t *testing.T) {
	path, dir := fakeSSH(t, `(trap '' TERM; exec sleep 600) >/dev/null &
echo $! > "$D/gc"
cat "$D/frames.txt"
exec 1>&-
wait`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	eventually(t, "reading", func() bool { return sk.readingCount() == 1 })
	gc := pidOf(t, dir, "gc")
	time.Sleep(100 * time.Millisecond) // stdout is closed: the reader is done
	require.True(t, alive(gc))

	stopWithin(t, s)
	eventually(t, "grandchild gone", func() bool { return !alive(gc) })
}

// Five unparsable frames in a row restart the session; a good frame in between resets the count.
func TestFiveBadFramesRestartAndAGoodOneResets(t *testing.T) {
	t.Run("five in a row", func(t *testing.T) {
		path, dir := fakeSSH(t, bad+";"+bad+";"+bad+";"+bad+";"+bad+"; exec sleep 600")
		fc, sk := newFakeClock(), &recSink{}
		s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
		defer stopWithin(t, s)
		eventually(t, "parse error", func() bool { return sk.last() == "error/parse" })
		eventually(t, "armed", func() bool { return fc.armed() == 1 })
		fc.Advance(5 * time.Second)
		eventually(t, "restart", func() bool { return startsOf(dir) == 2 })
	})
	t.Run("a good frame resets", func(t *testing.T) {
		path, dir := fakeSSH(t, bad+";"+bad+";"+bad+";"+bad+`; cat "$D/frame1.txt"; `+bad+";"+bad+";"+bad+";"+bad+"; exec sleep 600")
		fc, sk := newFakeClock(), &recSink{}
		s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
		defer stopWithin(t, s)
		eventually(t, "reading", func() bool { return sk.readingCount() == 1 })
		time.Sleep(300 * time.Millisecond)
		assert.False(t, sk.has("error/parse"))
		assert.Equal(t, 1, startsOf(dir))
	})
}

// An alias that left the SSH config is not started at all.
func TestAliasGoneStartsNothing(t *testing.T) {
	path, dir := fakeSSH(t, `exec sleep 600`)
	fc, sk := newFakeClock(), &recSink{}
	cfg := sessionFor(t, path, fc, sk, nil)
	cfg.Valid = func(string, string) bool { return false }
	s := startSession(t.Context(), cfg, "mserv")
	defer stopWithin(t, s)
	eventually(t, "alias_gone", func() bool { return sk.last() == "error/alias_gone" })
	assert.Equal(t, 0, startsOf(dir))
}

// Stop cuts a backoff wait short.
func TestStopCutsTheBackoffWait(t *testing.T) {
	path, _ := fakeSSH(t, `exit 255`)
	fc, sk := newFakeClock(), &recSink{}
	s := startSession(t.Context(), sessionFor(t, path, fc, sk, nil), "mserv")
	eventually(t, "waiting", func() bool { return fc.armed() == 1 })
	stopWithin(t, s)
}

// The slot keeps the newest frame only, and a frame older than 2 s when taken is dropped.
func TestSlotNewestWinsAndDropsOldFrames(t *testing.T) {
	sl := newSlot()
	t0 := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		it := frameItem{at: t0}
		it.fast.NCPU = i
		sl.put(it)
	}
	it, ok := sl.take(t0.Add(time.Second), 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, 3, it.fast.NCPU, "only the newest")
	_, ok = sl.take(t0.Add(time.Second), 2*time.Second)
	assert.False(t, ok, "taken once")

	sl.put(frameItem{at: t0})
	_, ok = sl.take(t0.Add(2*time.Second+time.Millisecond), 2*time.Second)
	assert.False(t, ok, "older than 2 s is dropped")
}
