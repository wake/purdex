package dialogue

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

func toolTurnID(id string, status convmodel.StepStatus) convmodel.Turn {
	return done(id, userItem(id+"-u", "run "+id), stepItem(id+"-s", "Bash", "ls", status), agentItem(id+"-a", "ok "+id))
}

// a retry reads from the cursor the first part of this event moved, so it never looks like a rebuild.
func TestIntakeRetryReadsFromTheMovedCursor(t *testing.T) {
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		st := convmodel.StepRunning
		if n >= 3 {
			st = convmodel.StepDone
		}
		return fromRes([]convmodel.Turn{basicTurn("A"), basicTurn("B"), toolTurnID("C", st)}, from), nil
	})
	h.in.OnTurnEnd(stopEv("s", 1))
	h.waitSettled(1)
	if got := strings.Join(h.fr.fromsOf("s"), ","); got != ",B,B" {
		t.Fatalf("read cursors = %q, want \",B,B\"", got)
	}
	w := h.writtenCalls()
	if len(w) != 1 {
		t.Fatalf("written = %d calls", len(w))
	}
	if w[0].wr.Rebuilt || !w[0].wr.First || w[0].wr.WaitingTool {
		t.Fatalf("result = %+v", w[0].wr)
	}
	var ids []string
	for _, tr := range w[0].wr.Turns {
		ids = append(ids, tr.ID)
	}
	if strings.Join(ids, ",") != "A,B,C" {
		t.Fatalf("onWritten turns = %v, want A,B,C", ids)
	}
	if got := h.read("s", 1); !strings.Contains(got, "ok C") {
		t.Fatalf("C not written:\n%s", got)
	}
}

// a rebuild that stops at a running tool continues without a second rebuild.
func TestIntakeNeedAllThenWaitingTool(t *testing.T) {
	var mu sync.Mutex
	phase, n2 := 1, 0
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if phase == 1 {
			return fromRes([]convmodel.Turn{basicTurn("A")}, from), nil
		}
		n2++
		st := convmodel.StepRunning
		if n2 >= 4 {
			st = convmodel.StepDone
		}
		return fromRes([]convmodel.Turn{basicTurn("A"), toolTurnID("C", st)}, from), nil
	})
	h.in.OnTurnEnd(stopEv("s", 1))
	h.waitSettled(1)
	if err := os.Remove(h.seg("s", 1)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	phase = 2
	mu.Unlock()
	h.in.OnTurnEnd(stopEv("s", 2))
	h.waitSettled(2)
	if got := strings.Join(h.fr.fromsOf("s"), ","); got != ",A,,A,A" {
		t.Fatalf("read cursors = %q, want \",A,,A,A\"", got)
	}
	w := h.writtenCalls()
	if len(w) != 2 || !w[1].wr.Rebuilt || w[1].wr.WaitingTool {
		t.Fatalf("written = %+v", w)
	}
	if got := h.read("s", 1); !strings.Contains(got, "ok C") || strings.Count(got, "question A") != 1 {
		t.Fatalf("file:\n%s", got)
	}
}

// Stop settles the events still queued, one call each.
func TestIntakeStopSettlesQueuedEvents(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := newHarness(t, func(sid, from string, n int) (convturns.TurnsFromResult, error) {
		if n == 1 {
			entered <- struct{}{}
			<-gate
		}
		return fromRes([]convmodel.Turn{basicTurn("t1")}, from), nil
	})
	h.in.OnTurnEnd(stopEv("s", 1))
	<-entered
	for at := int64(2); at <= 4; at++ {
		h.in.OnTurnEnd(stopEv("s", at))
	}
	stopped := make(chan struct{})
	go func() { h.in.Stop(); close(stopped) }()
	time.Sleep(20 * time.Millisecond)
	close(gate)
	<-stopped
	got := h.settledCalls()
	if len(got) != 4 {
		t.Fatalf("settled = %+v, want one call per event (4)", got)
	}
	seen := map[int64]int{}
	for _, c := range got {
		seen[c.at]++
	}
	for at := int64(1); at <= 4; at++ {
		if seen[at] != 1 {
			t.Fatalf("settled At %d %d times: %+v", at, seen[at], got)
		}
	}
}

