package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

func TestAbortedAt_SetByTheMainTurnsAbortedComplete(t *testing.T) {
	s := NewStreamState("s")
	now := t0.Add(7 * time.Second)
	at := now.Add(-300 * time.Millisecond) // the mod stamped it before the batch went out
	ev := e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)
	ev.Seq, ev.At = 1, at.UnixMilli()
	s.Apply(ev, now)
	if !s.AbortedAt.Equal(at) {
		t.Errorf("AbortedAt = %v, want the event's time %v (not the receive time %v)", s.AbortedAt, at, now)
	}
	if s.TurnID != "" || s.Status() != "idle" {
		t.Errorf("turn %q status %q: the abort must end the turn", s.TurnID, s.Status())
	}
}

func TestAbortedAt_NotForAnAnswerOrASubagent(t *testing.T) {
	s := NewStreamState("s")
	for i, data := range []string{
		`{"turn_id":"t1","reason":"answer","aborted":false}`,
		`{"turn_id":"t1","reason":"aborted","aborted":true,"agent_id":"ag-1"}`,
	} {
		ev := e(modevents.TypeTurnComplete, data)
		ev.Seq, ev.At = int64(i+1), t0.UnixMilli()
		s.Apply(ev, t0)
	}
	if !s.AbortedAt.IsZero() {
		t.Errorf("AbortedAt = %v, want zero", s.AbortedAt)
	}
}

func TestAbortedAt_ClearedByANewSession(t *testing.T) {
	s := NewStreamState("s")
	ev := e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)
	ev.Seq, ev.At = 1, t0.UnixMilli()
	s.Apply(ev, t0)
	sw := e(modevents.TypeSessionSwitch, `{}`)
	sw.Seq, sw.At = 2, t0.UnixMilli()
	s.Apply(sw, t0)
	if !s.AbortedAt.IsZero() {
		t.Errorf("AbortedAt = %v after a session switch", s.AbortedAt)
	}
}

func TestAbortedAt_ANewMainTurnClearsIt(t *testing.T) {
	s := NewStreamState("s")
	ev := e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)
	ev.Seq, ev.At = 1, t0.UnixMilli()
	s.Apply(ev, t0)
	if s.AbortedAt.IsZero() {
		t.Fatal("not recorded")
	}
	sub := e(modevents.TypeTurnStart, `{"turn_id":"s1","agent_id":"ag-1"}`)
	sub.Seq, sub.At = 2, t0.UnixMilli()
	s.Apply(sub, t0)
	if s.AbortedAt.IsZero() {
		t.Error("a subagent's turn.start cleared the main turn's abort")
	}
	next := e(modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	next.Seq, next.At = 3, t0.Add(time.Second).UnixMilli()
	s.Apply(next, t0.Add(time.Second))
	if !s.AbortedAt.IsZero() {
		t.Errorf("AbortedAt = %v after the next main turn began", s.AbortedAt)
	}
}
