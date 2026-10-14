package agent

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

// postAs sends one hook from the fixture's agent process (pid 100) under the given tmux session name.
func postAs(t *testing.T, m *Module, tmuxName, name, raw string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"tmux_session": tmuxName, "tmux_pane_id": "%5", "sender_pid": 100, "sender_start_time": "t100",
		"purdex_name": name, "agent_type": "cc", "raw_event": json.RawMessage(raw),
	})
	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != 200 {
		t.Fatalf("%s answered %d: %s", name, w.Code, w.Body.String())
	}
}

func predFixture(t *testing.T) (*Module, chan SessionStartEvent) {
	t.Helper()
	m := newSessionStartTestModule(t)
	withProcessTree(t, map[int]int{100: 999})
	got := make(chan SessionStartEvent, 8)
	t.Cleanup(m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev }))
	return m, got
}

func wantPred(t *testing.T, m *Module, sid, wantPrev string) {
	t.Helper()
	prev, _, ok, err := PredecessorReaderOf(m).PredecessorOf(sid)
	if err != nil {
		t.Fatal(err)
	}
	if wantPrev == "" {
		if ok {
			t.Fatalf("%s: unexpected predecessor %q", sid, prev)
		}
		return
	}
	if !ok || prev != wantPrev {
		t.Fatalf("%s: predecessor = %q ok=%v, want %q", sid, prev, ok, wantPrev)
	}
}

func startRaw(sid, source string) string {
	return `{"session_id":"` + sid + `","cwd":"/w","source":"` + source + `"}`
}

func nextStart(t *testing.T, ch chan SessionStartEvent) SessionStartEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no SessionStart event")
		return SessionStartEvent{}
	}
}

// Arrival order 1: the clear's SessionStart arrives while the frame still holds the old id.
func TestPredecessor_StartFirst_FrameIdIsCaptured(t *testing.T) {
	m, got := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	nextStart(t, got)
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "A")
	if ev := nextStart(t, got); ev.PrevSessionID != "A" || ev.Source != "clear" {
		t.Fatalf("event = %+v", ev)
	}
}

// Arrival order 2: the SessionEnd already removed the frame; last_agent_run answers.
func TestPredecessor_EndFirst_LastRunFallback(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionEnd", `{"session_id":"A"}`)
	if run, _ := m.frames.LastRun("work-code"); run == nil || run.SessionID != "A" {
		t.Fatalf("fixture: last run = %+v", run)
	}
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "A")
}

// Arrival order 3: the old SessionEnd arrives after the Start overwrote the id: the exact-id gate refuses it, and the
// record the Start already made stands.
func TestPredecessor_LateOldSessionEnd_RecordStands(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	postAs(t, m, "work", "PdxSessionEnd", `{"session_id":"A"}`)
	wantPred(t, m, "B", "A")
	if f, _ := m.frames.GetByIdentity("%5", 100, "t100"); f == nil || f.SessionID != "B" {
		t.Fatalf("frame after the late End = %+v", f)
	}
}

// Arrival order 4: a sweep removed the frame before the End: no last run, nothing recorded.
func TestPredecessor_SweepRemovedFrame_NothingRecorded(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	f, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if ok, err := m.frames.ClaimEnd(f.FrameID, "", nil); err != nil || !ok {
		t.Fatalf("sweep claim: %v %v", ok, err)
	}
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "")
}

func TestPredecessor_StaleLastRunIsSkipped(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	f, _ := m.frames.GetByIdentity("%5", 100, "t100")
	old := time.Now().UnixMilli() - 200_000 // > 120 s
	if ok, err := m.frames.ClaimEnd(f.FrameID, "A", &store.RunEnd{Code: "work-code", EndedAt: old}); err != nil || !ok {
		t.Fatalf("end: %v %v", ok, err)
	}
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "")
}

func TestPredecessor_FreshLastRunWithinWindowIsUsed(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	f, _ := m.frames.GetByIdentity("%5", 100, "t100")
	recent := time.Now().UnixMilli() - 100_000 // < 120 s
	if ok, err := m.frames.ClaimEnd(f.FrameID, "A", &store.RunEnd{Code: "work-code", EndedAt: recent}); err != nil || !ok {
		t.Fatalf("end: %v %v", ok, err)
	}
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "A")
}

func TestPredecessor_UnresolvableCode_NoFallback(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionEnd", `{"session_id":"A"}`)
	postAs(t, m, "nope", "PdxSessionStart", startRaw("B", "clear")) // a tmux name the session module does not know
	wantPred(t, m, "B", "")
}

func TestPredecessor_SameIdSkipped(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "clear"))
	wantPred(t, m, "A", "")
}

func TestPredecessor_OtherSourcesRecordNothing(t *testing.T) {
	for _, source := range []string{"resume", "startup", "compact", ""} {
		t.Run("source="+source, func(t *testing.T) {
			m, _ := predFixture(t)
			postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
			postAs(t, m, "work", "PdxSessionStart", startRaw("B", source))
			wantPred(t, m, "B", "")
		})
	}
}

func TestPredecessor_SecondRecordForSameSessionIgnored(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	postAs(t, m, "work", "PdxSessionStart", startRaw("C", "clear"))
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear")) // B again, now replacing C
	wantPred(t, m, "B", "A")
	wantPred(t, m, "C", "B")
}

// A recent last run of another tmux generation (the code was reused after a tmux restart) is not this clear's predecessor.
func TestPredecessor_LastRunOfAnotherTmuxGenerationIsSkipped(t *testing.T) {
	m, _ := predFixture(t)
	postAs(t, m, "work", "PdxSessionStart", startRaw("A", "startup"))
	postAs(t, m, "work", "PdxSessionEnd", `{"session_id":"A"}`)
	m.sessions = fakeProviderWithInstance("9999:1799999999") // tmux restarted since the run ended
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "")
}

// A recent run of another agent type on the same tmux code (a codex that just ended) is not a Claude session's predecessor.
func TestPredecessor_LastRunOfAnotherAgentTypeIsSkipped(t *testing.T) {
	m, _ := predFixture(t)
	f := seedRootWithIdentity(t, m, "%9", "codex", 300, "t300", "X")
	if err := m.frames.SetTmuxInstance(f.FrameID, "inst-1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.frames.ClaimEnd(f.FrameID, "X", &store.RunEnd{Code: "work-code", EndedAt: time.Now().UnixMilli()}); err != nil || !ok {
		t.Fatalf("end: %v %v", ok, err)
	}
	if run, _ := m.frames.LastRun("work-code"); run == nil || run.AgentType != "codex" {
		t.Fatalf("fixture: last run = %+v", run)
	}
	postAs(t, m, "work", "PdxSessionStart", startRaw("B", "clear"))
	wantPred(t, m, "B", "")
}
