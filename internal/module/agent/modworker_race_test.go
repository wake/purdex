package agent

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// The ordering race the overlay switch exists for (plan, "The overlay
// switch"): the mod queues events and flushes 150 ms after the first, while
// hooks arrive almost at once. A hook emit therefore reads the mod's state
// from before the event the hook is about to be followed by. Only the
// worker's round after the mod event lands makes the light right again; each
// test drives the real handler path for the hook and ends on a round.

const (
	raceFrameStart = "Sun Apr 20 01:30:00 2026"
	racePane       = "%5"
)

// hookBody is a hook of the sender nonTmuxTail names (pid 200) in session
// "work", pane %5, under purdexName.
func hookBody(purdexName string) string {
	return strings.Replace(
		`{"tmux_session":"work","tmux_pane_id":"%5",`+nonTmuxTail+`,"raw_event":{}}`,
		`"purdex_name":"PdxStop"`, `"purdex_name":"`+purdexName+`"`, 1)
}

// raceRig is a worker rig whose cc hooks answer waiting for a permission
// request and idle for everything else, with the sender's frame on sid1.
func raceRig(t *testing.T) *workerRig {
	t.Helper()
	r := newWorkerRig(t)
	r.m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "PdxPermissionRequest" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	seedIdentityFrame(t, r.m, racePane, "cc", 200, raceFrameStart, 10, modSID1, "/w")
	return r
}

func (r *workerRig) hook(t *testing.T, purdexName string) {
	t.Helper()
	if w := postEvent(r.m, hookBody(purdexName)); w.Code != http.StatusOK {
		t.Fatalf("%s: status %d body=%s", purdexName, w.Code, w.Body.String())
	}
}

func lastEmit(t *testing.T, what string, got []emitted) emitted {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: nothing emitted", what)
	}
	return got[len(got)-1]
}

func wantLastEmit(t *testing.T, what string, got []emitted, status, source string) {
	t.Helper()
	if e := lastEmit(t, what, got); e.Ev.Status != status || e.Ev.Source != source {
		t.Fatalf("%s: last emit is %s/%s, want %s/%s (all: %+v)", what, e.Ev.Status, e.Ev.Source, status, source, got)
	}
}

// TestModWorker_StopHookBeforeTurnComplete: the Stop hook lands while the
// mod still says running, so the hook's own frame is running / mod; the
// mod's turn.complete arrives 150 ms later and the worker's next round
// must put idle on the wire.
func TestModWorker_StopHookBeforeTurnComplete(t *testing.T) {
	r := raceRig(t)
	feedMod(r.m, modStrm, modStart, modTurnStart)

	r.hook(t, "PdxStop")
	wantLastEmit(t, "hook Stop", r.drain(t), "running", "mod")

	complete := modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	complete.Seq = 3
	feedMod(r.m, modStrm, complete)
	r.round()

	wantLastEmit(t, "after turn.complete", r.drain(t), "idle", "mod")
}

// TestModWorker_PermissionAskBeforeHookOrder: the same shape for waiting. The
// permission hook lands before the mod's tool.check, so its frame still says
// running; the ask and then its approval each need a worker round.
func TestModWorker_PermissionAskBeforeHookOrder(t *testing.T) {
	r := raceRig(t)
	feedMod(r.m, modStrm, modStart, modTurnStart)

	r.hook(t, "PdxPermissionRequest")
	wantLastEmit(t, "hook PermissionRequest", r.drain(t), "running", "mod")

	feedMod(r.m, modStrm, modAsk(modSID1, "toolu_1", 3))
	r.round()
	wantLastEmit(t, "after tool.check ask", r.drain(t), "waiting", "mod")

	approved := modEv(modSID1, modevents.TypeToolApproved, `{"tool_use_id":"toolu_1"}`)
	approved.Seq = 4
	feedMod(r.m, modStrm, approved)
	r.round()
	wantLastEmit(t, "after tool.approved", r.drain(t), "running", "mod")
}
