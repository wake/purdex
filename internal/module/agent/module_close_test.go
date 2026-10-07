package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

var _ core.Closer = (*Module)(nil)

// T5: Stop must leave the trace sink open (HTTP is still draining at that
// point); Close is what shuts it. Close is idempotent and nil-sink safe.
func TestModule_StopKeepsTraceSinkOpen_CloseClosesIt(t *testing.T) {
	m := newTestModule(t)
	if m.traceSink == nil {
		t.Fatal("expected a trace sink in the test module")
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	m.traceSink.Enqueue(closeTestRecord("after-stop"))
	m.traceSink.FlushForTest()
	if got := m.traceSink.dropped.Load(); got != 0 {
		t.Fatalf("record enqueued after Stop was dropped (dropped=%d)", got)
	}
	if got := len(listAllChains(t, m.traces)); got != 1 {
		t.Fatalf("persisted chains after Stop = %d, want 1", got)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	captureLog(t)
	m.traceSink.Enqueue(closeTestRecord("after-close"))
	if got := m.traceSink.dropped.Load(); got != 1 {
		t.Fatalf("dropped after Close = %d, want 1", got)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestModule_CloseWithNilTraceSink(t *testing.T) {
	m := newTestModule(t)
	m.traceSink = nil
	if err := m.Close(); err != nil {
		t.Fatalf("Close with nil sink: %v", err)
	}
}

// T6: a hook request arriving after the sink closed still succeeds and
// updates state; only its trace is dropped.
func TestHandleEvent_AfterTraceSinkClosed_StillProcessesHook(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%7", "work")
	m.tmux = fakeTmux
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "session-code-1", Name: "work"}},
	}
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	captureLog(t)
	m.traceSink.Close()

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"purdex_name":"UserPromptSubmit",
		"raw_event":{"prompt":"hi"},
		"agent_type":"codex",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want running", got)
	}
	if n := m.traceSink.dropped.Load(); n < 1 {
		t.Fatalf("dropped = %d, want >= 1 (the hook's trace)", n)
	}
}
