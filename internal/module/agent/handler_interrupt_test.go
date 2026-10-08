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
