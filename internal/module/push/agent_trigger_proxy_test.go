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

// A suppressed proxy Stop still passes the gate: it records its timestamp (rule 0), so a main Stop that arrives later
// with an older one is stale. Mutation gate: move the suppression before the gate → red.
func TestAgentTrigger_AProxyStopStillRecordsFreshness(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	proxy := nev("c1", "PdxStop", "idle", stopDetail("proxy"))
	proxy.FromProxy = true
	older := nev("c1", "PdxStop", "idle", stopDetail("main, older"))
	older.Event.BroadcastTs = proxy.Event.BroadcastTs - 1
	e.feed.emit(proxy)
	e.feed.emit(older)
	e.noSends(t)
}

// A proxy StopFailure still takes part in the error debounce: the identical main error right after it is not pushed.
func TestAgentTrigger_AProxyStopFailureStillFeedsTheErrorDebounce(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	proxy := nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit"})
	proxy.FromProxy = true
	e.feed.emit(proxy)
	e.noSends(t)
	e.clock.advance(10 * time.Second)
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit"}))
	e.noSends(t)
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
