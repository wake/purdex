package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

func withStatus(f store.Frame, s agentpkg.Status) store.Frame { f.Status = s; return f }

// The light follows the frame it is asked about: a newer frame of the same session never stands in for it.
func TestLightStatus_FollowsOnlyTheNamedFrame(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t) // no tmux session is added: the light must not need one
	a := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-1", "/w")
	b := seedIdentityFrame(t, m, "%6", "cc", 101, "t101", 20, "sess-1", "/w") // newer, not the confirmed one
	if _, err := m.frames.Upsert(withStatus(a, agentpkg.StatusIdle)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.frames.Upsert(withStatus(b, agentpkg.StatusRunning)); err != nil {
		t.Fatal(err)
	}
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})
	if got, ok := m.LightStatus("SESS-1", a.FrameID); !ok || got != string(agentpkg.StatusIdle) {
		t.Fatalf("frame a: got %q ok %v, want idle", got, ok)
	}
	if got, ok := m.LightStatus("sess-1", b.FrameID); !ok || got != string(agentpkg.StatusRunning) {
		t.Fatalf("frame b: got %q ok %v, want running", got, ok)
	}
	withLivePids(t, map[int]string{101: "t101"}) // a's process is gone
	if got, ok := m.LightStatus("sess-1", a.FrameID); ok || got != "" {
		t.Fatalf("dead frame: got %q ok %v", got, ok)
	}
}

func TestLightStatus_OtherSessionsOtherAgentsAndEmptyAreNobody(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t)
	codex := seedIdentityFrame(t, m, "%5", "codex", 100, "t100", 10, "sess-1", "/w")
	other := seedIdentityFrame(t, m, "%6", "cc", 101, "t101", 10, "sess-2", "/w")
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})
	for _, c := range []struct{ sid, frame string }{
		{"sess-1", codex.FrameID}, // not a Claude Code frame
		{"sess-1", other.FrameID}, // a frame of another session
		{"sess-3", other.FrameID},
		{"", other.FrameID},
		{"sess-2", ""},
		{"sess-2", "no-such-frame"},
	} {
		if got, ok := m.LightStatus(c.sid, c.frame); ok {
			t.Fatalf("%+v: got %q, want nobody", c, got)
		}
	}
	var nilMod *Module
	if _, ok := nilMod.LightStatus("sess-1", "f"); ok {
		t.Fatal("a nil module answers nobody")
	}
}
