package agent

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/lights"
)

// MainTurnRunning reads the session's own live stream: a turn in progress is running, a live stream without one is
// not, and a session no live stream reports is unknown. (M: ignore the live window.)
func TestMainTurnRunning_FromTheStream(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	mk := func(sid, turn string, last time.Time, ended bool) *lights.StreamState {
		st := lights.NewStreamState("s-" + sid)
		st.SID, st.TurnID, st.LastEvent, st.Ended = sid, turn, last, ended
		return st
	}
	m := &Module{}
	m.modStreams = map[string]*lights.StreamState{
		"s-busy":  mk("busy", "turn-1", now.Add(-5*time.Second), false),
		"s-idle":  mk("idle", "", now.Add(-5*time.Second), false),
		"s-stale": mk("stale", "turn-2", now.Add(-time.Minute), false),
		"s-ended": mk("ended", "turn-3", now.Add(-time.Second), true),
		"s-moved": mk("other", "turn-4", now, false), // the stream now reports another session id
	}
	m.modBySID = map[string]string{"busy": "s-busy", "idle": "s-idle", "stale": "s-stale", "ended": "s-ended", "moved": "s-moved"}
	m.modNow = func() time.Time { return now }

	for sid, want := range map[string][2]bool{
		"busy": {true, true}, "idle": {false, true},
		"stale": {false, false}, "ended": {false, false}, "moved": {false, false}, "none": {false, false}, "": {false, false},
	} {
		running, ok := m.MainTurnRunning(sid)
		if running != want[0] || ok != want[1] {
			t.Errorf("%q: running %v ok %v, want %v", sid, running, ok, want)
		}
	}
	var nilMod *Module
	if _, ok := nilMod.MainTurnRunning("busy"); ok {
		t.Error("a nil module answers unknown")
	}
}
