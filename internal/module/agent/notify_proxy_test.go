package agent

import (
	"net/http"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// A Stop of a proxy subagent (a codex that a cc runs in its pane) is a tool of the main agent finishing, not the
// main agent's "task completed": the NotifyEvent says so (FromProxy) and the push module skips it.

func proxyNotifyModule(t *testing.T) (*Module, chan NotifyEvent) {
	t.Helper()
	m := newProxyTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	got := make(chan NotifyEvent, 16)
	t.Cleanup(m.SubscribeNotify(func(ev NotifyEvent) { got <- ev }))
	return m, got
}

func seedCodexProxy(t *testing.T, m *Module) {
	t.Helper()
	seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
		ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
	}})
	turnAwareEnvAlive(t, 100, "t100")
}

// Mutation gate: drop the line that sets FromProxy at the emit → red.
func TestHandler_ProxyCodexStopPublishesFromProxy(t *testing.T) {
	m, got := proxyNotifyModule(t)
	seedCodexProxy(t, m)
	w := postHookEventAs(t, m, "codex", "PdxStop", 42, "t1", `{"turn_id":"t_a"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	ev := wantNotify(t, got)
	if ev.Event.RawEventName != "PdxStop" || !ev.FromProxy {
		t.Fatalf("event = %+v, want a PdxStop with FromProxy", ev)
	}
}

// A late / duplicate Stop whose ref is already gone is still a proxy's Stop: the sender owns no frame of its own, so
// it cannot be the pane's main agent.
func TestHandler_ProxyCodexStopNoMatchPublishesFromProxy(t *testing.T) {
	m, got := proxyNotifyModule(t)
	seedCodexProxy(t, m)
	w := postHookEventAs(t, m, "codex", "PdxStop", 42, "t1", `{"turn_id":"t_other"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ev := wantNotify(t, got); !ev.FromProxy {
		t.Fatalf("event = %+v, want FromProxy", ev)
	}
}

// The pane's own main agent (a frame of its own) is not a proxy.
func TestHandler_MainAgentStopIsNotFromProxy(t *testing.T) {
	m, got := proxyNotifyModule(t)
	seedCodexProxy(t, m)
	w := postHookEventAs(t, m, "cc", "PdxStop", 100, "t100", `{"session_id":"S"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ev := wantNotify(t, got); ev.FromProxy {
		t.Fatalf("event = %+v, want not FromProxy", ev)
	}
}

func TestIsProxySubagentStopReason(t *testing.T) {
	for _, r := range []string{"proxy_subagent_detached_on_stop_turn", "proxy_subagent_detached_on_stop", "proxy_subagent_stop_no_match", "proxy_subagent_stop_parse_failed"} {
		if !isProxySubagentStopReason(r) {
			t.Errorf("%s should be a proxy stop", r)
		}
	}
	for _, r := range []string{"", "updated_frame", "proxy_subagent_upserted_on_user_prompt", "parent_frame_found"} {
		if isProxySubagentStopReason(r) {
			t.Errorf("%q should not be a proxy stop", r)
		}
	}
}
