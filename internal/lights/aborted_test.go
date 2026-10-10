package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

func TestAbortedAt_SetByTheMainTurnsAbortedComplete(t *testing.T) {
	s := NewStreamState("s")
	now := t0.Add(7 * time.Second)
	ev := e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)
	ev.Seq, ev.At = 1, now.UnixMilli()
	s.Apply(ev, now)
	if !s.AbortedAt.Equal(now) {
		t.Errorf("AbortedAt = %v, want the receive time %v", s.AbortedAt, now)
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
