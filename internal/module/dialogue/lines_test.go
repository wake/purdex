package dialogue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/workbooklines"
)

// The waiter the push hold uses (ported from the workbook's lines_test.go; the store is a fake source here, and
// TestAwait_RealStore runs the same path over dialogue.db).

type fakeSource struct {
	mu   sync.Mutex
	rows []summaryLine
	at   []int64 // turn_at of rows[i]
	sid  []string
}

func (f *fakeSource) put(sid string, turnAt int64, l summaryLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows, f.at, f.sid = append(f.rows, l), append(f.at, turnAt), append(f.sid, sid)
}

// set replaces the row with this id (a state change); the caller then wakes the waiter as the channel would.
func (f *fakeSource) set(l summaryLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].ID == l.ID {
			f.rows[i] = l
		}
	}
}

func (f *fakeSource) closest(sid string, at, before, after int64) (summaryLine, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	best, found := -1, false
	for i := range f.rows {
		if f.sid[i] != sid || f.at[i] < at-before || f.at[i] > at+after {
			continue
		}
		if !found || abs64(f.at[i]-at) < abs64(f.at[best]-at) || (abs64(f.at[i]-at) == abs64(f.at[best]-at) && f.rows[i].ID > f.rows[best].ID) {
			best, found = i, true
		}
	}
	if !found {
		return summaryLine{}, false, nil
	}
	return f.rows[best], true, nil
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func linesKit(src *fakeSource) *PushLines {
	return NewPushLines(func() summarySource { return src })
}

func doneLine(id int64, title, push string) summaryLine {
	return summaryLine{ID: id, ConvKey: "s1", State: stateDone, Title: title, Push: push, PushReadyAt: 1}
}

type awaited struct {
	line workbooklines.Line
	ok   bool
	took time.Duration
}

func awaitAsync(pl *PushLines, ctx context.Context, sid string, since int64, wait time.Duration) <-chan awaited {
	out := make(chan awaited, 1)
	go func() {
		start := time.Now()
		l, ok := pl.Await(ctx, sid, since, time.Now().Add(wait))
		out <- awaited{l, ok, time.Since(start)}
	}()
	return out
}

func awaitResult(t *testing.T, ch <-chan awaited, within time.Duration) awaited {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(within):
		t.Fatal("Await did not return")
		return awaited{}
	}
}

func TestPushLinesIsTheWorkbookLines(t *testing.T) {
	var _ workbooklines.Lines = (*PushLines)(nil)
}

// The line may already be there when the hold starts.
// Mutation gate: skip the store check and only wait for a wake -> red.
func TestAwait_LineAlreadyWritten(t *testing.T) {
	src := &fakeSource{}
	src.put("s1", 1000, doneLine(7, "功能甲", "好了"))
	a := awaitResult(t, awaitAsync(linesKit(src), context.Background(), "s1", 1000, 5*time.Second), time.Second)
	if !a.ok || a.line.Thing != "功能甲" || a.line.Push != "好了" || a.line.ConvKey != "s1" || a.line.EntryID != 7 {
		t.Fatalf("line = %+v ok=%v", a.line, a.ok)
	}
}

// Ready before the deadline: woken, no polling.
func TestAwait_ReadyBeforeTheDeadline(t *testing.T) {
	src := &fakeSource{}
	pl := linesKit(src)
	src.put("s1", 1000, summaryLine{ID: 1, State: statePending})
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	src.set(doneLine(1, "功能甲", "好了"))
	pl.wake()
	a := awaitResult(t, ch, 2*time.Second)
	if !a.ok || a.line.Push != "好了" {
		t.Fatalf("got %+v", a)
	}
}

