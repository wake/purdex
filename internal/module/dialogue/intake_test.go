package dialogue

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

// fakeReader is a convturns reader whose answers a test programs per call.
type fakeReader struct {
	mu    sync.Mutex
	fn    func(sid, from string, n int) (convturns.TurnsFromResult, error) // n: 1-based call number of that session
	calls map[string]int
	froms map[string][]string
}

func (f *fakeReader) TurnsFrom(ctx context.Context, provider, sid, from string) (convturns.TurnsFromResult, error) {
	if provider != "claude" {
		return convturns.TurnsFromResult{}, convturns.ErrUnsupportedProvider
	}
	f.mu.Lock()
	f.calls[sid]++
	n := f.calls[sid]
	f.froms[sid] = append(f.froms[sid], from)
	f.mu.Unlock()
	return f.fn(sid, from, n)
}

func (f *fakeReader) callCount(sid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[sid]
}

func (f *fakeReader) fromsOf(sid string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.froms[sid]...)
}

type writtenCall struct {
	ev     agentmod.TurnEndEvent
	res    convturns.TurnsFromResult
	wr     WriteResult
	target string
	hook   string
}

type settledCall struct {
	sid string
	at  int64
}

type harness struct {
	*env
	in *intake
	fr *fakeReader

	mu      sync.Mutex
	written []writtenCall
	settled []settledCall
	logs2   []string
}

func newHarness(t *testing.T, fn func(sid, from string, n int) (convturns.TurnsFromResult, error)) *harness {
	t.Helper()
	h := &harness{env: newEnv(t, Hooks{})}
	h.fr = &fakeReader{fn: fn, calls: map[string]int{}, froms: map[string][]string{}}
	h.in = newIntake(intakeDeps{
		turns: h.fr, writer: h.w, store: h.store,
		logf: func(f string, a ...any) {
			h.mu.Lock()
			h.logs2 = append(h.logs2, fmt.Sprintf(f, a...))
			h.mu.Unlock()
		},
		onWritten: func(ev agentmod.TurnEndEvent, res convturns.TurnsFromResult, wr WriteResult, target, hook string) {
			h.mu.Lock()
			h.written = append(h.written, writtenCall{ev, res, wr, target, hook})
			h.mu.Unlock()
		},
		onSettled: func(sid string, at int64) {
			h.mu.Lock()
			h.settled = append(h.settled, settledCall{sid, at})
			h.mu.Unlock()
		},
		timing: timing{busyDelay: time.Millisecond, settleDelay: time.Millisecond, idle: time.Hour},
	})
	t.Cleanup(h.in.Stop)
	return h
}

func (h *harness) settledCalls() []settledCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]settledCall(nil), h.settled...)
}

func (h *harness) writtenCalls() []writtenCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]writtenCall(nil), h.written...)
}

