package agent

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The hook frame the SPA receives carries from_proxy for a proxy subagent's Stop (the desktop notification and the
// unread mark skip it), and omits it for the main agent's.
func broadcastFromProxy(t *testing.T, m *Module, agent string, pid int, start, body string) (present bool, value any) {
	t.Helper()
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	w := postHookEventAs(t, m, agent, "PdxStop", pid, start, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	for {
		select {
		case b := <-sub.SendCh():
			var env struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}
			if json.Unmarshal(b, &env) != nil || env.Type != "hook" {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(env.Value), &ev); err != nil {
				t.Fatalf("hook value: %v", err)
			}
			value, present = ev["from_proxy"]
			return present, value
		default:
			t.Fatal("no hook frame broadcast")
			return
		}
	}
}

// Mutation gate: stop setting NormalizedEvent.FromProxy at the emit → red.
func TestBroadcast_ProxyCodexStopCarriesFromProxy(t *testing.T) {
	m, _ := proxyNotifyModule(t)
	seedCodexProxy(t, m)
	if present, v := broadcastFromProxy(t, m, "codex", 42, "t1", `{"turn_id":"t_a"}`); !present || v != true {
		t.Fatalf("from_proxy = %v (present %v), want true", v, present)
	}
}

func TestBroadcast_MainAgentStopOmitsFromProxy(t *testing.T) {
	m, _ := proxyNotifyModule(t)
	seedCodexProxy(t, m)
	if present, _ := broadcastFromProxy(t, m, "cc", 100, "t100", `{"session_id":"S"}`); present {
		t.Fatal("main agent Stop must not carry from_proxy")
	}
}
