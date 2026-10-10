package agent

import (
	"context"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// ConfirmedOwners (the conversation's full lookup) carries the same light LightStatus does: the frame's hook status with
// the mod's light laid over it.
func TestConfirmedOwners_StatusCarriesTheModsLight(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	f := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	if _, err := m.frames.Upsert(withStatus(f, agentpkg.StatusRunning)); err != nil { // the hooks say running; Esc ran no Stop
		t.Fatal(err)
	}
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.ConfirmedOwners(context.Background(), "sess-1")
	if err != nil || len(owners) != 1 || owners[0].Status != "running" {
		t.Fatalf("without a mod: %+v err %v, want the frame's running", owners, err)
	}

	now := time.Now()
	m.modNow = func() time.Time { return now }
	m.modOverlayOn.Store(true)
	feedMod(m, "stream-owners",
		modEv("sess-1", modevents.TypeSessionStart, `{"cwd":"/w","surface":"terminal"}`),
		modEv("sess-1", modevents.TypeTurnStart, `{"turn_id":"t1"}`),
		modEv("sess-1", modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`),
	)
	owners, err = m.ConfirmedOwners(context.Background(), "sess-1")
	if err != nil || len(owners) != 1 || owners[0].Status != "idle" {
		t.Errorf("with the mod idle after an abort: %+v err %v, want idle", owners, err)
	}
}
