package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// TestModWorker_DigestCoversEverySubagentRefField: two refs with the same
// identity but a different wire field are a different light — the SPA draws
// delegating, the type and the start time.
func TestModWorker_DigestCoversEverySubagentRefField(t *testing.T) {
	base := agentpkg.SubagentRef{ID: "agent-A", Type: "task", StartedAt: 10, SourcePID: 900, SourceStartTime: "s900", SourceTurnID: "t1"}
	variants := map[string]func(*agentpkg.SubagentRef){
		"delegating":        func(r *agentpkg.SubagentRef) { r.Delegating = true },
		"delegating tool":   func(r *agentpkg.SubagentRef) { r.DelegatingToolUseIDs = []string{"toolu_1"} },
		"is_proxy":          func(r *agentpkg.SubagentRef) { r.IsProxy = true },
		"type":              func(r *agentpkg.SubagentRef) { r.Type = "explore" },
		"started_at":        func(r *agentpkg.SubagentRef) { r.StartedAt = 11 },
		"source_turn_id":    func(r *agentpkg.SubagentRef) { r.SourceTurnID = "t2" },
		"source_pid":        func(r *agentpkg.SubagentRef) { r.SourcePID = 901 },
		"source_start_time": func(r *agentpkg.SubagentRef) { r.SourceStartTime = "s901" },
	}
	digest := func(r agentpkg.SubagentRef) lightsDigest {
		return lightsDigestOf(nil, agentpkg.NormalizedEvent{Status: "running", Subagents: []agentpkg.SubagentRef{r}})
	}
	want := digest(base)
	for name, mutate := range variants {
		v := base
		mutate(&v)
		if digest(v) == want {
			t.Errorf("a change of %s does not change the digest", name)
		}
	}
}

// TestModWorker_EmitsWhenAHookRefFieldChanges: the mod dots a subagent, the
// hooks mark it delegating; same identity, same status, but a light the
// worker must send.
func TestModWorker_EmitsWhenAHookRefFieldChanges(t *testing.T) {
	r := newWorkerRig(t)
	parent := seedFrameWithSubagents(t, r.m, "%5", "cc", 501, "s501", 10, []agentpkg.SubagentRef{{ID: "agent-A", Type: "task", StartedAt: 5}})
	if err := r.m.frames.UpdateSessionIdentity(parent.FrameID, modSID1, "", 1<<40); err != nil {
		t.Fatal(err)
	}
	spawn := modEv(modSID1, modevents.TypeAgentSpawn, `{"agent_id":"agent-A"}`)
	spawn.Seq, spawn.At = 3, 2000
	feedMod(r.m, modStrm, modStart, modTurnStart, spawn)
	r.round()
	wantOneEmit(t, "first", r.drain(t), "code-work", "running", "mod")

	parent.Subagents = []agentpkg.SubagentRef{{ID: "agent-A", Type: "task", StartedAt: 5, Delegating: true, DelegatingToolUseIDs: []string{"toolu_1"}}}
	if _, err := r.m.frames.Upsert(parent); err != nil {
		t.Fatal(err)
	}
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = "hook"
	r.m.modMu.Unlock()
	r.round()

	e := wantOneEmit(t, "delegating", r.drain(t), "code-work", "running", "mod")
	if len(e.Ev.Subagents) != 1 || !e.Ev.Subagents[0].Delegating {
		t.Fatalf("subagents = %+v, want agent-A delegating", e.Ev.Subagents)
	}
}
