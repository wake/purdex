package agent

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// #2115: the turn end of an accepted cc PdxStop is published on the non-tmux hook path too, so a session
// that only reaches the daemon that way (U24 adopts such sessions) gets a last turn.

func nonTmuxTurnEndModule(t *testing.T) (*Module, chan TurnEndEvent) {
	t.Helper()
	m := newTurnEndModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	got := make(chan TurnEndEvent, 4)
	t.Cleanup(m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev }))
	return m, got
}

func nonTmuxBody(purdexName, raw string) string {
	return `{"tmux_session":"","tmux_pane_id":"","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"` + purdexName + `","agent_type":"cc","raw_event":` + raw + `}`
}

func TestNonTmux_PublishesTurnEndForAcceptedStop(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	before := time.Now().UnixMilli()
	if w := postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N1","last_assistant_message":"做完了。"}`)); w.Code != http.StatusOK {
		t.Fatalf("answered %d %s", w.Code, w.Body.String())
	}
	select {
	case ev := <-got:
		if ev.SessionID != "N1" || ev.Text != "做完了。" || ev.Seq <= 0 || ev.At < before || ev.At > time.Now().UnixMilli() {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no TurnEndEvent for an accepted non-tmux Stop")
	}
}

func TestNonTmux_TurnEndNotForSubagentStop(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	postEvent(m, nonTmuxBody("PdxSubagentStop", `{"hook_event_name":"SubagentStop","session_id":"N2","agent_id":"a1","last_assistant_message":"sub"}`))
	wantNoTurnEnd(t, got)
}

func TestNonTmux_TurnEndNotForAnEventTheProviderCannotDerive(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	postEvent(m, nonTmuxBody("PdxNoSuchEvent", `{"hook_event_name":"Stop","session_id":"N3","last_assistant_message":"x"}`))
	wantNoTurnEnd(t, got)
}

func TestNonTmux_TurnEndNotForARejectedEvent(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	old := verifyEventFn
	verifyEventFn = func(*Module, EventRequest) verifyDecision { return verifyDecision{Reason: "pid_reused"} }
	defer func() { verifyEventFn = old }()
	postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N4","last_assistant_message":"rejected"}`))
	wantNoTurnEnd(t, got)
}

func TestNonTmux_TurnEndNotForAnotherProvider(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	postEvent(m, `{"tmux_session":"","tmux_pane_id":"","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","agent_type":"codex","raw_event":{"session_id":"N5","last_assistant_message":"codex"}}`)
	wantNoTurnEnd(t, got)
}

// Published with the daemon's emit unavailable too: the hub does not depend on core.
func TestNonTmux_TurnEndDoesNotNeedTheEventsBroadcaster(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	m.core = nil
	postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N6","last_assistant_message":"x"}`))
	select {
	case ev := <-got:
		if ev.SessionID != "N6" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no TurnEndEvent")
	}
}

// The first Stop is held in verification while the second finishes: the stamps keep the arrival order.
// Mutation gate: stamp at publish → the first-arrived Stop carries the larger Seq → red.
func TestNonTmux_TurnEndStampedAtEntry(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	release, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	old := verifyEventFn
	verifyEventFn = func(mm *Module, req EventRequest) verifyDecision {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-release
		}
		return old(mm, req)
	}
	defer func() { verifyEventFn = old }()
	done := make(chan struct{})
	go func() {
		postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N7","last_assistant_message":"first"}`))
		close(done)
	}()
	<-entered
	postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N7","last_assistant_message":"second"}`))
	close(release)
	<-done
	a, b := <-got, <-got
	if a.Text != "second" || b.Text != "first" {
		t.Fatalf("published order = %q, %q (the second finished first)", a.Text, b.Text)
	}
	if !(b.Seq < a.Seq) || b.At > a.At {
		t.Fatalf("stamps follow publication, not arrival: first %+v second %+v", b, a)
	}
}

// A Stop the provider cannot derive (Valid=false) is not a turn end, even though it is a cc PdxStop.
// Mutation gate: publish before the Valid check → red.
type invalidDerive struct{ *agentcc.Provider }

func (invalidDerive) DeriveStatus(string, json.RawMessage) agentpkg.DeriveResult {
	return agentpkg.DeriveResult{Reason: "payload_unmappable"}
}

func TestNonTmux_TurnEndNotForAStopThatDerivesNothing(t *testing.T) {
	m, got := nonTmuxTurnEndModule(t)
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(invalidDerive{agentcc.NewProvider(nil, nil, nil, nil)})
	postEvent(m, nonTmuxBody("PdxStop", `{"hook_event_name":"Stop","session_id":"N8","last_assistant_message":"x"}`))
	wantNoTurnEnd(t, got)
}
