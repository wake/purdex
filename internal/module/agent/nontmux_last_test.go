package agent

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// nonTmuxLastModule is a module whose cc provider maps PdxSessionEnd to clear
// and every other event to idle, with a fake clock for the table's expiry.
func nonTmuxLastModule(t *testing.T) (*Module, *core.EventSubscriber, *time.Time) {
	t.Helper()
	m := newTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor(), BootID: "boot-test"}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "PdxSessionEnd" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	m.nonTmuxNow = func() time.Time { return now }
	return m, sub, &now
}

// postNonTmux posts a hook event of a session outside tmux.
func postNonTmux(t *testing.T, m *Module, sid, purdexName string) {
	t.Helper()
	body := fmt.Sprintf(`{"tmux_session":"","tmux_pane_id":"","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":%q,"agent_type":"cc","raw_event":{"session_id":%q}}`, purdexName, sid)
	if w := postEvent(m, body); w.Code != 200 {
		t.Fatalf("post %s %s: status %d body=%s", sid, purdexName, w.Code, w.Body.String())
	}
}

// nonTmuxLastCodes returns the codes of the table, read under the slot's lock
// (the table is protected by it).
func nonTmuxLastCodes(m *Module) map[string]agentpkg.NormalizedEvent {
	m.emit.mu.Lock()
	defer m.emit.mu.Unlock()
	out := make(map[string]agentpkg.NormalizedEvent, len(m.nonTmuxLast))
	for code, e := range m.nonTmuxLast {
		out[code] = e.event
	}
	return out
}

// TestNonTmuxLast_WrittenBySlotAndClearedByClearFrame: the slot records the
// last frame it sent for a non-tmux code (the frame, with its epoch and seq),
// a clear frame forgets the code, and a tmux session's frame that merely has
// no projection (the minimal probe frame also enters the slot with an empty
// session name) is never recorded.
func TestNonTmuxLast_WrittenBySlotAndClearedByClearFrame(t *testing.T) {
	m, sub, _ := nonTmuxLastModule(t)

	postNonTmux(t, m, "abc", "PdxStop")
	got := nonTmuxLastCodes(m)
	last, ok := got[NonTmuxAgentCode("abc")]
	if !ok || len(got) != 1 {
		t.Fatalf("table = %+v, want exactly cc-abc", got)
	}
	if last.Status != "idle" || last.Epoch != "boot-test" || last.Seq != 1 {
		t.Fatalf("recorded frame = %+v, want idle, epoch boot-test, seq 1 (the frame as broadcast)", last)
	}
	var env struct{ Value string }
	if err := json.Unmarshal(<-sub.SendCh(), &env); err != nil {
		t.Fatal(err)
	}
	var sent agentpkg.NormalizedEvent
	if err := json.Unmarshal([]byte(env.Value), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Seq != last.Seq || sent.Epoch != last.Epoch {
		t.Fatalf("recorded (%s,%d) differs from broadcast (%s,%d)", last.Epoch, last.Seq, sent.Epoch, sent.Seq)
	}

	// A tmux session code sent through the slot without a session name (the
	// minimal probe frame) is not a non-tmux session.
	m.emitSessionWith(kindProbe, "tmux-code", "", func(*SessionProjection, error) (agentpkg.NormalizedEvent, bool) {
		return agentpkg.NormalizedEvent{AgentType: "cc", Status: "running"}, true
	})
	if got := nonTmuxLastCodes(m); len(got) != 1 {
		t.Fatalf("a probe frame leaked into the non-tmux table: %+v", got)
	}

	// A newer event for the same code replaces the entry.
	postNonTmux(t, m, "abc", "PdxUserPromptSubmit")
	if got := nonTmuxLastCodes(m)[NonTmuxAgentCode("abc")]; got.RawEventName != "PdxUserPromptSubmit" {
		t.Fatalf("entry = %+v, want the latest frame", got)
	}

	postNonTmux(t, m, "abc", "PdxSessionEnd")
	if got := nonTmuxLastCodes(m); len(got) != 0 {
		t.Fatalf("table after the clear frame = %+v, want empty", got)
	}
}

// TestNonTmuxLast_ExpiresAfterTwoHours: a session that has sent nothing for
// two hours is dropped when the next non-tmux frame is written (no goroutine
// sweeps the table); one that keeps talking is not.
func TestNonTmuxLast_ExpiresAfterTwoHours(t *testing.T) {
	m, _, now := nonTmuxLastModule(t)

	postNonTmux(t, m, "old", "PdxStop")
	postNonTmux(t, m, "busy", "PdxStop")

	*now = now.Add(time.Hour + 59*time.Minute)
	postNonTmux(t, m, "busy", "PdxStop") // refreshes busy
	if got := nonTmuxLastCodes(m); len(got) != 2 {
		t.Fatalf("after 1h59m the table = %+v, want both", got)
	}

	*now = now.Add(2 * time.Minute) // old: 2h01m of silence; busy: 2m
	postNonTmux(t, m, "new", "PdxStop")
	got := nonTmuxLastCodes(m)
	if _, ok := got[NonTmuxAgentCode("old")]; ok {
		t.Fatalf("old survived two hours of silence: %+v", got)
	}
	if _, ok := got[NonTmuxAgentCode("busy")]; !ok {
		t.Fatalf("busy was dropped although it spoke 2 minutes ago: %+v", got)
	}
	if _, ok := got[NonTmuxAgentCode("new")]; !ok {
		t.Fatalf("new is missing: %+v", got)
	}
}
