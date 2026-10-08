package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// interruptBody is a cc PostToolUseFailure for pane %5 as CC sends it when the
// user presses Esc during a tool call.
func interruptBody(agentID string) string {
	raw, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"tool_name":       "Bash",
		"tool_use_id":     "T-int",
		"error":           "interrupted",
		"is_interrupt":    true,
		"agent_id":        agentID,
	})
	return `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxPostToolUseFailure","raw_event":` + string(raw) + `,"agent_type":"cc"}`
}

func promptBody() string {
	return `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxUserPromptSubmit","raw_event":{},"agent_type":"cc"}`
}

// Esc during a tool: CC sends no Stop, so the failure hook is the only idle
// signal. The existing frame goes idle through the narrow write, the
// projection is rebuilt and the idle frame goes out.
func TestHandler_InterruptIdlesExistingFrame(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	sendBody(t, m, promptBody())
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusRunning {
		t.Fatalf("precondition: frame status = %q, want running", got)
	}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, interruptBody(""))

	row := findCCFrameRow(t, m)
	if row.Status != agentpkg.StatusIdle {
		t.Fatalf("frame status = %q, want idle after is_interrupt", row.Status)
	}
	if len(row.Subagents) != 1 || row.Subagents[0].ID != "agent-X" {
		t.Fatalf("Subagents = %+v, want untouched [agent-X] (Esc status write is narrow)", row.Subagents)
	}
	msgs := drainBroadcasts(sub, 200*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("no hook broadcast after is_interrupt")
	}
	if last := msgs[len(msgs)-1]; last.Status != string(agentpkg.StatusIdle) {
		t.Fatalf("emitted status = %q, want idle", last.Status)
	}
	m.mu.Lock()
	cur := m.currentStatus["work"]
	m.mu.Unlock()
	if cur != agentpkg.StatusIdle {
		t.Fatalf("currentStatus[work] = %q, want idle (probe and the next guard read it)", cur)
	}
}

// Esc's idle is only as new as the interrupt itself: a frame a newer event has
// already written (here a running turn stamped later than the interrupt)
// keeps its status and nothing is emitted (U1-2a-4 F2).
func TestHandler_InterruptNeverOverwritesNewerFrame(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	row := findCCFrameRow(t, m)
	newer := time.Now().Add(time.Hour).UnixNano()
	if err := m.frames.UpdateStatusAndLastSeen(row.FrameID, agentpkg.StatusRunning, newer); err != nil {
		t.Fatalf("seed newer running: %v", err)
	}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, interruptBody(""))

	got := findCCFrameRow(t, m)
	if got.Status != agentpkg.StatusRunning || got.LastSeenAt != newer {
		t.Fatalf("frame = status %q last_seen %d, want running at %d untouched", got.Status, got.LastSeenAt, newer)
	}
	if msgs := drainBroadcasts(sub, 150*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("broadcasts = %+v, want none", msgs)
	}
}

// The race itself: between the interrupt's read of the frame and its write,
// another hook writes running with a newer stamp. The conditional write loses
// (its expected last_seen no longer matches), the re-read shows a newer
// frame, and the interrupt gives way: running survives and idle is not sent.
func TestHandler_InterruptLosesRaceToNewerRunning(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	sendBody(t, m, promptBody())
	row := findCCFrameRow(t, m)

	fired := false
	orig := interruptBeforeWriteFn
	interruptBeforeWriteFn = func(m *Module) {
		if fired {
			return
		}
		fired = true
		if err := m.frames.UpdateStatusAndLastSeen(row.FrameID, agentpkg.StatusRunning, time.Now().Add(time.Hour).UnixNano()); err != nil {
			t.Errorf("racing writer: %v", err)
		}
	}
	t.Cleanup(func() { interruptBeforeWriteFn = orig })
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, interruptBody(""))

	if !fired {
		t.Fatal("the seam between the read and the write never ran")
	}
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusRunning {
		t.Fatalf("frame status = %q, want running (the newer write must survive)", got)
	}
	if msgs := drainBroadcasts(sub, 150*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("broadcasts = %+v, want none: idle must not be sent over a newer running", msgs)
	}
}

// A failure hook that arrives after the frame is gone must not bring it back
// (the detail-only branch's rule, unchanged by giving interrupts a status).
func TestHandler_InterruptNeverResurrects(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, interruptBody("agent-X"))

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frames = %+v, want none (interrupt must not create a frame)", frames)
	}
	if msgs := drainBroadcasts(sub, 150*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("broadcasts = %+v, want none", msgs)
	}
}