// A failed or skipped row, or one whose push line was dropped, answers false at once.
func TestAwait_FalseAtOnceWhenThereIsNoLine(t *testing.T) {
	cases := map[string]summaryLine{
		"failed":     {ID: 1, State: stateFailed, Reason: "api"},
		"no push":    {ID: 1, State: stateDone, Title: "事", Push: "", PushReadyAt: 1},
		"skip:model": {ID: 1, State: stateSkipped, Reason: "model"},
		"no_mod":     {ID: 1, State: stateSkipped, Reason: reasonNoMod},
	}
	for name, final := range cases {
		t.Run(name, func(t *testing.T) {
			src := &fakeSource{}
			pl := linesKit(src)
			src.put("s1", 1000, summaryLine{ID: 1, State: statePending})
			ch := awaitAsync(pl, context.Background(), "s1", 1000, 30*time.Second)
			time.Sleep(50 * time.Millisecond)
			src.set(final)
			pl.wake()
			if a := awaitResult(t, ch, 2*time.Second); a.ok {
				t.Fatalf("got a line: %+v", a.line)
			}
		})
	}
}

// A done row without push_ready_at is not final yet: the hold keeps waiting.
func TestAwait_DoneWithoutReadyAtKeepsWaiting(t *testing.T) {
	src := &fakeSource{}
	src.put("s1", 1000, summaryLine{ID: 1, State: stateDone, Title: "事", Push: "推", PushReadyAt: 0})
	a := awaitResult(t, awaitAsync(linesKit(src), context.Background(), "s1", 1000, 200*time.Millisecond), 3*time.Second)
	if a.ok || a.took < 150*time.Millisecond {
		t.Fatalf("got %+v", a)
	}
}

// An event whose intake made no summary (a turn with nothing to summarise) lets the hold go at once.
// Mutation gate: drop intakeDone's wake or its record -> the wait runs to its deadline -> red.
func TestAwait_AnEventWithNoSummaryReleasesTheHold(t *testing.T) {
	src := &fakeSource{}
	pl := linesKit(src)
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	pl.intakeDone("s1", 1000)
	if a := awaitResult(t, ch, 2*time.Second); a.ok {
		t.Fatalf("got %+v", a)
	}
}

// An older turn's row does not satisfy a newer Stop: it waits, and the deadline ends it.
// Mutation gate: drop the turn_at bound from the match -> the old row answers -> red.
func TestAwait_AnOlderTurnDoesNotSatisfyANewerStop(t *testing.T) {
	src := &fakeSource{}
	src.put("s1", 1000, doneLine(1, "舊的事", "舊的推播"))
	pl := linesKit(src)
	a := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 60_000, 300*time.Millisecond), 3*time.Second)
	if a.ok {
		t.Fatalf("the old row answered a new Stop: %+v", a.line)
	}
	if a.took < 250*time.Millisecond {
		t.Fatalf("returned after %v: it should have waited for its deadline", a.took)
	}
	// within the 2 s slack the row still counts (the two stamps come from different paths)
	if a := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 2999, 5*time.Second), time.Second); !a.ok {
		t.Fatal("the slack of 2 s was not applied")
	}
}

func TestAwait_DeadlineAndCancel(t *testing.T) {
	pl := linesKit(&fakeSource{})
	if a := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 1000, 200*time.Millisecond), 2*time.Second); a.ok || a.took < 150*time.Millisecond {
		t.Fatalf("deadline: %+v", a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := awaitAsync(pl, ctx, "s1", 1000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if a := awaitResult(t, ch, time.Second); a.ok {
		t.Fatal("a line after cancel")
	}
	// the module is off: nothing to wait for
	off := NewPushLines(func() summarySource { return nil })
	if a := awaitResult(t, awaitAsync(off, context.Background(), "s1", 1000, 30*time.Second), time.Second); a.ok {
		t.Fatal("a line from a stopped module")
	}
}

// Two Stops of one session a moment apart each get their own row: the match is the nearest turn_at within the bounds.
// Mutation gate: take the newest row in range (the old match) -> both waiters get the second -> red.
func TestAwait_TwoRapidStopsEachFindTheirOwnRow(t *testing.T) {
	src := &fakeSource{}
	src.put("s1", 10_000, doneLine(1, "事", "推1"))
	src.put("s1", 10_300, doneLine(2, "事", "推2"))
	pl := linesKit(src)
	a := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 10_000, time.Second), time.Second)
	b := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 10_300, time.Second), time.Second)
	if !a.ok || a.line.EntryID != 1 || !b.ok || b.line.EntryID != 2 {
		t.Fatalf("first stop got %+v, second %+v", a.line, b.line)
	}
	if c := awaitResult(t, awaitAsync(pl, context.Background(), "s1", 60_000, 200*time.Millisecond), 3*time.Second); c.ok {
		t.Fatalf("a later stop took an old row: %+v", c.line)
	}
}

