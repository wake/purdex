package agent

import (
	"testing"
	"time"
)

// A main-turn StopFailure ends the turn too (workbook D4): Failed is set, the id comes from the raw event.
// Mutation gate: drop the StopFailure branch of publishTurnEnd → red.
func TestHandler_PublishesFailedTurnEndForMainStopFailure(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "SF1")
	withProcessTree(t, map[int]int{100: 999})
	if code := postHookEvent(t, m, "PdxStopFailure", 100, "t100", `{"hook_event_name":"StopFailure","session_id":"SF1","error":"rate_limit"}`).Code; code != 200 {
		t.Fatalf("stop failure answered %d", code)
	}
	select {
	case ev := <-got:
		if ev.SessionID != "SF1" || !ev.Failed || ev.Seq <= 0 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no TurnEndEvent for a main StopFailure")
	}
}

// A StopFailure that names a subagent is that subagent's, never the main turn's.
// Mutation gate: drop the agent_id check → red.
func TestHandler_NoTurnEndForSubagentStopFailure(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "SF2")
	withProcessTree(t, map[int]int{100: 999})
	postHookEvent(t, m, "PdxStopFailure", 100, "t100", `{"hook_event_name":"StopFailure","session_id":"SF2","agent_id":"a1","error":"rate_limit"}`)
	wantNoTurnEnd(t, got)
}

// A plain Stop is not Failed.
func TestHandler_StopTurnEndIsNotFailed(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "SF3")
	postStop(t, m, `{"hook_event_name":"Stop","session_id":"SF3","last_assistant_message":"ok"}`)
	if ev := <-got; ev.Failed {
		t.Fatalf("event = %+v", ev)
	}
}
