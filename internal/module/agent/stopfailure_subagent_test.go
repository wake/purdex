package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// stopFailureBody is a cc StopFailure for pane %5 as the production provider
// sees it. agentID == "" omits the key (the main agent failed).
func stopFailureBody(agentID string) string {
	raw := map[string]any{
		"hook_event_name": "StopFailure",
		"error":           "rate_limit",
	}
	if agentID != "" {
		raw["agent_id"] = agentID
		raw["agent_type"] = "general-purpose"
	}
	rawJSON, _ := json.Marshal(raw)
	return `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStopFailure","raw_event":` + string(rawJSON) + `,"agent_type":"cc"}`
}

// A subagent that dies (rate limit, ...) must not turn the main frame red:
// the main agent is still running. The dot goes, the status stays, and the
// emitted frame, the stored row and the in-memory view all say running.
func TestStopFailure_SubagentKeepsMainStatus(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	sendBody(t, m, promptBody())
	if got := findCCFrameRow(t, m).Status; got != agentpkg.StatusRunning {
		t.Fatalf("precondition: frame status = %q, want running", got)
	}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, stopFailureBody("agent-X"))

	row := findCCFrameRow(t, m)
	if row.Status != agentpkg.StatusRunning {
		t.Fatalf("frame status = %q, want running (subagent failure)", row.Status)
	}
	if len(row.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want the failed subagent's dot removed", row.Subagents)
	}
	msgs := drainBroadcasts(sub, 200*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("no hook broadcast after the subagent StopFailure")
	}
	last := msgs[len(msgs)-1]
	if last.Status != string(agentpkg.StatusRunning) {
		t.Fatalf("emitted status = %q, want running", last.Status)
	}
	if len(last.Subagents) != 0 {
		t.Fatalf("emitted Subagents = %+v, want none", last.Subagents)
	}
	m.mu.Lock()
	cur := m.currentStatus["work"]
	m.mu.Unlock()
	if cur != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want running (never error)", cur)
	}
}

// A subagent's StopFailure that finds no frame for its sender (a late hook
// after SessionEnd or the sweep) must not bring the frame back, and nothing
// goes on the wire.
func TestStopFailure_SubagentWithoutFrameDoesNotResurrect(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	sendBody(t, m, stopFailureBody("agent-X"))

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frames = %+v, want none", frames)
	}
	if msgs := drainBroadcasts(sub, 150*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("broadcasts = %+v, want none", msgs)
	}
}

// Without an agent_id the failure is the main agent's: still error.
func TestStopFailure_MainAgentStillTurnsError(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	seedCCFrameWithSubagentReal(t, m, "agent-X")
	sendBody(t, m, promptBody())

	sendBody(t, m, stopFailureBody(""))

	row := findCCFrameRow(t, m)
	if row.Status != agentpkg.StatusError {
		t.Fatalf("frame status = %q, want error (the main agent failed)", row.Status)
	}
	if len(row.Subagents) != 1 {
		t.Fatalf("Subagents = %+v, want untouched", row.Subagents)
	}
	m.mu.Lock()
	cur := m.currentStatus["work"]
	m.mu.Unlock()
	if cur != agentpkg.StatusError {
		t.Fatalf("currentStatus[work] = %q, want error", cur)
	}
}

// The detach commits through UpsertIfUnchanged. When another writer moved the
// row first (here a hook set it to waiting), the retry must write the row it
// just reloaded — the waiting status survives; a status captured before the
// first attempt would put the stale one back.
func TestStopFailure_SubagentDetachKeepsConcurrentStatus(t *testing.T) {
	m := newProxyTestModule(t)
	pane := newStopFailurePane()
	stale := seedRunningFrameWithSubagents(t, m, pane, "cc", 100, "t100", 50, []agentpkg.SubagentRef{
		nativeSubagentRef("match-id", 40),
	})
	// The concurrent writer: status running -> waiting, LastSeenAt bumped, so
	// the first UpsertIfUnchanged (expected = stale.LastSeenAt) conflicts.
	if err := m.frames.UpdateStatusAndLastSeen(stale.FrameID, agentpkg.StatusWaiting, 999); err != nil {
		t.Fatalf("concurrent status write: %v", err)
	}

	ref := agentpkg.SubagentRef{ID: "match-id", Type: "cc"}
	outcome, stored, err := m.mutateSubagentsAndStatusWithRetry(stale, ref, 1500)
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if outcome != detachOutcomeDetached {
		t.Fatalf("outcome = %v, want detachOutcomeDetached", outcome)
	}
	if stored.Status != agentpkg.StatusWaiting {
		t.Fatalf("stored status = %q, want waiting (the concurrent writer's)", stored.Status)
	}
	got, err := m.frames.GetByIdentity(pane, stale.PID, stale.ProcessStartTime)
	if err != nil || got == nil {
		t.Fatalf("reload: %v / %v", err, got)
	}
	if got.Status != agentpkg.StatusWaiting {
		t.Fatalf("row status = %q, want waiting", got.Status)
	}
	if len(got.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want the ref removed", got.Subagents)
	}
}
