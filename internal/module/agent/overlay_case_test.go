package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// Callers may spell the session id in another case than the frame and the mod's stream hold it (LightStatus and
// ConfirmedOwners match case-insensitively). The overlay must still find the mod's stream.
func TestHeaderLight_ASessionIdInAnotherCaseStillGetsTheModsLight(t *testing.T) {
	r, frame := escRig(t)
	r.setClock(sec(1))
	r.modRunning()
	r.setClock(sec(2))
	r.hook(t, "PdxUserPromptSubmit")
	r.setClock(sec(4))
	r.modEventAt(sec(4), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)
	got, ok := r.m.LightStatus(strings.ToUpper(modSID1), frame)
	if !ok || got != "idle" {
		t.Errorf("upper-case session id: %q ok %v, want idle", got, ok)
	}
}

func TestConfirmedOwners_ASessionIdInAnotherCaseStillGetsTheModsLight(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	f := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	if _, err := m.frames.Upsert(withStatus(f, agentpkg.StatusRunning)); err != nil {
		t.Fatal(err)
	}
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})
	now := time.Now()
	m.modNow = func() time.Time { return now }
	m.modOverlayOn.Store(true)
	feedMod(m, "stream-case",
		modEv("sess-1", modevents.TypeSessionStart, `{"cwd":"/w","surface":"terminal"}`),
		modEv("sess-1", modevents.TypeTurnStart, `{"turn_id":"t1"}`),
		modEv("sess-1", modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`),
	)
	owners, err := m.ConfirmedOwners(context.Background(), "SESS-1")
	if err != nil || len(owners) != 1 || owners[0].Status != "idle" {
		t.Errorf("upper-case session id: %+v err %v, want idle", owners, err)
	}
}