func (h *harness) logged(sub string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.logs2 {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (h *harness) waitSettled(n int) {
	h.t.Helper()
	waitFor(h.t, fmt.Sprintf("%d settled events", n), func() bool { return len(h.settledCalls()) >= n })
}

// steady answers every read with the same turns (from the cursor on).
func steady(turns ...convmodel.Turn) func(sid, from string, n int) (convturns.TurnsFromResult, error) {
	return func(sid, from string, n int) (convturns.TurnsFromResult, error) { return fromRes(turns, from), nil }
}

func stopEv(sid string, at int64) agentmod.TurnEndEvent {
	return agentmod.TurnEndEvent{SessionID: sid, At: at, Seq: at}
}

func basicTurn(id string) convmodel.Turn {
	return done(id, userItem(id+"-u", "question "+id), agentItem(id+"-a", "answer "+id))
}

// 1: one session's stuck read does not hold another session back.
func TestIntakeSessionsDoNotBlockEachOther(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if sid == sidA {
			entered <- struct{}{}
			<-gate
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidA, 1))
	<-entered
	h.in.OnTurnEnd(stopEv(sidB, 2))
	waitFor(t, "b written while a is stuck", func() bool { return fileExists(h.seg(sidB, 1)) })
	if fileExists(h.seg(sidA, 1)) {
		t.Fatal("a was written before its read returned")
	}
	close(gate)
	waitFor(t, "a written", func() bool { return fileExists(h.seg(sidA, 1)) })
	h.waitSettled(2)
}

// 2: events of one session are handled one by one, in arrival order, none merged.
func TestIntakeSameSessionKeepsOrder(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n == 1 {
			entered <- struct{}{}
			<-gate
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	<-entered
	for at := int64(2); at <= 5; at++ {
		h.in.OnTurnEnd(stopEv(sidS, at)) // must not block on the busy worker
	}
	close(gate)
	h.waitSettled(5)
	got := h.settledCalls()
	for i, c := range got {
		if c.sid != sidS || c.at != int64(i+1) {
			t.Fatalf("settled[%d] = %+v, want At %d (all: %+v)", i, c, i+1, got)
		}
	}
	if n := h.fr.callCount(sidS); n != 5 {
		t.Fatalf("reads = %d, want 5 (one per event)", n)
	}
}

// 3: a full queue drops the new event and says so.
func TestIntakeQueueFullDropsNewEvent(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n == 1 {
			entered <- struct{}{}
			<-gate
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	<-entered // the worker holds event 1; the queue is empty
	for at := int64(2); at <= 17; at++ {
		h.in.OnTurnEnd(stopEv(sidS, at))
	}
	if h.logged("queue full") {
		t.Fatal("logged a drop with room left")
	}
	h.in.OnTurnEnd(stopEv(sidS, 99)) // the 17th queued: dropped
	if !h.logged("queue full") {
		t.Fatal("a dropped event was not logged")
	}
	close(gate)
	h.waitSettled(18)
	if n := h.fr.callCount(sidS); n != 17 {
		t.Fatalf("reads = %d, want 17 (the dropped event is not processed)", n)
	}
	// the dropped event is still reported settled, so nothing waits on it
	var saw bool
	for _, c := range h.settledCalls() {
		if c.at == 99 {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("the dropped event was never reported settled: %+v", h.settledCalls())
	}
}

// 4: a busy transcript cache is retried up to three times.
func TestIntakeBusyRetries(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n <= 3 {
			return convturns.TurnsFromResult{}, convfeed.ErrBusy
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	if !fileExists(h.seg(sidS, 1)) || len(h.writtenCalls()) != 1 {
		t.Fatal("busy three times then ok: nothing written")
	}
	if n := h.fr.callCount(sidS); n != 4 {
		t.Fatalf("reads = %d, want 4", n)
	}
}

func TestIntakeBusyGivesUpButSettles(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		return convturns.TurnsFromResult{}, convfeed.ErrBusy
	})
	h.in.OnTurnEnd(stopEv(sidS, 7))
	h.waitSettled(1)
	if n := h.fr.callCount(sidS); n != 4 {
		t.Fatalf("reads = %d, want 4 (1 + 3 retries)", n)
	}
	if fileExists(h.seg(sidS, 1)) || len(h.writtenCalls()) != 0 {
		t.Fatal("wrote after giving up")
	}
	if got := h.settledCalls(); got[0].at != 7 {
		t.Fatalf("settled = %+v", got)
	}
}

func TestIntakeOtherErrorIsLoggedAndSettles(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		return convturns.TurnsFromResult{}, convfeed.ErrNotFound
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	if n := h.fr.callCount(sidS); n != 1 {
		t.Fatalf("reads = %d, want 1 (no retry)", n)
	}
	if !h.logged(sidS) {
		t.Fatal("the error was not logged")
	}
}

func runningNoText(id string) convmodel.Turn {
	tr := turn(id, convmodel.OutcomeRunning, userItem(id+"-u", "question "+id))
	tr.EndedAt = nil
	return tr
}

// 5: the Stop comes before the file has the last words: wait, then write.
func TestIntakeWaitsForTranscriptToCatchUp(t *testing.T) {
	var h *harness
	early := 0
	h = newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n <= 3 {
			if fileExists(h.seg(sidS, 1)) {
				early++
			}
			return fromRes([]convmodel.Turn{runningNoText("t1")}, from), nil
		}
		tr := turn("t1", convmodel.OutcomeRunning, userItem("t1-u", "question t1"), agentItem("t1-a", "all done"))
		tr.EndedAt = nil
		return fromRes([]convmodel.Turn{tr}, from), nil
	})
	ev := stopEv(sidS, 1000)
	ev.Text = "all done"
	h.in.OnTurnEnd(ev)
	h.waitSettled(1)
	if early != 0 {
		t.Fatal("the turn was written before the transcript caught up")
	}
	if got := h.read(sidS, 1); !strings.Contains(got, "agent: all done") {
		t.Fatalf("file lacks the last words:\n%s", got)
	}
	w := h.writtenCalls()
	if len(w) != 1 || w[0].target != "t1" || w[0].hook != "all done" {
		t.Fatalf("written = %+v", w)
	}
}

func TestIntakeForcesAfterFifteenWaitsAndLaterTextJoinsTheTurn(t *testing.T) {
	var mu sync.Mutex
	late := false
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		mu.Lock()
		defer mu.Unlock()
		tr := runningNoText("t1")
		if late {
			tr = turn("t1", convmodel.OutcomeDone, userItem("t1-u", "question t1"), agentItem("t1-a", "late words"))
		}
		return fromRes([]convmodel.Turn{tr}, from), nil
	})
	ev := stopEv(sidS, 1000)
	ev.Text = "late words"
	h.in.OnTurnEnd(ev)
	h.waitSettled(1)
	if n := h.fr.callCount(sidS); n != 16 {
		t.Fatalf("reads = %d, want 16 (1 + 15 waits)", n)
	}
	got := h.read(sidS, 1)
	if !strings.Contains(got, "user: question t1") || strings.Contains(got, "late words") {
		t.Fatalf("the forced write should hold the file as it was:\n%s", got)
	}
	if h.writtenCalls()[0].target != "" {
		t.Fatalf("target = %q", h.writtenCalls()[0].target)
	}

	mu.Lock()
	late = true
	mu.Unlock()
	h.in.OnTurnEnd(ev)
	h.waitSettled(2)
	got = h.read(sidS, 1)
	if !strings.Contains(got, "agent: late words") || strings.Count(got, "user: question t1") != 1 {
		t.Fatalf("the late words did not join the same turn:\n%s", got)
	}
	if st, _, _ := h.store.Session(sidS); st.NextTurnNo != 2 {
		t.Fatalf("the late words got a new turn number: %+v", st)
	}
}

