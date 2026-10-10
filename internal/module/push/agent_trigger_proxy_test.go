package push

import (
	"strings"
	"testing"
	"time"
)

// A proxy subagent's Stop (a codex a cc runs in its pane) is the main agent's tool finishing: no "task completed".
// Mutation gate: drop the FromProxy check in onNotify → red.
func TestAgentTrigger_AProxyStopIsNotPushed(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	for _, raw := range []string{"PdxStop", "PdxStopFailure"} {
		ev := nev("c1", raw, "idle", stopDetail("proxy done"))
		ev.FromProxy = true
		e.feed.emit(ev)
	}
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("main done")))
	e.waitSends(t, 1)
	time.Sleep(100 * time.Millisecond)
	e.apns.mu.Lock()
	calls := append([]sendCall(nil), e.apns.calls...)
	e.apns.mu.Unlock()
	if len(calls) != 1 || !strings.Contains(calls[0].Payload, "main done") {
		t.Fatalf("sends = %+v, want only the non-proxy Stop", calls)
	}
}

// A proxy that waits for a person still needs one.
func TestAgentTrigger_AProxyWaitingIsStillPushed(t *testing.T) {
	e := newAgentEnv(t, 0)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	ev := nev("c1", "PdxPermissionRequest", "waiting", map[string]any{"tool_name": "Bash"})
	ev.FromProxy = true
	e.feed.emit(ev)
	e.waitSends(t, 1)
}
