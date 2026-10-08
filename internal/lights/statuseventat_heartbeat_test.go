package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// TestStatusEventAt_HeartbeatThatChangesStatusUpdatesIt: a heartbeat that
// repairs the state (a lost turn.start / turn.complete, an ask, a compaction
// or an error the events missed) moves the light, so it counts; one that
// repeats the state does not.
func TestStatusEventAt_HeartbeatThatChangesStatusUpdatesIt(t *testing.T) {
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	cases := []struct {
		name  string
		setup modevents.Event // applied at at(1)
		beat  string
		moves bool
	}{
		{"turn_id repairs idle to running", e(modevents.TypeSessionStart, `{}`), `{"turn_id":"t1"}`, true},
		{"no turn_id repairs running to idle", e(modevents.TypeTurnStart, `{"turn_id":"t1"}`), `{}`, true},
		{"asks repair running to waiting", e(modevents.TypeTurnStart, `{"turn_id":"t1"}`), `{"turn_id":"t1","asks":["u1"]}`, true},
		{"compacting repairs idle to running", e(modevents.TypeSessionStart, `{}`), `{"compacting":true}`, true},
		{"error repairs idle to error", e(modevents.TypeSessionStart, `{}`), `{"error":true}`, true},
		{"the same running state", e(modevents.TypeTurnStart, `{"turn_id":"t1"}`), `{"turn_id":"t1"}`, false},
		{"the same idle state", e(modevents.TypeSessionStart, `{}`), `{}`, false},
		{"a dot repaired, the status the same", e(modevents.TypeSessionStart, `{}`), `{"agents":[{"id":"a1","status":"running"}]}`, false},
	}
	for _, c := range cases {
		s := NewStreamState("s")
		s.Apply(c.setup, at(1))
		s.Apply(e(modevents.TypeHeartbeat, c.beat), at(5))
		want := at(1)
		if c.moves {
			want = at(5)
		}
		if !s.StatusEventAt.Equal(want) {
			t.Errorf("%s: StatusEventAt = %v, want %v", c.name, s.StatusEventAt, want)
		}
	}
}
