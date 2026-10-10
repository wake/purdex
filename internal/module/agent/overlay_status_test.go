package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// The conversation's header reads the frame store (LightStatus), not the pane's broadcast. A turn the hooks never close
// — Esc runs no Stop hook — would stay 'running' there while the pane's own light is idle, unless the mod's light is laid
// over it by the same rule the broadcast uses.

func escRig(t *testing.T) (*workerRig, string) {
	t.Helper()
	r := edgeRig(t)
	withLivePids(t, map[int]string{200: raceFrameStart})
	f, err := r.m.frames.GetByIdentity(racePane, 200, raceFrameStart)
	if err != nil || f == nil {
		t.Fatalf("seeded frame: %v %v", f, err)
	}
	return r, f.FrameID
}

func headerLight(t *testing.T, r *workerRig, frameID string) string {
	t.Helper()
	got, ok := r.m.LightStatus(modSID1, frameID)
	if !ok {
		t.Fatal("LightStatus: no live frame")
	}
	return got
}

func TestHeaderLight_EscLeavesNoStopHookButTheModSaysIdle(t *testing.T) {
	r, frame := escRig(t)
	r.setClock(sec(1))
	r.modRunning()
	r.setClock(sec(2))
	r.hook(t, "PdxUserPromptSubmit")
	r.round()
	if got := headerLight(t, r, frame); got != "running" {
		t.Fatalf("during the turn: %q, want running", got)
	}
	r.setClock(sec(4))
	r.modEventAt(sec(4), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`) // Esc: no Stop hook
	if got := headerLight(t, r, frame); got != "idle" {
		t.Errorf("after Esc: %q, want idle (the frame row alone still says running)", got)
	}
	// the pane's own light — the broadcast — is unchanged by this: idle from the mod
	r.round()
	wantLight(t, "pane", r.light(t), agentpkg.StatusIdle, SourceMod)
}

func TestHeaderLight_AHookNewerThanTheModEventStillDecides(t *testing.T) {
	r, frame := escRig(t)
	r.setClock(sec(1))
	r.modIdle()
	r.setClock(sec(5))
	r.hook(t, "PdxUserPromptSubmit") // a new prompt: the hook is ahead of the mod's turn.start
	if got := headerLight(t, r, frame); got != "running" {
		t.Errorf("hook edge newer than the mod: %q, want running", got)
	}
}

func TestHeaderLight_WaitingFollowsTheModsAsk(t *testing.T) {
	r, frame := escRig(t)
	r.setClock(sec(1))
	r.modRunning()
	r.setClock(sec(2))
	r.modEventAt(sec(2), modevents.TypeToolCheck, `{"tool_use_id":"toolu_1","decision":"ask"}`)
	if got := headerLight(t, r, frame); got != "waiting" {
		t.Errorf("a question open: %q, want waiting", got)
	}
}

func TestOverlayStatus_FallsBackToTheHookStatus(t *testing.T) {
	m := newTestModule(t)
	if got := m.overlayStatus(modSID1, "f1", "running"); got != "running" {
		t.Errorf("no mod at all: %q", got)
	}
	if got := m.overlayStatus("", "f1", "idle"); got != "idle" {
		t.Errorf("no session: %q", got)
	}
	var nilMod *Module
	if got := nilMod.overlayStatus(modSID1, "f1", "idle"); got != "idle" {
		t.Errorf("nil module: %q", got)
	}
}

func TestOverlayStatus_OverlayOffOrAnEndedStreamLeavesTheHookStatus(t *testing.T) {
	r, frame := escRig(t)
	r.setClock(sec(1))
	r.modIdle()
	if got := r.m.overlayStatus(modSID1, frame, "running"); got != "idle" {
		t.Fatalf("live stream: %q, want the mod's idle", got)
	}
	r.m.modOverlayOn.Store(false)
	if got := r.m.overlayStatus(modSID1, frame, "running"); got != "running" {
		t.Errorf("overlay off: %q, want the hook status", got)
	}
	r.m.modOverlayOn.Store(true)
	r.setClock(sec(2))
	r.modEvent(modevents.TypeSessionEnd, `{"reason":"exit"}`)
	if got := r.m.overlayStatus(modSID1, frame, "running"); got != "running" {
		t.Errorf("ended stream: %q, want the hook status", got)
	}
}
