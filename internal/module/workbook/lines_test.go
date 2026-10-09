package workbook

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/workbooklines"
)

// WB-3: the waiter the push hold uses (plan WB-3 item 1).

func linesKit(t *testing.T, ids ...string) (*kit, *PushLines) {
	t.Helper()
	k := kitWith(t, "s1", ids...)
	pl := NewPushLines(func() *Store { return k.st })
	k.e.SetWaiter(pl)
	return k, pl
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

func wait(t *testing.T, ch <-chan awaited, within time.Duration) awaited {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(within):
		t.Fatal("Await did not return")
		return awaited{}
	}
}

// The line may already be there when the hold starts.
// Mutation gate: skip the store check and only wait for a wake → red.
func TestAwait_LineAlreadyWritten(t *testing.T) {
	k, pl := linesKit(t, "t1") // the event of kitWith is at 1000
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("功能甲", "好了", "句。", "狀", ""))
	a := wait(t, awaitAsync(pl, context.Background(), "s1", 1000, 5*time.Second), time.Second)
	if !a.ok || a.line.Thing != "功能甲" || a.line.Push != "好了" || a.line.ConvKey != "s1" || a.line.EntryID == 0 {
		t.Fatalf("line = %+v ok=%v", a.line, a.ok)
	}
}

// Ready before the deadline: woken by the engine, no polling.
func TestAwait_ReadyBeforeTheDeadline(t *testing.T) {
	k, pl := linesKit(t, "t1")
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("功能甲", "好了", "句。", "狀", ""))
	a := wait(t, ch, 2*time.Second)
	if !a.ok || a.line.Push != "好了" {
		t.Fatalf("got %+v", a)
	}
}

// A failed or skipped entry, or one whose push line was dropped, answers false at once.
func TestAwait_FalseAtOnceWhenThereIsNoLine(t *testing.T) {
	cases := map[string]Result{
		"failed":     {Reason: "api-error"},
		"no push":    answerJSON("事", "", "句。", "狀", ""),
		"skip:model": {Answered: true, Text: `{"skip":true,"thing":"","push":"","entry":"","status":"","thing_done":false,"todos":{"done":[],"dropped":[],"add":[]}}`},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			k, pl := linesKit(t, "t1")
			ch := awaitAsync(pl, context.Background(), "s1", 1000, 30*time.Second)
			time.Sleep(50 * time.Millisecond)
			j := mustNext(t, k, "m", "s1")
			k.finish("m", j, r)
			if a := wait(t, ch, 2*time.Second); a.ok {
				t.Fatalf("got a line: %+v", a.line)
			}
		})
	}
}

// A session without the mod: skipped:no_mod is final at once.
func TestAwait_NoModIsFalseAtOnce(t *testing.T) {
	k := newKit(t) // not capable
	pl := NewPushLines(func() *Store { return k.st })
	k.e.SetWaiter(pl)
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.event("s1", 1000)
	if a := wait(t, ch, 2*time.Second); a.ok {
		t.Fatal("a line for a session without the mod")
	}
}

// An event whose intake made no entry (a turn with nothing to summarise) lets the hold go at once instead of waiting out
// the whole deadline.
// Mutation gate: drop intakeDone → the wait runs to its deadline → red.
func TestAwait_AnEventWithNoEntryReleasesTheHold(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	pl := NewPushLines(func() *Store { return k.st })
	k.e.SetWaiter(pl)
	empty := endedTurn("t1", 100, "")
	empty.Items = empty.Items[:1]
	k.turns.set("s1", empty)
	ch := awaitAsync(pl, context.Background(), "s1", 1000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	k.event("s1", 1000)
	if a := wait(t, ch, 2*time.Second); a.ok || a.took > 2*time.Second {
		t.Fatalf("got %+v", a)
	}
}

// An older turn's entry (or a caught-up turn whose turn_at is its own end time) does not satisfy a newer Stop: it waits,
// and the deadline ends it.
// Mutation gate: drop the turn_at bound from the match → the old entry answers → red.
func TestAwait_AnOlderTurnDoesNotSatisfyANewerStop(t *testing.T) {
	k, pl := linesKit(t, "t1") // entry at turn_at 1000
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("舊的事", "舊的推播", "句。", "狀", ""))
	a := wait(t, awaitAsync(pl, context.Background(), "s1", 60_000, 300*time.Millisecond), 3*time.Second)
	if a.ok {
		t.Fatalf("the old entry answered a new Stop: %+v", a.line)
	}
	if a.took < 250*time.Millisecond {
		t.Fatalf("returned after %v: it should have waited for its deadline", a.took)
	}
	// within the 2 s slack the entry still counts (the two stamps come from different paths)
	if a := wait(t, awaitAsync(pl, context.Background(), "s1", 2999, 5*time.Second), time.Second); !a.ok {
		t.Fatal("the slack of 2 s was not applied")
	}
}

func TestAwait_DeadlineAndCancel(t *testing.T) {
	_, pl := linesKit(t, "t1")
	if a := wait(t, awaitAsync(pl, context.Background(), "s1", 1000, 200*time.Millisecond), 2*time.Second); a.ok || a.took < 150*time.Millisecond {
		t.Fatalf("deadline: %+v", a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := awaitAsync(pl, ctx, "s1", 1000, 30*time.Second)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if a := wait(t, ch, time.Second); a.ok {
		t.Fatal("a line after cancel")
	}
	// the module is off: nothing to wait for
	off := NewPushLines(func() *Store { return nil })
	if a := wait(t, awaitAsync(off, context.Background(), "s1", 1000, 30*time.Second), time.Second); a.ok {
		t.Fatal("a line from a stopped module")
	}
}
