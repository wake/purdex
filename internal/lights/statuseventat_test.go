package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// TestStatusEventAt_OnlyStatusEventsUpdateIt: StatusEventAt is the time of the
// last event that moved (or could have moved) the light. A heartbeat,
// a usage report, a background report or an agent.spawn describes the old
// state again and must not move it, or the periodic heartbeat would hand a
// pane back to a mod that has not yet caught up with a hook.
func TestStatusEventAt_OnlyStatusEventsUpdateIt(t *testing.T) {
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

	// Events that do not touch the light never move it.
	quiet := map[string]modevents.Event{
		"heartbeat":  e(modevents.TypeHeartbeat, `{"turn_id":"t1"}`),
		"usage":      e(modevents.TypeUsage, `{}`),
		"background": e(modevents.TypeBackground, `{"tasks":[],"crons":1}`),
		"spawn":      e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`),
		"unknown":    e("something.else", `{}`),
	}
	for name, ev := range quiet {
		s := NewStreamState("s")
		s.Apply(e(modevents.TypeTurnStart, `{"turn_id":"t1"}`), at(1))
		s.Apply(ev, at(5))
		if !s.StatusEventAt.Equal(at(1)) {
			t.Errorf("%s moved StatusEventAt to %v, want it left at %v", name, s.StatusEventAt, at(1))
		}
		if !s.LastEvent.Equal(at(5)) {
			t.Errorf("%s: LastEvent = %v, want %v", name, s.LastEvent, at(5))
		}
	}

	// Events that move the light do.
	loud := map[string]modevents.Event{
		"session.start":  e(modevents.TypeSessionStart, `{}`),
		"session.switch": e(modevents.TypeSessionSwitch, `{"source":"clear"}`),
		"session.end":    e(modevents.TypeSessionEnd, `{"reason":"logout"}`),
		"turn.start":     e(modevents.TypeTurnStart, `{"turn_id":"t2"}`),
		"turn.complete":  e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`),
		"tool.check ask": e(modevents.TypeToolCheck, `{"tool_use_id":"u1","decision":"ask"}`),
		"tool.start ask": e(modevents.TypeToolStart, `{"tool":"AskUserQuestion","tool_use_id":"u1"}`),
		"compact.start":  e(modevents.TypeCompactStart, `{}`),
		"compact.end":    e(modevents.TypeCompactEnd, `{}`),
		"tool.end":       e(modevents.TypeToolEnd, `{"tool_use_id":"u0"}`),
		"tool.approved":  e(modevents.TypeToolApproved, `{"tool_use_id":"u0"}`),
	}
	for name, ev := range loud {
		s := NewStreamState("s")
		s.Asks["u0"] = true // something for tool.end / tool.approved to close
		s.Apply(e(modevents.TypeHeartbeat, `{}`), at(1))
		s.Apply(ev, at(5))
		if !s.StatusEventAt.Equal(at(5)) {
			t.Errorf("%s: StatusEventAt = %v, want %v", name, s.StatusEventAt, at(5))
		}
	}

	// Events of the same types that cannot change the light do not count: a
	// subagent's turn, a tool that was not waiting on the person, a check
	// that allowed, a precompute compaction, a payload that does not decode.
	inert := map[string]modevents.Event{
		"subagent turn.complete": e(modevents.TypeTurnComplete, `{"agent_id":"a1","reason":"answer"}`),
		"subagent turn.start":    e(modevents.TypeTurnStart, `{"turn_id":"x","agent_id":"a1"}`),
		"tool.check allow":       e(modevents.TypeToolCheck, `{"tool_use_id":"u1","decision":"allow"}`),
		"tool.start plain":       e(modevents.TypeToolStart, `{"tool":"Bash","tool_use_id":"u2"}`),
		"tool.end unknown":       e(modevents.TypeToolEnd, `{"tool_use_id":"nope"}`),
		"precompute":             e(modevents.TypeCompactStart, `{"trigger":"precompute"}`),
		"undecodable turn.start": e(modevents.TypeTurnStart, `[]`),
	}
	for name, ev := range inert {
		s := NewStreamState("s")
		s.Apply(e(modevents.TypeTurnStart, `{"turn_id":"t1"}`), at(1))
		s.Apply(ev, at(5))
		if !s.StatusEventAt.Equal(at(1)) {
			t.Errorf("%s moved StatusEventAt to %v, want it left at %v", name, s.StatusEventAt, at(1))
		}
	}
}
