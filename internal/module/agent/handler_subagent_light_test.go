package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// ccBody builds a cc hook request for pane %5 with the given purdex name and raw event.
func ccBody(name string, raw map[string]any) string {
	rawJSON, _ := json.Marshal(raw)
	return `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"` + name + `","raw_event":` + string(rawJSON) + `,"agent_type":"cc"}`
}

// idleMainWithSubagent leaves a cc pane whose main turn has ended (idle) while
// subagent agent-X is still registered.
func idleMainWithSubagent(t *testing.T) *Module {
	t.Helper()
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	sendBody(t, m, promptBody())
	sendBody(t, m, ccBody("PdxStop", map[string]any{"last_assistant_message": "done"}))
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusIdle {
		t.Fatalf("precondition: frame status = %q, want idle", got)
	}
	return m
}

// A subagent's PostToolUse carries the MAIN session's id and an agent_id. It
// must not turn the idle main conversation back into running (#2873).
func TestHandler_SubagentPostToolUse_DoesNotMoveMainLight(t *testing.T) {
	m := idleMainWithSubagent(t)
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, ccBody("PdxPostToolUse", map[string]any{
		"tool_name": "Write", "agent_id": "agent-X", "agent_type": "general-purpose", "session_id": "main-sid",
	}))

	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusIdle {
		t.Fatalf("frame status = %q, want idle", got)
	}
	m.mu.Lock()
	cur := m.currentStatus["work"]
	m.mu.Unlock()
	if cur == agentpkg.StatusRunning {
		t.Fatalf("currentStatus = running, want it left alone")
	}
	for _, msg := range drainBroadcasts(sub, 200*time.Millisecond) {
		if msg.Status == string(agentpkg.StatusRunning) {
			t.Fatalf("broadcast status running: %+v", msg)
		}
	}
}

// The main conversation's own PostToolUse (no agent_id) still means running.
func TestHandler_MainPostToolUse_StillRunning(t *testing.T) {
	m := idleMainWithSubagent(t)

	sendBody(t, m, ccBody("PdxPostToolUse", map[string]any{"tool_name": "Write"}))

	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusRunning {
		t.Fatalf("frame status = %q, want running", got)
	}
}

func permissionBy(agentID string) string {
	raw := map[string]any{"tool_name": "Write"}
	if agentID != "" {
		raw["agent_id"] = agentID
	}
	return ccBody("PdxPermissionRequest", raw)
}

func subagentTool(agentID string) string {
	return ccBody("PdxPostToolUse", map[string]any{"tool_name": "Write", "agent_id": agentID})
}

// The subagent that raised the permission prompt clears the waiting it caused
// with its next tool call, back to the light from before (idle here); another
// subagent's tool call does not (#2873).
func TestHandler_SubagentWaiting_ClearedOnlyByItsOwnAgent(t *testing.T) {
	m := idleMainWithSubagent(t)
	sendBody(t, m, permissionBy("agent-X"))
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusWaiting {
		t.Fatalf("precondition: status = %q, want waiting", got)
	}

	sendBody(t, m, subagentTool("agent-Y"))
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusWaiting {
		t.Fatalf("another subagent cleared the wait: status = %q", got)
	}

	sendBody(t, m, subagentTool("agent-X"))
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusIdle {
		t.Fatalf("status = %q, want idle (the light before the wait)", got)
	}

	// Cleared once: a later tool call by it changes nothing.
	sendBody(t, m, subagentTool("agent-X"))
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusIdle {
		t.Fatalf("status = %q after a second call, want idle", got)
	}
}

// The tmux path must not consume the record when it reads it: a failed frame
// write has to leave it for the hook's retry (codex R3).
func TestSubagentWait_TmuxReadKeepsRecordForRetry(t *testing.T) {
	m := idleMainWithSubagent(t)
	sendBody(t, m, permissionBy("agent-X"))
	req := EventRequest{TmuxPaneID: "%5", RawEvent: json.RawMessage(`{"agent_id":"agent-X"}`)}
	for i := 0; i < 2; i++ {
		if _, ok := m.takeSubagentWait(req, false); !ok {
			t.Fatalf("read %d: record gone", i)
		}
	}
}

// A waiting the main conversation caused (no agent_id) is not cleared by a
// subagent's tool call.
func TestHandler_MainWaiting_NotClearedBySubagent(t *testing.T) {
	m := idleMainWithSubagent(t)
	sendBody(t, m, permissionBy("agent-X"))
	sendBody(t, m, permissionBy(""))

	sendBody(t, m, subagentTool("agent-X"))

	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusWaiting {
		t.Fatalf("status = %q, want the main waiting kept", got)
	}
}

// SubagentStop after a subagent's tool use must not broadcast running for an
// idle main conversation (the idle->running->idle flicker).
func TestHandler_SubagentStopAfterToolUse_StaysIdle(t *testing.T) {
	m := idleMainWithSubagent(t)
	sendBody(t, m, ccBody("PdxPostToolUse", map[string]any{"tool_name": "Write", "agent_id": "agent-X"}))
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, ccBody("PdxSubagentStop", map[string]any{"agent_id": "agent-X"}))

	msgs := drainBroadcasts(sub, 200*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("no SubagentStop broadcast")
	}
	for _, msg := range msgs {
		if msg.Status == string(agentpkg.StatusRunning) {
			t.Fatalf("SubagentStop broadcast running: %+v", msg)
		}
	}
	if row := findCCFrameRow(t, m); len(row.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want agent-X removed", row.Subagents)
	}
}