type ctxReader struct{ entered chan struct{} }

func (r ctxReader) TurnsFrom(ctx context.Context, provider, sid, from string) (convturns.TurnsFromResult, error) {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return convturns.TurnsFromResult{}, ctx.Err()
}

// Stop cancels a read that is in flight instead of waiting out its timeout.
func TestIntakeStopInterruptsRead(t *testing.T) {
	e := newEnv(t, Hooks{})
	r := ctxReader{entered: make(chan struct{}, 1)}
	in := newIntake(intakeDeps{turns: r, writer: e.w, store: e.store, timing: timing{busyDelay: time.Millisecond, settleDelay: time.Millisecond, idle: time.Hour}})
	in.OnTurnEnd(stopEv("s", 1))
	<-r.entered
	stopped := make(chan struct{})
	go func() { in.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not interrupt the read")
	}
}

func mkTurn(id string, outcome convmodel.Outcome, started int64, text string, step bool) convmodel.Turn {
	items := []convmodel.Item{userItem(id+"-u", "q "+id)}
	if step {
		items = append(items, stepItem(id+"-s", "Bash", "ls", convmodel.StepDone))
	}
	if text != "" {
		items = append(items, agentItem(id+"-a", text))
	}
	tr := turn(id, outcome, items...)
	tr.StartedAt = started
	if outcome == convmodel.OutcomeRunning {
		tr.EndedAt = nil
	}
	return tr
}

func TestFindTargetTable(t *testing.T) {
	c := mkTurn("c", convmodel.OutcomeDone, 10, "hello world", false)
	run, dn, fl := convmodel.OutcomeRunning, convmodel.OutcomeDone, convmodel.OutcomeFailed
	rows := []struct {
		name   string
		turns  []convmodel.Turn
		found  bool
		text   string
		at     int64
		target int
		caught bool
	}{
		{"hook text cut short", []convmodel.Turn{c, mkTurn("n", dn, 20, "all done and more", false)}, true, "all done", 100, 1, true},
		{"turn text is a prefix of the hook", []convmodel.Turn{c, mkTurn("n", dn, 20, "all done", false)}, true, "all done and more", 100, 1, true},
		{"cursor turn has the same words, a newer turn has none yet", []convmodel.Turn{c, mkTurn("n", run, 20, "", false)}, true, "hello world", 100, -1, false},
		{"same Stop sent again", []convmodel.Turn{c}, true, "hello world", 100, 0, true},
		{"only the cursor turn, other words", []convmodel.Turn{c}, true, "something else", 100, -1, true},
		{"several newer turns, running newest has words", []convmodel.Turn{c, mkTurn("n1", dn, 20, "a", false), mkTurn("n2", run, 60, "b", false)}, true, "zzz", 100, -1, true},
		{"running turn that began after the Stop", []convmodel.Turn{c, mkTurn("n", run, 150, "", false)}, true, "zzz", 100, -1, true},
		{"no text: failed turn with a later ended turn", []convmodel.Turn{c, mkTurn("f", fl, 10, "", true), mkTurn("g", dn, 200, "x", false)}, true, "", 100, 1, true},
		{"no text: only the cursor turn", []convmodel.Turn{c}, true, "", 100, -1, true},
		{"no turns", nil, false, "x", 100, -1, true},
		{"no text, At unknown", []convmodel.Turn{c, mkTurn("n", dn, 20, "x", false)}, true, "", 0, 1, true},
		{"no cursor: first turn matches", []convmodel.Turn{mkTurn("t1", dn, 20, "x", false)}, false, "x", 100, 0, true},
		{"newest running with its own words, one newer turn", []convmodel.Turn{c, mkTurn("n", run, 20, "different", false)}, true, "zzz", 100, 1, true},
		{"nothing at all yet, no cursor", []convmodel.Turn{mkTurn("t1", run, 20, "", false)}, false, "zzz", 100, -1, false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			ev := agentmod.TurnEndEvent{SessionID: "s", Text: r.text, At: r.at}
			target, caught := findTarget(r.turns, r.found, ev)
			if target != r.target || caught != r.caught {
				t.Fatalf("findTarget = (%d, %v), want (%d, %v)", target, caught, r.target, r.caught)
			}
		})
	}
}
