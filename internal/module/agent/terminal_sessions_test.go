package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

func TestLiveBySessionID(t *testing.T) {
	m := newTestModule(t)
	live := seedRootWithIdentity(t, m, "%1", "cc", 101, "st-101", "S")
	dead := seedRootWithIdentity(t, m, "%2", "cc", 102, "st-102", "S")
	reused := seedRootWithIdentity(t, m, "%3", "cc", 103, "st-103", "S")
	unreadable := seedRootWithIdentity(t, m, "%4", "cc", 104, "st-104", "S")
	_ = seedRootWithIdentity(t, m, "%5", "codex", 105, "st-105", "S")
	_ = seedRootWithIdentity(t, m, "%6", "cc", 106, "st-106", "OTHER")
	child := seedChildFrame(t, m, "%1", "cc", 107, "st-107", live.FrameID)
	if err := m.frames.UpdateSessionIdentity(child.FrameID, "S", "", 1<<40); err != nil {
		t.Fatal(err)
	}

	withLivePids(t, map[int]string{101: "st-101", 103: "st-OTHER", 104: "st-104", 105: "st-105", 106: "st-106", 107: "st-107"})
	// 104: alive but its start time cannot be read.
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 104 {
			return "", errors.New("ps failed")
		}
		return prev(pid)
	}
	t.Cleanup(func() { processStartTimeFn = prev })

	got, err := m.LiveBySessionID(context.Background(), "cc", "S")
	if err != nil {
		t.Fatal(err)
	}
	byFrame := map[string]TerminalSession{}
	for _, g := range got {
		byFrame[g.FrameID] = g
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 (live verified + unreadable): %+v", len(got), got)
	}
	if g := byFrame[live.FrameID]; !g.Verified || g.SessionID != "S" || g.PaneID != "%1" || g.AgentType != "cc" || g.Cwd != "/w/p" {
		t.Errorf("live frame wrong: %+v", g)
	}
	if g, ok := byFrame[unreadable.FrameID]; !ok || g.Verified {
		t.Errorf("unreadable start time: want present and unverified, got %+v ok=%v", g, ok)
	}
	for _, f := range []string{dead.FrameID, reused.FrameID, child.FrameID} {
		if _, ok := byFrame[f]; ok {
			t.Errorf("frame %s must not be returned", f)
		}
	}
}

func TestLiveBySessionID_EmptySessionIDReturnsNothing(t *testing.T) {
	m := newTestModule(t)
	got, err := m.LiveBySessionID(context.Background(), "cc", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// --- SessionStart subscription ----------------------------------------------

// newSessionStartTestModule is newProvenanceTestModule whose fake cc provider
// also does what the real one does for a compact SessionStart: Valid=false,
// reason compact_ignored (internal/agent/cc/status.go).
func newSessionStartTestModule(t *testing.T) *Module {
	t.Helper()
	m := newProvenanceTestModule(t, "inst-1")
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(&fakeAgentProvider{typeName: "cc", derive: func(name string, raw json.RawMessage) agentpkg.DeriveResult {
		var p struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(raw, &p)
		if name == "PdxSessionStart" && p.Source == "compact" {
			return agentpkg.DeriveResult{Valid: false, Reason: "compact_ignored"}
		}
		return deriveWithSessionDetail(name, raw)
	}})
	return m
}

func postHookEvent(t *testing.T, m *Module, purdexName string, senderPID int, startTime string, raw string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tmux_session":      "work",
		"tmux_pane_id":      "%5",
		"sender_pid":        senderPID,
		"sender_start_time": startTime,
		"purdex_name":       purdexName,
		"agent_type":        "cc",
		"raw_event":         json.RawMessage(raw),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	return w
}

func postRootSessionStart(t *testing.T, m *Module, raw string) *httptest.ResponseRecorder {
	t.Helper()
	withProcessTree(t, map[int]int{100: 999})
	return postHookEvent(t, m, "PdxSessionStart", 100, "t100", raw)
}

// postChildSessionStart sends the hook from pid 100, a descendant of a seeded
// root frame (pid 50) on the same pane.
func postChildSessionStart(t *testing.T, m *Module, raw string) *httptest.ResponseRecorder {
	t.Helper()
	seedRootWithIdentity(t, m, "%5", "cc", 50, "t50", "ROOT")
	withProcessTree(t, map[int]int{100: 50, 50: 999})
	withLivePids(t, map[int]string{50: "t50", 100: "t100"})
	return postHookEvent(t, m, "PdxSessionStart", 100, "t100", raw)
}

func postRootPrompt(t *testing.T, m *Module, raw string) *httptest.ResponseRecorder {
	t.Helper()
	withProcessTree(t, map[int]int{100: 999})
	return postHookEvent(t, m, "PdxUserPromptSubmit", 100, "t100", raw)
}

func TestSessionStartSubscription_DeliversGrantedStart(t *testing.T) {
	m := newSessionStartTestModule(t)
	got := make(chan SessionStartEvent, 4)
	unsub := m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })
	defer unsub()

	postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"resume","transcript_path":"/t/S.jsonl"}`)

	select {
	case ev := <-got:
		if ev.SessionID != "S" || ev.Source != "resume" || ev.TranscriptPath != "/t/S.jsonl" || ev.FrameID == "" || ev.AgentType != "cc" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SessionStartEvent")
	}
}

func TestSessionStartSubscription_NoEventWithoutEnvelope(t *testing.T) {
	m := newSessionStartTestModule(t)
	got := make(chan SessionStartEvent, 4)
	defer m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })()

	postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"compact"}`)   // compact_ignored
	postChildSessionStart(t, m, `{"session_id":"S2","cwd":"/w","source":"startup"}`) // has a parent frame
	postRootPrompt(t, m, `{"session_id":"S","cwd":"/w"}`)                            // not a SessionStart
	postRootPrompt(t, m, `{"session_id":"S","cwd":"/w","source":"resume"}`)          // a non-SessionStart that carries source

	select {
	case ev := <-got:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSessionStartSubscription_UnsubscribeAndPanicIsolation(t *testing.T) {
	m := newSessionStartTestModule(t)
	got := make(chan SessionStartEvent, 4)
	unsubPanic := m.SubscribeSessionStart(func(SessionStartEvent) { panic("boom") })
	defer unsubPanic()
	unsub := m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })

	rec := postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"startup"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("healthy subscriber starved by a panicking one")
	}
	unsub()
	postRootSessionStart(t, m, `{"session_id":"S3","cwd":"/w","source":"startup"}`)
	select {
	case ev := <-got:
		t.Fatalf("delivered after unsubscribe: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
