package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// ---------------------------------------------------------------------------
// The exit envelope (agent-last-state spec §1, review decisions 2, 5, 7).
//
// A ROOT frame ending — SessionEnd deleting a frame with ParentFrameID == ""
// (not a proxy detach), or the sweep clearing one for pid_dead / pid_reused —
// carries detail.pdx_exit, built from the frame BEFORE it is deleted. Child
// frames, proxy detaches and orphans carry none.
// ---------------------------------------------------------------------------

const exitTestInstance = "4471:1788740000"

// exitOf pulls the envelope off a normalized event, or reports that there is
// none. The value is the Go struct on the in-process path and a decoded map
// after a JSON round trip; both are accepted so one helper serves both.
func exitOf(t *testing.T, ev agentpkg.NormalizedEvent) (Exit, bool) {
	t.Helper()
	raw, ok := ev.Detail["pdx_exit"]
	if !ok {
		return Exit{}, false
	}
	if e, ok := raw.(Exit); ok {
		return e, true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal pdx_exit: %v", err)
	}
	var e Exit
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("decode pdx_exit %s: %v", b, err)
	}
	return e, true
}

// seedRootWithIdentity stores a root frame that has reported its own session
// id, the way a SessionStart would have left it.
func seedRootWithIdentity(t *testing.T, m *Module, paneID, agentType string, pid int, startTime, sessionID string) store.Frame {
	t.Helper()
	return seedIdentityFrame(t, m, paneID, agentType, pid, startTime, 10, sessionID, "/w/p")
}

// seedChildFrame stores a frame under parent — a native same-type subagent
// frame, never a root.
func seedChildFrame(t *testing.T, m *Module, paneID, agentType string, pid int, startTime, parentFrameID string) store.Frame {
	t.Helper()
	f, err := m.frames.Upsert(store.Frame{
		PaneID:           paneID,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: startTime,
		ParentFrameID:    parentFrameID,
		Status:           agentpkg.StatusIdle,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
		SessionID:        "child-session",
	})
	if err != nil {
		t.Fatalf("seed child frame: %v", err)
	}
	if f.ParentFrameID != parentFrameID {
		t.Fatalf("child ParentFrameID = %q, want %q", f.ParentFrameID, parentFrameID)
	}
	return f
}

// --- SessionEnd -------------------------------------------------------------

func TestExit_RootSessionEnd_CarriesEnvelopeBuiltBeforeDelete(t *testing.T) {
	for _, agentType := range []string{"cc", "codex"} {
		t.Run(agentType, func(t *testing.T) {
			m := newProvenanceTestModule(t, exitTestInstance)
			root := seedRootWithIdentity(t, m, "%5", agentType, 200, "t200", "S1")
			req := EventRequest{
				TmuxPaneID: "%5", AgentType: agentType, SenderPID: 200,
				SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
				RawEvent: []byte(`{"session_id":"S1"}`),
			}

			before := time.Now().UnixMilli()
			ev := m.buildNormalizedForTest(t, req)
			after := time.Now().UnixMilli()

			// The frame is gone — the envelope must have been taken before.
			if frames, _ := m.frames.ListByPane("%5"); len(frames) != 0 {
				t.Fatalf("frames = %+v, want the root deleted", frames)
			}
			e, ok := exitOf(t, ev)
			if !ok {
				t.Fatalf("root SessionEnd carried no pdx_exit: detail=%+v", ev.Detail)
			}
			if e.AgentType != agentType || e.SessionID != "S1" || e.TmuxPaneID != "%5" ||
				e.TmuxInstance != exitTestInstance || e.FrameID != root.FrameID || e.Reason != ExitReasonSessionEnd {
				t.Fatalf("envelope = %+v, want %s/S1/%%5/%s/%s/session-end", e, agentType, exitTestInstance, root.FrameID)
			}
			if e.At < before || e.At > after {
				t.Fatalf("At = %d, want unix ms in [%d, %d]", e.At, before, after)
			}
			if _, ok := ev.Detail["pdx_provenance"]; ok {
				t.Fatalf("an exit must not also carry provenance")
			}
		})
	}
}

// R1 P1 + attacker #2 (#1381): a SessionStart landing on the SAME frame (same
// pid and start time — cc /clear delivered out of order, an in-process
// /resume) keeps the frame id and overwrites the frame's session id. A late
// SessionEnd of the OLD run then claims nothing: the frame — the newer run's —
// survives, and no exit is sent.
func TestExit_LateSessionEndOfAnOlderRun_ClaimsNothing(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	root := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S-new") // SessionStart(new) already landed
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
		RawEvent: []byte(`{"session_id":"S-old"}`),
	}
	if e, ok := exitOf(t, m.buildNormalizedForTest(t, req)); ok {
		t.Fatalf("a late SessionEnd of an older run sent %+v", e)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].FrameID != root.FrameID || frames[0].SessionID != "S-new" {
		t.Fatalf("frames = %+v, want the newer run's frame untouched", frames)
	}
}

// The exit's session id is the payload's: a frame that never recorded one is
// still ended, and named by what the SessionEnd says.
func TestExit_SessionEnd_SessionIDComesFromThePayload(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	root := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "")
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
		RawEvent: []byte(`{"session_id":"S-old"}`),
	}
	e, ok := exitOf(t, m.buildNormalizedForTest(t, req))
	if !ok {
		t.Fatalf("no pdx_exit")
	}
	if e.SessionID != "S-old" || e.FrameID != root.FrameID {
		t.Fatalf("envelope = %+v, want session id S-old on frame %s", e, root.FrameID)
	}
}

