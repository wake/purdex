package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

func TestLightStatus_NewestLiveRootWinsAndNothingElseIsConsulted(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t) // no tmux session is added: the light must not need one
	a := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-1", "/w")
	b := seedIdentityFrame(t, m, "%6", "cc", 101, "t101", 20, "sess-1", "/w")
	if _, err := m.frames.Upsert(withStatus(a, agentpkg.StatusIdle)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.frames.Upsert(withStatus(b, agentpkg.StatusRunning)); err != nil {
		t.Fatal(err)
	}
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})
	if got, ok := m.LightStatus("SESS-1"); !ok || got != string(agentpkg.StatusRunning) {
		t.Fatalf("got %q ok %v, want the newer live frame's running", got, ok)
	}
	withLivePids(t, map[int]string{100: "t100"}) // the newer one's process is gone: the older live one answers
	if got, ok := m.LightStatus("sess-1"); !ok || got != string(agentpkg.StatusIdle) {
		t.Fatalf("got %q ok %v, want idle from the remaining live frame", got, ok)
	}
	withLivePids(t, map[int]string{})
	if got, ok := m.LightStatus("sess-1"); ok || got != "" {
		t.Fatalf("no live frame: got %q ok %v", got, ok)
	}
}

func TestLightStatus_OtherSessionsOtherAgentsAndEmptyAreNobody(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t)
	seedIdentityFrame(t, m, "%5", "codex", 100, "t100", 10, "sess-1", "/w")
	seedIdentityFrame(t, m, "%6", "cc", 101, "t101", 10, "sess-2", "/w")
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})
	for _, id := range []string{"sess-1", "sess-3", ""} {
		if got, ok := m.LightStatus(id); ok {
			t.Fatalf("%q: got %q, want nobody", id, got)
		}
	}
	var nilMod *Module
	if _, ok := nilMod.LightStatus("sess-1"); ok {
		t.Fatal("a nil module answers nobody")
	}
}

func withStatus(f store.Frame, s agentpkg.Status) store.Frame { f.Status = s; return f }