func toolTurn(status convmodel.StepStatus) convmodel.Turn {
	return done("t1", userItem("t1-u", "run it"), stepItem("t1-s", "Bash", "ls", status), agentItem("t1-a", "ok"))
}

// 6: a tool still running holds the write back; force writes it anyway.
func TestIntakeWaitingToolRetriesUntilDone(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		st := convmodel.StepRunning
		if n >= 3 {
			st = convmodel.StepDone
		}
		return fromRes([]convmodel.Turn{toolTurn(st)}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	if n := h.fr.callCount(sidS); n != 3 {
		t.Fatalf("reads = %d, want 3", n)
	}
	if got := h.read(sidS, 1); !strings.Contains(got, "<tool Bash | ls>") {
		t.Fatalf("file:\n%s", got)
	}
	if w := h.writtenCalls(); len(w) != 1 || w[0].wr.WaitingTool {
		t.Fatalf("written = %+v", w)
	}
}

func TestIntakeWaitingToolForcesAfterFifteen(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		return fromRes([]convmodel.Turn{toolTurn(convmodel.StepRunning)}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	if n := h.fr.callCount(sidS); n != 16 {
		t.Fatalf("reads = %d, want 16", n)
	}
	if !fileExists(h.seg(sidS, 1)) || len(h.writtenCalls()) != 1 {
		t.Fatal("force did not write")
	}
}

// 7: a rebuild the turns in hand cannot serve is redone from the whole conversation.
func TestIntakeNeedAllRereadsEverything(t *testing.T) {
	turns := []convmodel.Turn{basicTurn("t1"), basicTurn("t2")}
	h := newHarness(t, steady(turns...))
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	before := h.read(sidS, 1)
	if err := os.Remove(h.seg(sidS, 1)); err != nil {
		t.Fatal(err)
	}
	h.in.OnTurnEnd(stopEv(sidS, 2))
	h.waitSettled(2)
	if got := h.read(sidS, 1); got != before {
		t.Fatalf("rebuilt file differs:\n%s\n---\n%s", got, before)
	}
	if froms := h.fr.fromsOf(sidS); strings.Join(froms, ",") != ",t2," {
		t.Fatalf("read cursors = %q, want first, the cursor, then the whole conversation", froms)
	}
	if len(h.writtenCalls()) != 2 {
		t.Fatalf("written = %d calls", len(h.writtenCalls()))
	}
}

// 8: the hooks get the target, the write result and the hook's text.
func TestIntakeOnWrittenGetsTargetAndResult(t *testing.T) {
	h := newHarness(t, steady(basicTurn("t1")))
	ev := stopEv(sidS, 5)
	ev.Text = "answer t1"
	h.in.OnTurnEnd(ev)
	h.waitSettled(1)
	w := h.writtenCalls()
	if len(w) != 1 {
		t.Fatalf("written = %+v", w)
	}
	if w[0].target != "t1" || w[0].hook != "answer t1" || w[0].ev.At != 5 || !w[0].wr.First ||
		len(w[0].wr.Turns) != 1 || w[0].wr.Turns[0].ID != "t1" || w[0].wr.Turns[0].No != 1 || len(w[0].res.Turns) != 1 {
		t.Fatalf("written = %+v", w[0])
	}
}

// the worker ends when idle, and a later event starts a new one.
func TestIntakeWorkerEndsWhenIdleAndRestarts(t *testing.T) {
	h := newHarness(t, steady(basicTurn("t1")))
	h.in.d.timing.idle = 10 * time.Millisecond
	h.in.OnTurnEnd(stopEv(sidS, 1))
	h.waitSettled(1)
	waitFor(t, "the idle worker to end", func() bool { return h.in.workers() == 0 })
	h.in.OnTurnEnd(stopEv(sidS, 2))
	h.waitSettled(2)
	if n := h.fr.callCount(sidS); n != 2 {
		t.Fatalf("reads = %d", n)
	}
}

// Stop: no write after it returns, an event after it is ignored.
func TestIntakeStopEndsWorkersAndIgnoresLaterEvents(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n == 1 {
			entered <- struct{}{}
			<-gate
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv(sidS, 1))
	<-entered
	stopped := make(chan struct{})
	go func() { h.in.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while an event was being handled")
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	<-stopped
	if fileExists(h.seg(sidS, 1)) {
		t.Fatal("a write happened after Stop")
	}
	h.in.OnTurnEnd(stopEv(sidS, 2))
	time.Sleep(20 * time.Millisecond)
	if n := h.fr.callCount(sidS); n != 1 {
		t.Fatalf("an event after Stop was read: %d reads", n)
	}
}