// A payload without a session id sends "" — never the frame's id, which may
// already name a newer run.
func TestExit_SessionEnd_PayloadWithoutSessionID_SendsEmpty(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S-frame")
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
	}
	e, ok := exitOf(t, m.buildNormalizedForTest(t, req))
	if !ok {
		t.Fatalf("no pdx_exit")
	}
	if e.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty (no fallback to the frame's id)", e.SessionID)
	}
}

func TestExit_NativeChildSessionEnd_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	parent := seedRootWithIdentity(t, m, "%5", "cc", 100, "t100", "P1")
	child := seedChildFrame(t, m, "%5", "cc", 200, "t200", parent.FrameID)
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
	}

	ev := m.buildNormalizedForTest(t, req)

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].FrameID != parent.FrameID {
		t.Fatalf("frames = %+v, want only the parent (child %s deleted)", frames, child.FrameID)
	}
	if e, ok := exitOf(t, ev); ok {
		t.Fatalf("a child frame's SessionEnd must not carry pdx_exit, got %+v", e)
	}
}

func TestExit_ProxyDetachSessionEnd_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100", 200: "t200"})

	start := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1"}`),
	}
	if _, meta, err := m.applyFrameEvent(start, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("attach: %v", err)
	} else if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("fixture: attach reason = %q, want proxy_subagent_attached", meta.Reason)
	}

	end := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
	}
	ev := m.buildNormalizedForTest(t, end)

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].FrameID != parent.FrameID || len(frames[0].Subagents) != 0 {
		t.Fatalf("frames = %+v, want the cc parent with its proxy ref detached", frames)
	}
	if e, ok := exitOf(t, ev); ok {
		t.Fatalf("a proxy detach must not carry pdx_exit, got %+v", e)
	}
}

func TestExit_OrphanSessionEnd_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionEnd",
	}
	if e, ok := exitOf(t, m.buildNormalizedForTest(t, req)); ok {
		t.Fatalf("an orphan SessionEnd must not carry pdx_exit, got %+v", e)
	}
}

func TestExit_OrdinaryEventOnRoot_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxUserPromptSubmit",
	}
	if e, ok := exitOf(t, m.buildNormalizedForTest(t, req)); ok {
		t.Fatalf("a non-SessionEnd event must not carry pdx_exit, got %+v", e)
	}
}

// The handler wires the envelope onto the broadcast it sends, and the JSON on
// the wire is the exact shape the SPA parses.
func TestExit_HandlerBroadcastsEnvelopeOnTheWire(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = fakeProviderWithInstance(exitTestInstance)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{typeName: "cc", derive: deriveWithSessionDetail})
	root := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"t200","purdex_name":"PdxSessionEnd","raw_event":{"session_id":"S1"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ev := readSweepNormalizedEvent(t, sub)
	raw, ok := ev.Detail["pdx_exit"].(map[string]any)
	if !ok {
		t.Fatalf("broadcast carried no pdx_exit object: detail=%+v", ev.Detail)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	want := map[string]any{
		"agent_type": "cc", "session_id": "S1", "tmux_pane_id": "%5",
		"tmux_instance": exitTestInstance, "frame_id": root.FrameID, "reason": "session-end",
	}
	for k, v := range want {
		if raw[k] != v {
			t.Fatalf("pdx_exit[%q] = %v, want %v (envelope=%v)", k, raw[k], v, raw)
		}
	}
	if at, ok := raw["at"].(float64); !ok || at <= 0 {
		t.Fatalf("pdx_exit.at = %v, want a positive unix-ms number", raw["at"])
	}
	if len(raw) != len(want)+1 {
		t.Fatalf("pdx_exit keys = %v, want exactly the seven fields", keys)
	}
}

// The claim is atomic, not just the pre-check: a SessionStart that takes the
// frame over AFTER the SessionEnd read its snapshot (the snapshot still says
// the old run, or nothing) must still win. claimFrameEnd re-checks the row.
func TestClaimFrameEnd_NewerRunTookTheFrameAfterTheSnapshot(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	snapshot := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "")
	if err := m.frames.UpdateSessionIdentity(snapshot.FrameID, "S-new", "/w/p", 1); err != nil {
		t.Fatalf("UpdateSessionIdentity: %v", err)
	}
	exit := exitForFrame(snapshot, exitTestInstance, ExitReasonSessionEnd, 1)
	got, claimed, err := m.claimFrameEnd(snapshot, "S-old", exit)
	if err != nil || claimed || got != nil {
		t.Fatalf("claimFrameEnd = (%v, %v, %v), want no claim", got, claimed, err)
	}
	if frames, _ := m.frames.ListByPane("%5"); len(frames) != 1 {
		t.Fatalf("the newer run's frame was deleted: %+v", frames)
	}
	if got, claimed, err := m.claimFrameEnd(snapshot, "S-new", exit); err != nil || !claimed || got != exit {
		t.Fatalf("the current run's end = (%v, %v, %v), want claimed with the exit", got, claimed, err)
	}
}
