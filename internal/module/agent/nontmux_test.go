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
	"github.com/wake/purdex/internal/tmux"
)

func nonTmuxModule(t *testing.T) (*Module, *core.EventSubscriber) {
	t.Helper()
	m := newTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	return m, sub
}

func postEvent(m *Module, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	return w
}

const nonTmuxTail = `"sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","agent_type":"cc"`

func TestHandleEvent_NonTmuxSessionIDInRawEvent_BroadcastsUnderDerivedCode(t *testing.T) {
	m, sub := nonTmuxModule(t)
	w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{"session_id":"abc-123"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	select {
	case msg := <-sub.SendCh():
		var env struct{ Type, Session, Value string }
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "hook" || env.Session != "cc-abc-123" {
			t.Fatalf("type=%q session=%q, want hook / cc-abc-123", env.Type, env.Session)
		}
		if !strings.Contains(env.Value, `"agent_type":"cc"`) || !strings.Contains(env.Value, `"status":"idle"`) {
			t.Fatalf("value = %s", env.Value)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no broadcast for a non-tmux event")
	}
}

func TestHandleEvent_NonTmuxExplicitSessionIDField(t *testing.T) {
	m, sub := nonTmuxModule(t)
	w := postEvent(m, `{"session_id":"sid-9",`+nonTmuxTail+`,"raw_event":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	select {
	case msg := <-sub.SendCh():
		if !strings.Contains(string(msg), `"session":"cc-sid-9"`) {
			t.Fatalf("msg = %s", msg)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no broadcast")
	}
}

func TestHandleEvent_NoIdentityAndNoSessionID_Still400(t *testing.T) {
	m, sub := nonTmuxModule(t)
	w := postEvent(m, `{"tmux_session":"","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected broadcast %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// A half-present tmux identity is malformed, not a non-tmux session: the
// session_id must not rescue it.
func TestHandleEvent_PartialTmuxIdentityWithSessionID_Still400(t *testing.T) {
	m, _ := nonTmuxModule(t)
	w := postEvent(m, `{"tmux_session":"work","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{"session_id":"x"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleEvent_TmuxPathUnchangedBySessionID(t *testing.T) {
	m, sub := nonTmuxModule(t)
	m.tmux = tmux.NewFakeExecutor()
	m.sessions = &fakeSessionProvider{}
	w := postEvent(m, `{"tmux_session":"work","tmux_pane_id":"%5","session_id":"sid-1",`+nonTmuxTail+`,"raw_event":{"session_id":"sid-1"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	select {
	case msg := <-sub.SendCh():
		if strings.Contains(string(msg), `cc-sid-1`) {
			t.Fatalf("tmux event leaked into the non-tmux code: %s", msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// Verification keeps the pid checks for a non-tmux sender and drops only the
// pane-tree one (there is no pane).
func TestVerifyEvent_NonTmuxKeepsPidChecksSkipsPaneTree(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{typeName: "cc", identify: func(agentpkg.ProcessInfo) bool { return true }})
	m.tmux = nil
	req := EventRequest{AgentType: "cc", SenderPID: 200, SenderStartTime: "Sun Apr 20 01:30:00 2026"}
	if d := m.verifyEvent(req); !d.Accepted {
		t.Fatalf("non-tmux verify rejected: %s", d.Reason)
	}
	req.SenderStartTime = "other"
	if d := m.verifyEvent(req); d.Reason != "pid_reused" {
		t.Fatalf("reason = %q, want pid_reused", d.Reason)
	}
	req.SenderStartTime, req.TmuxPaneID = "Sun Apr 20 01:30:00 2026", "%1"
	if d := m.verifyEvent(req); d.Reason != "tmux_unavailable" {
		t.Fatalf("tmux path reason = %q, want tmux_unavailable", d.Reason)
	}
}
