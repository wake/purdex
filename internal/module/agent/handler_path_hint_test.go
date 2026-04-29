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
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

func TestHandleEvent_EmitsPathHintForCCPreToolUseRead(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxPreToolUse","raw_event":{"cwd":"/x","tool_name":"Read","tool_input":{"file_path":"/x/y/z.go"}},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if !awaitPathHint(t, sub, "code-work", "/x/y", "/x", []string{"z.go", `"path":`, `"basename"`}) {
		t.Fatal("expected agent.path_hint broadcast within deadline")
	}
}

func TestHandleEvent_DoesNotEmitPathHintForBashTool(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxPreToolUse","raw_event":{"tool_name":"Bash","tool_input":{"command":"ls"}},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if sawPathHintWithin(sub, 80*time.Millisecond) {
		t.Fatal("Bash tool should not emit agent.path_hint")
	}
}

func TestHandleEvent_DoesNotEmitPathHintForOpenCode(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PreToolUse","raw_event":{"cwd":"/x","tool_name":"Read","tool_input":{"file_path":"/x/y/z.go"}},"agent_type":"opencode"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	if sawPathHintWithin(sub, 80*time.Millisecond) {
		t.Fatal("opencode agent_type should not emit cc-only agent.path_hint")
	}
}

// awaitPathHint scans broadcasts for an agent.path_hint matching the expected
// session, dir, and cwd, asserting that no banned tokens leak into the payload.
func awaitPathHint(t *testing.T, sub *core.EventSubscriber, wantSession, wantDir, wantCwd string, bannedTokens []string) bool {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct{ Type, Session, Value string }
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v (%s)", err, string(msg))
			}
			if env.Type != "agent.path_hint" {
				continue
			}
			if env.Session != wantSession {
				t.Fatalf("agent.path_hint session = %q, want %q", env.Session, wantSession)
			}
			if !strings.Contains(env.Value, `"dir":"`+wantDir+`"`) {
				t.Errorf("agent.path_hint value missing dir=%q; got %s", wantDir, env.Value)
			}
			if !strings.Contains(env.Value, `"cwd":"`+wantCwd+`"`) {
				t.Errorf("agent.path_hint value missing cwd=%q; got %s", wantCwd, env.Value)
			}
			for _, banned := range bannedTokens {
				if strings.Contains(env.Value, banned) {
					t.Errorf("agent.path_hint payload must not contain %q; got %s", banned, env.Value)
				}
			}
			return true
		case <-deadline:
			return false
		}
	}
}

func sawPathHintWithin(sub *core.EventSubscriber, d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct{ Type string }
			if err := json.Unmarshal(msg, &env); err == nil && env.Type == "agent.path_hint" {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
