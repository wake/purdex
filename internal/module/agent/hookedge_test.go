package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/store"
)

// The hook turn edge (hookedge.go): a UserPromptSubmit that makes a root cc
// frame running, and a Stop that makes it idle, beat the mod's late report of
// the same boundary. Each test drives the real handler for the hooks and the
// subscriber / worker round for the mod, on the settable mod clock.

// edgeRig is a worker rig whose cc hooks answer like the real provider for
// the events the edge rule cares about, with the sender's frame on sid1.
func edgeRig(t *testing.T) *workerRig {
	t.Helper()
	r := newWorkerRig(t)
	r.m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			switch event {
			case "PdxUserPromptSubmit", "PdxPostToolUse":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
			case "PdxNotification", "PdxPermissionRequest":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			case "PdxPreToolUse", "PdxPostToolUseFailure":
				return agentpkg.DeriveResult{Valid: true}
			case "PdxSubagentStart", "PdxSubagentStop":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle, Detail: map[string]any{"agent_id": "a1"}}
			case "PdxSessionEnd":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
			default: // PdxStop, PdxSessionStart
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
		},
	})
	seedIdentityFrame(t, r.m, racePane, "cc", 200, raceFrameStart, 10, modSID1, "/w")
	return r
}

// modIdle feeds the mod a finished turn (idle) at the clock's now.
func (r *workerRig) modIdle() {
	feedMod(r.m, modStrm, modStart, modTurnStart,
		modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`))
}

// modRunning feeds the mod a turn in progress at the clock's now.
func (r *workerRig) modRunning() { feedMod(r.m, modStrm, modStart, modTurnStart) }

// modEvent feeds one more mod event at the clock's now.
func (r *workerRig) modEvent(typ, data string) {
	feedMod(r.m, modStrm, modEv(modSID1, typ, data))
}

func (r *workerRig) advance(d time.Duration) { r.clock.Set(r.clock.Now().Add(d)) }

// light is the pane's projected light right now.
func (r *workerRig) light(t *testing.T) SessionProjection {
	t.Helper()
	p, ok := liveProjectionByPane(t, r.m)[racePane]
	if !ok {
		t.Fatalf("no projection for %s", racePane)
	}
	return p
}

// emitLights is "status/source" of every emit, in order.
func emitLights(got []emitted) []string {
	out := make([]string, 0, len(got))
	for _, e := range got {
		out = append(out, e.Ev.Status+"/"+e.Ev.Source)
	}
	return out
}

func wantLights(t *testing.T, what string, got []emitted, want ...string) {
	t.Helper()
	if g, w := strings.Join(emitLights(got), " "), strings.Join(want, " "); g != w {
		t.Fatalf("%s: emits [%s], want [%s]", what, g, w)
	}
}

func (r *workerRig) edge(frameID string) (hookEdge, bool) {
	r.m.modMu.Lock()
	defer r.m.modMu.Unlock()
	e, ok := r.m.hookEdge[frameID]
	return e, ok
}

func (r *workerRig) edgeCount() int {
	r.m.modMu.Lock()
	defer r.m.modMu.Unlock()
	return len(r.m.hookEdge)
}

// TestHookEdge_UserPromptSubmitBeatsStaleModIdle: the mod still says idle
// when the prompt's hook lands. The hook's own frame must say running (hook),
// and no idle frame may go out at the start of the turn.
func TestHookEdge_UserPromptSubmitBeatsStaleModIdle(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)

	r.hook(t, "PdxUserPromptSubmit")
	r.round()

	wantLights(t, "prompt hook", r.drain(t), "running/hook")
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceHook)
}

// TestHookEdge_StopBeatsStaleModRunning: the mod still says running when the
// Stop hook lands; the hook's frame says idle.
func TestHookEdge_StopBeatsStaleModRunning(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.advance(10 * time.Second)

	r.hook(t, "PdxStop")
	r.round()

	wantLights(t, "stop hook", r.drain(t), "idle/hook")
	wantLight(t, "overlay", r.light(t), agentpkg.StatusIdle, SourceHook)
}

// TestHookEdge_LateNotificationDoesNotRestoreWaiting: the mod saw the ask and
// its approval (running); a Notification / PermissionRequest hook that is
// delivered after that must not put the waiting back (spec §12.1 item 16).
func TestHookEdge_LateNotificationDoesNotRestoreWaiting(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.modEvent(modevents.TypeToolCheck, `{"tool_use_id":"toolu_1","decision":"ask"}`)
	r.modEvent(modevents.TypeToolApproved, `{"tool_use_id":"toolu_1"}`)
	r.round()
	wantLights(t, "mod", r.drain(t), "running/mod")
	r.advance(time.Second)

	r.hook(t, "PdxNotification")
	r.hook(t, "PdxPermissionRequest")
	r.round()

	for _, e := range r.drain(t) {
		if e.Ev.Status != "running" || e.Ev.Source != "mod" {
			t.Fatalf("a late %s hook moved the light to %s/%s", "Notification", e.Ev.Status, e.Ev.Source)
		}
	}
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceMod)
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges recorded, want none", n)
	}
}

// TestHookEdge_DetailOnlyHookDoesNotWin: tool hooks are newer than the mod's
// idle, but they say nothing about the turn boundary.
func TestHookEdge_DetailOnlyHookDoesNotWin(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(time.Second)

	for _, name := range []string{"PdxPreToolUse", "PdxPostToolUse", "PdxPostToolUseFailure"} {
		r.hook(t, name)
		wantLight(t, name, r.light(t), agentpkg.StatusIdle, SourceMod)
	}
	r.round()
	for _, e := range r.drain(t) {
		if e.Ev.Status != "idle" || e.Ev.Source != "mod" {
			t.Fatalf("a tool hook emitted %s/%s, want idle/mod", e.Ev.Status, e.Ev.Source)
		}
	}
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges recorded, want none", n)
	}
}

// TestHookEdge_ModCatchesUp: the mod's own turn.start / turn.complete, newer
// than the edge, hand the pane back with source mod.
func TestHookEdge_ModCatchesUp(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)
	r.hook(t, "PdxUserPromptSubmit")
	wantLights(t, "prompt hook", r.drain(t), "running/hook")

	r.advance(900 * time.Millisecond)
	r.modEvent(modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	wantLight(t, "overlay after turn.start", r.light(t), agentpkg.StatusRunning, SourceMod)
	r.round()
	wantLights(t, "turn.start", r.drain(t), "running/mod")

	r.advance(5 * time.Second)
	r.hook(t, "PdxStop")
	wantLights(t, "stop hook", r.drain(t), "idle/hook")

	r.advance(time.Second)
	r.modEvent(modevents.TypeTurnComplete, `{"turn_id":"t2","reason":"answer"}`)
	wantLight(t, "overlay after turn.complete", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	wantLights(t, "turn.complete", r.drain(t), "idle/mod")
}

// TestHookEdge_ModEventWithSameStatusHandsBackTheSource: the mod was already
// running, so its turn.start changes nothing the worker would notice; the
// hook's source still has to turn into mod.
func TestHookEdge_ModEventWithSameStatusHandsBackTheSource(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.round() // the worker has seen the stream live, so only the edge can dirty it later
	r.drain(t)
	r.advance(10 * time.Second)
	r.hook(t, "PdxUserPromptSubmit") // a prompt queued behind a running turn
	wantLights(t, "prompt hook", r.drain(t), "running/hook")

	r.advance(time.Second)
	r.modEvent(modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	r.round()

	wantLights(t, "turn.start", r.drain(t), "running/mod")
}

// TestHookEdge_HeartbeatDoesNotHandBack: a heartbeat after the edge repeats
// the mod's old state; it must not give the pane back to a mod that has not
// yet seen the boundary.
func TestHookEdge_HeartbeatDoesNotHandBack(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)
	r.hook(t, "PdxUserPromptSubmit")
	r.drain(t)

	r.advance(300 * time.Millisecond)
	r.modEvent(modevents.TypeHeartbeat, `{}`) // no turn_id: idle
	r.round()

	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceHook)
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("a heartbeat re-sent the light: %v", emitLights(got))
	}
}

// TestHookEdge_ModEventBetweenReceiveAndNoteDoesNotCreateEdge: the mod's
// turn.complete lands while the Stop hook is still being processed (after the
// daemon received it, before the edge is noted). The mod has caught up with
// the hook, so the hook leaves no edge: the edge's time is the hook's arrival,
// not the moment the handler gets round to noting it.
func TestHookEdge_ModEventBetweenReceiveAndNoteDoesNotCreateEdge(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.round()
	r.drain(t)
	r.advance(10 * time.Second)

	orig := verifyEventFn
	verifyEventFn = func(m *Module, req EventRequest) verifyDecision {
		r.advance(time.Second)
		r.modEvent(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
		r.advance(time.Second)
		return orig(m, req)
	}
	t.Cleanup(func() { verifyEventFn = orig })

	r.hook(t, "PdxStop")

	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges, want none: the mod had already caught up", n)
	}
	wantLight(t, "overlay", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	for _, e := range r.drain(t) {
		if e.Ev.Source != "mod" {
			t.Fatalf("emit %s/%s, want source mod throughout", e.Ev.Status, e.Ev.Source)
		}
	}
}

// TestHookEdge_HeartbeatRepairHandsBack: a heartbeat that repairs the mod's
// light (here the turn.start was lost and the beat carries the turn_id) is a
// light event like any other: it supersedes the edge at once, with the mod's
// running.
func TestHookEdge_HeartbeatRepairHandsBack(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)
	r.hook(t, "PdxStop")
	wantLights(t, "stop hook", r.drain(t), "idle/hook")

	r.advance(300 * time.Millisecond)
	r.modEvent(modevents.TypeHeartbeat, `{"turn_id":"t2"}`)
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceMod)
	r.round()
	wantLights(t, "heartbeat repair", r.drain(t), "running/mod")
}

// TestHookEdge_ExpiresAfterTTL: no mod event confirms the prompt (a slash
// command starts no turn), so the edge runs out and the mod's light returns;
// the worker says so once.
func TestHookEdge_ExpiresAfterTTL(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)
	r.hook(t, "PdxUserPromptSubmit")
	wantLights(t, "prompt hook", r.drain(t), "running/hook")

	r.advance(hookEdgeTTL - time.Millisecond)
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("emit before the edge expired: %v", emitLights(got))
	}
	wantLight(t, "overlay before expiry", r.light(t), agentpkg.StatusRunning, SourceHook)

	r.advance(time.Millisecond)
	wantLight(t, "overlay at expiry", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	e := wantOneEmit(t, "expiry", r.drain(t), "code-work", "idle", "mod")
	if e.Ev.Detail["mod_event"] != modEventEdgeExpired {
		t.Fatalf("detail = %v, want mod_event %s", e.Ev.Detail, modEventEdgeExpired)
	}
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the expiry was sent again: %v", emitLights(got))
	}
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges left after expiry", n)
	}
}

// TestHookEdge_OtherSessionIsIgnored: an edge belongs to the conversation it
// was recorded in; after a /clear the new sid starts from the mod's light.
func TestHookEdge_OtherSessionIsIgnored(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	// The conversation the pane moves to has a stream of its own, quiet since
	// before the edge.
	feedMod(r.m, "stream-test-0002", modEv(modSID2, modevents.TypeSessionStart, `{"cwd":"/w"}`))
	r.advance(10 * time.Second)
	r.hook(t, "PdxUserPromptSubmit")
	r.drain(t)

	f, err := r.m.frames.GetByIdentity(racePane, 200, raceFrameStart)
	if err != nil || f == nil {
		t.Fatalf("frame: %v %v", f, err)
	}
	if e, ok := r.edge(f.FrameID); !ok || e.sid != modSID1 {
		t.Fatalf("edge = %+v %v, want one on %s", e, ok, modSID1)
	}
	if err := r.m.frames.UpdateSessionIdentity(f.FrameID, modSID2, "/w", 1<<40); err != nil {
		t.Fatal(err)
	}
	wantLight(t, "new sid", r.light(t), agentpkg.StatusIdle, SourceMod)
}

// TestHookEdge_SubagentStopAndProxyDoNotRecord: only the main agent's root
// frame has a turn edge.
func TestHookEdge_SubagentStopAndProxyDoNotRecord(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.advance(10 * time.Second)

	// A native subagent starts and stops: the stop updates the frame (and
	// the derive answer is idle), yet it is not the turn's Stop.
	r.hook(t, "PdxSubagentStart")
	r.hook(t, "PdxSubagentStop")
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("SubagentStop recorded %d edges", n)
	}
	wantLight(t, "after SubagentStop", r.light(t), agentpkg.StatusRunning, SourceMod)

	// A cc frame under another agent's frame (a proxy): its own Stop is not
	// the turn boundary of the pane's session.
	root, err := r.m.frames.GetByIdentity(racePane, 200, raceFrameStart)
	if err != nil || root == nil {
		t.Fatalf("root: %v %v", root, err)
	}
	if _, err := r.m.frames.Upsert(store.Frame{
		PaneID: racePane, AgentType: "cc", PID: 300, PPID: 200, ProcessStartTime: "Sun Apr 20 01:31:00 2026",
		ParentFrameID: root.FrameID, Status: agentpkg.StatusRunning, StartedAt: 20, LastSeenAt: 20,
		Verified: true, SessionID: modSID1, Cwd: "/w", Subagents: []agentpkg.SubagentRef{},
	}); err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(hookBody("PdxStop"), `"sender_pid":200`, `"sender_pid":300`, 1)
	body = strings.Replace(body, raceFrameStart, "Sun Apr 20 01:31:00 2026", 1)
	if w := postEvent(r.m, body); w.Code != 200 {
		t.Fatalf("proxy Stop: %d %s", w.Code, w.Body.String())
	}
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("a nested frame's Stop recorded %d edges", n)
	}
}

// TestHookEdge_ClearedBySessionStartAndFrameDelete: a new conversation starts
// without an edge, and a deleted frame leaves none behind.
func TestHookEdge_ClearedBySessionStartAndFrameDelete(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)

	r.hook(t, "PdxUserPromptSubmit")
	if n := r.edgeCount(); n != 1 {
		t.Fatalf("%d edges after the prompt, want 1", n)
	}
	r.hook(t, "PdxSessionStart")
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges after SessionStart, want 0", n)
	}

	r.hook(t, "PdxStop")
	if n := r.edgeCount(); n != 1 {
		t.Fatalf("%d edges after Stop, want 1", n)
	}
	// The SessionEnd names the run it ends, or the frame is not claimed.
	end := strings.Replace(hookBody("PdxSessionEnd"), `"raw_event":{}`, `"raw_event":{"session_id":"`+modSID1+`"}`, 1)
	if w := postEvent(r.m, end); w.Code != 200 {
		t.Fatalf("SessionEnd: %d %s", w.Code, w.Body.String())
	}
	if f, _ := r.m.frames.GetByIdentity(racePane, 200, raceFrameStart); f != nil {
		t.Fatal("the SessionEnd did not delete the frame")
	}
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges after SessionEnd deleted the frame, want 0", n)
	}
}

// TestPeerPromptStartsTurnWithoutIdleFrame replays the sequence measured on
// alpha.607: a peer message opens a turn while the mod still says idle, the
// mod's turn.start follows about a second later, and the same at the end. The
// light goes running, running, idle, idle and never shows an idle in the
// middle of the turn (the SPA marks every idle unread).
func TestPeerPromptStartsTurnWithoutIdleFrame(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.advance(10 * time.Second)
	r.round()
	r.drain(t)

	var seen []emitted
	step := func(f func()) {
		t.Helper()
		f()
		seen = append(seen, r.drain(t)...)
	}
	step(func() { r.hook(t, "PdxUserPromptSubmit") })
	r.advance(900 * time.Millisecond)
	step(func() { // mod turn.start
		r.modEvent(modevents.TypeTurnStart, `{"turn_id":"t2"}`)
		r.round()
	})
	r.advance(20 * time.Second)
	step(func() { r.hook(t, "PdxStop") })
	r.advance(time.Second)
	step(func() { // mod turn.complete
		r.modEvent(modevents.TypeTurnComplete, `{"turn_id":"t2","reason":"answer"}`)
		r.round()
	})

	wantLights(t, "turn", seen, "running/hook", "running/mod", "idle/hook", "idle/mod")
	for _, e := range seen[:3] {
		if e.Ev.Status == "idle" && e.Ev.Source == "mod" {
			t.Fatalf("a stale mod idle went out: %+v", e)
		}
	}
}