// An earlier event of the session that made no row must not release the hold of a later Stop whose own intake has not
// happened yet: intake times are matched to the push's stamp, not just "some intake at or after".
// Mutation gate: a monotonic per-session time -> the hold is released early -> red.
func TestAwait_AnEarlierIntakeDoesNotReleaseALaterStop(t *testing.T) {
	pl := linesKit(&fakeSource{})
	pl.intakeDone("s1", 5_000)
	ch := awaitAsync(pl, context.Background(), "s1", 7_000, 5*time.Second)
	select {
	case a := <-ch:
		t.Fatalf("released by the earlier event: %+v", a)
	case <-time.After(300 * time.Millisecond):
	}
	pl.intakeDone("s1", 7_000)
	if a := awaitResult(t, ch, 3*time.Second); a.ok {
		t.Fatal("no model ran, so no line")
	}
}

// The intake that happened before the row exists must not release a hold whose row is pending: only a final row or a
// missing row ends it (the row exists, so the "looked at, no summary" shortcut does not apply).
func TestAwait_IntakeDoneDoesNotReleaseAPendingRow(t *testing.T) {
	src := &fakeSource{}
	src.put("s1", 1000, summaryLine{ID: 1, State: statePending})
	pl := linesKit(src)
	pl.intakeDone("s1", 1000)
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 5*time.Second)
	select {
	case a := <-ch:
		t.Fatalf("released while the summary was pending: %+v", a)
	case <-time.After(300 * time.Millisecond):
	}
	src.set(doneLine(1, "事", "推"))
	pl.wake()
	if a := awaitResult(t, ch, 2*time.Second); !a.ok || a.line.Push != "推" {
		t.Fatalf("got %+v", a)
	}
}

func TestPushLines_IntakeTimesAreBounded(t *testing.T) {
	pl := NewPushLines(func() summarySource { return nil })
	for i := 0; i < 1000; i++ {
		pl.intakeDone("s1", int64(i))
	}
	for i := 0; i < 500; i++ {
		pl.intakeDone(string(rune('a'+i%26))+string(rune('A'+i/26)), int64(1_000_000+i))
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if n := len(pl.intakeAt["s1"]); n > maxIntakeKept {
		t.Fatalf("%d times kept for one session", n)
	}
	if n := len(pl.intakeAt); n > maxIntakeSessions {
		t.Fatalf("%d sessions kept", n)
	}
}

// The real path: ClosestSummary over dialogue.db, woken by the store write.
func TestAwait_RealStore(t *testing.T) {
	s := storeWithSession(t)
	pl := StorePushLines(func() *Store { return s })
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 10_000))
	ch := awaitAsync(pl, context.Background(), "s1", 10_000, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	if _, err := s.SetSummaryDone(id, "功能甲", "好了", "內文。", false, usage{}); err != nil {
		t.Fatal(err)
	}
	pl.wake()
	a := awaitResult(t, ch, 2*time.Second)
	if !a.ok || a.line.Thing != "功能甲" || a.line.Push != "好了" || a.line.ConvKey != "root-s1" || a.line.EntryID != id {
		t.Fatalf("got %+v", a)
	}
	// a skipped row answers false at once
	id2 := mustInsertSummary(t, s, pendingRow("s1", "t2", 2, 90_000))
	ch = awaitAsync(pl, context.Background(), "s1", 90_000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	if ok, err := s.FinishSummary(id2, stateSkipped, reasonNoMod); err != nil || !ok {
		t.Fatal(err)
	}
	pl.wake()
	if a := awaitResult(t, ch, 2*time.Second); a.ok {
		t.Fatalf("got %+v", a)
	}
	// the module is off
	off := StorePushLines(func() *Store { return nil })
	if a := awaitResult(t, awaitAsync(off, context.Background(), "s1", 10_000, 30*time.Second), time.Second); a.ok {
		t.Fatal("a line from a stopped module")
	}
}
