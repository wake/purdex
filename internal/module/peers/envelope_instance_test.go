package peers

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
)

// #3316 D: the tmux generation an inventory starts from is the one the session list carries (read in the same tmux invocation), so the
// "before" sample costs no probe of its own; the "after" probe that detects a restart during the inventory is still made.
func TestLocalEnvelope_InstanceBeforeComesFromTheList(t *testing.T) {
	sessions := &fakeSessions{
		sessions:  []session.SessionInfo{{Code: "c1", Name: "work", TmuxID: "$1", TmuxInstance: "100:1"}},
		instances: []string{"100:1"},
	}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, &fakeOwners{}, t.TempDir(), allLiveLiveness(fixture76973ProcStart), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, time.Second)

	env := m.localEnvelope(t.Context(), "mlab:abc123", "mlab")
	if !env.OK {
		t.Fatalf("envelope not ok: %+v", env)
	}
	if n := sessions.instanceCtxCalls.Load(); n != 1 {
		t.Errorf("tmux-instance probes = %d, want 1 (only the re-check after the owner work)", n)
	}
}

// A restart that happens during the inventory is still caught: the list's generation differs from the re-check's.
func TestLocalEnvelope_RestartAfterTheListIsStillCaught(t *testing.T) {
	sessions := &fakeSessions{
		sessions:  []session.SessionInfo{{Code: "c1", Name: "work", TmuxID: "$1", TmuxInstance: "100:1"}},
		instances: []string{"200:2"}, // the probe after sees another server
	}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, &fakeOwners{}, t.TempDir(), allLiveLiveness(fixture76973ProcStart), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, time.Second)

	env := m.localEnvelope(t.Context(), "mlab:abc123", "mlab")
	if env.OK || env.Error != "tmux server restarted during inventory" {
		t.Fatalf("envelope = %+v, want the restart error", env)
	}
}

// With no session to carry a generation (or a provider that stamps none) the first sample is probed, as before.
func TestLocalEnvelope_InstanceProbedWhenTheListHasNone(t *testing.T) {
	for name, list := range map[string][]session.SessionInfo{
		"no sessions":        nil,
		"sessions unstamped": {{Code: "c1", Name: "work", TmuxID: "$1"}},
	} {
		sessions := &fakeSessions{sessions: list, instances: []string{"100:1"}}
		c := newTestCore(t, "mlab:abc123", "mlab")
		m := newTestModule(t, c, sessions, &fakeOwners{}, t.TempDir(), allLiveLiveness(fixture76973ProcStart), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, time.Second)
		var env ipeers.Envelope = m.localEnvelope(t.Context(), "mlab:abc123", "mlab")
		if !env.OK {
			t.Fatalf("%s: envelope not ok: %+v", name, env)
		}
		if n := sessions.instanceCtxCalls.Load(); n != 2 {
			t.Errorf("%s: probes = %d, want 2 (before and after)", name, n)
		}
	}
}
