package agent

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// stepClock is the hold timer's clock. The slot reads it twice per hold
// (taking the mutex, releasing it): the first reading is the current time, the
// second is step later and becomes the current time. Setting step to d before
// an emit therefore makes that hold last exactly d.
type stepClock struct {
	mu    sync.Mutex
	t     time.Time
	step  time.Duration
	reads int
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reads%2 == 1 {
		c.t = c.t.Add(c.step)
	}
	c.reads++
	return c.t
}

// syncBuf is a log sink the slot and the test can share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureSlotLog(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
}

func holdBuckets(m *Module) [4]uint64 {
	h := &m.emit.hold
	return [4]uint64{h.buckets[0].Load(), h.buckets[1].Load(), h.buckets[2].Load(), h.buckets[3].Load()}
}

func lines(s, sub string) int { return strings.Count(s, sub) }

// TestEmitSlotHold_LogsOver250msAndCountsBuckets: one hold in each bucket.
// Only the one that reaches 250 ms is logged, and the line carries the bucket
// counts so far.
func TestEmitSlotHold_LogsOver250msAndCountsBuckets(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	clk := &stepClock{t: time.Unix(1_000_000, 0)}
	r.m.emit.hold.clock = clk.now
	out := captureSlotLog(t)

	for _, d := range []time.Duration{5 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond, 300 * time.Millisecond} {
		clk.step = d
		if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) {
			t.Fatalf("emit (%s) did not go out", d)
		}
		if slow := lines(out.String(), "emit slot held"); (d >= holdWarn) != (slow == 1) || slow > 1 {
			t.Fatalf("after a %s hold: %d slow-hold lines (log: %s)", d, slow, out.String())
		}
	}
	if got := holdBuckets(r.m); got != [4]uint64{1, 1, 1, 1} {
		t.Fatalf("buckets = %v, want one hold in each", got)
	}
	if n := r.m.emit.hold.total.Load(); n != 4 {
		t.Fatalf("total = %d, want 4", n)
	}
	if mx := time.Duration(r.m.emit.hold.maxNs.Load()); mx != 300*time.Millisecond {
		t.Fatalf("max = %s, want 300ms", mx)
	}
	for _, want := range []string{
		"emit slot held 300ms", `session="work"`, "kind=hook",
		"<10ms=1", "<50ms=1", "<250ms=1", ">=250ms=1", "total=4", "max=300ms",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("log lacks %q:\n%s", want, out.String())
		}
	}
}

// TestEmitSlotHold_SnapshotIsMeasuredToo: the subscribe-time replay holds the
// same mutex, so its hold counts and is labelled.
func TestEmitSlotHold_SnapshotIsMeasuredToo(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	clk := &stepClock{t: time.Unix(1_000_000, 0), step: 400 * time.Millisecond}
	r.m.emit.hold.clock = clk.now
	out := captureSlotLog(t)

	r.m.sendSnapshot(r.sub)

	if got := holdBuckets(r.m); got != [4]uint64{0, 0, 0, 1} {
		t.Fatalf("buckets = %v, want the snapshot's 400ms hold in the last", got)
	}
	if !strings.Contains(out.String(), "kind=snapshot") {
		t.Fatalf("no slow-hold line for the snapshot:\n%s", out.String())
	}
}

// TestEmitSlotHold_SummaryEveryFiveMinutes: in dev mode the emit that
// finishes five minutes after the first hold writes a summary line; one that
// finishes sooner writes none.
func TestEmitSlotHold_SummaryEveryFiveMinutes(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	clk := &stepClock{t: time.Unix(1_000_000, 0), step: time.Millisecond}
	r.m.emit.hold.clock = clk.now
	out := captureSlotLog(t)

	emit := func() {
		if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) {
			t.Fatal("emit did not go out")
		}
	}
	emit()
	emit()
	if n := lines(out.String(), "emit slot summary"); n != 0 {
		t.Fatalf("%d summaries inside the first five minutes:\n%s", n, out.String())
	}
	clk.step = 6 * time.Minute // this hold ends six minutes after the first began
	emit()
	if n := lines(out.String(), "emit slot summary"); n != 1 {
		t.Fatalf("%d summaries after five minutes, want 1:\n%s", n, out.String())
	}
	clk.step = time.Millisecond
	emit()
	if n := lines(out.String(), "emit slot summary"); n != 1 {
		t.Fatalf("a second summary right after the first:\n%s", out.String())
	}
}
