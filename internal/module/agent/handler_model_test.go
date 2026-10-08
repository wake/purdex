package agent

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// A hook event describes the whole tmux session, but its model is its
// sender's. When the sender is not the pane that represents the session, the
// wire event must not pair the representative's status with the sender's
// model (U1-2b-1).

// modelHookBody is a hook of the cc sender in pane (pid), carrying model in
// its raw event so the fake provider can echo it.
func modelHookBody(pane string, pid int, purdexName, model string) string {
	return `{"tmux_session":"work","tmux_pane_id":"` + pane + `","sender_pid":` + itoa(pid) +
		`,"sender_start_time":"` + raceFrameStart + `","purdex_name":"` + purdexName + `","agent_type":"cc","raw_event":{"model":"` + model + `"}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestHandler_ModelOnlyFromRepresentativePane(t *testing.T) {
	r := newWorkerRig(t)
	r.m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, raw json.RawMessage) agentpkg.DeriveResult {
			var p struct{ Model string }
			_ = json.Unmarshal(raw, &p)
			status := agentpkg.StatusRunning
			if strings.Contains(event, "Permission") {
				status = agentpkg.StatusWaiting
			}
			return agentpkg.DeriveResult{Valid: true, Status: status, Model: p.Model}
		},
	})
	// A (%5, opus) started first and waits; B (%7, sonnet) started later and
	// runs. A outranks B, so A represents the session.
	a := seedIdentityFrame(t, r.m, "%5", "cc", 200, raceFrameStart, 10, "", "/w")
	seedIdentityFrame(t, r.m, "%7", "cc", 300, raceFrameStart, 20, "", "/w")
	if err := r.m.frames.UpdateStatusAndLastSeen(a.FrameID, agentpkg.StatusWaiting, 11); err != nil {
		t.Fatal(err)
	}

	post := func(body string) emitted {
		t.Helper()
		if w := postEvent(r.m, body); w.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", w.Code, w.Body.String())
		}
		return lastEmit(t, "hook", r.drain(t))
	}

	// B is not the representative: A's waiting goes out without B's model.
	got := post(modelHookBody("%7", 300, "PdxPreCompact", "sonnet"))
	if got.Ev.Status != "waiting" || got.Ev.Model != "" {
		t.Fatalf("B's hook: status %q model %q, want waiting with no model", got.Ev.Status, got.Ev.Model)
	}
	// A is the representative: its own model still goes out.
	got = post(modelHookBody("%5", 200, "PdxPermissionRequest", "opus"))
	if got.Ev.Status != "waiting" || got.Ev.Model != "opus" {
		t.Fatalf("A's hook: status %q model %q, want waiting with opus", got.Ev.Status, got.Ev.Model)
	}
}
