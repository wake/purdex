// internal/module/team/facts_sweep_test.go
package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/config"
)

func (f *fixture) pairHosts(hostIDs ...string) {
	f.t.Helper()
	f.core.CfgMu.Lock()
	defer f.core.CfgMu.Unlock()
	f.core.Cfg.Peers.Hosts = nil
	for i, id := range hostIDs {
		f.core.Cfg.Peers.Hosts = append(f.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "h" + string(rune('a'+i)), URL: "https://x.example", HostID: id, InboundToken: "i"})
	}
}

// §5.2: the sweeper marks a remote member whose session is gone, and queues the ended fact with it.
func TestSweep_RemoteMemberWhoseSessionIsGone(t *testing.T) {
	f := newFixture(t)
	f.m.bootAt = 0
	seedRemote(t, f.m.store, "mk-1", "sid-gone", f.clock.Load())
	seedRemote(t, f.m.store, "mk-2", "sid-live", f.clock.Load())
	seedRemote(t, f.m.store, "mk-3", "sid-unknown", f.clock.Load())
	f.origins.markDead("sid-gone")
	f.origins.mu.Lock()
	if f.origins.unknown == nil {
		f.origins.unknown = map[string]bool{}
	}
	f.origins.unknown["sid-unknown"] = true
	f.origins.mu.Unlock()

	f.m.markGoneRemoteMembers()

	for mk, want := range map[string]string{"mk-1": remoteGone, "mk-2": remoteActive, "mk-3": remoteActive} {
		if row, _, _ := f.m.store.RemoteMember(mk); row.State != want {
			t.Fatalf("%s = %s, want %s", mk, row.State, want)
		}
	}
	facts := factsOf(t, f.m.store, "host-L")
	if len(facts) != 1 || facts[0].MK != "mk-1" {
		t.Fatalf("facts = %+v, want one ended fact for mk-1", facts)
	}
	// A second tick finds nothing new.
	f.m.markGoneRemoteMembers()
	if len(factsOf(t, f.m.store, "host-L")) != 1 {
		t.Fatal("a second sweep queued another fact")
	}
}

func TestSweep_RemoteMemberWaitsForTheBootGrace(t *testing.T) {
	f := newFixture(t)
	f.m.bootAt = f.clock.Load() // just started
	seedRemote(t, f.m.store, "mk-1", "sid-gone", f.clock.Load())
	f.origins.markDead("sid-gone")
	f.m.markGoneRemoteMembers()
	if row, _, _ := f.m.store.RemoteMember("mk-1"); row.State != remoteActive {
		t.Fatalf("marked gone inside the boot grace: %+v", row)
	}
}

// §3.2 / §11 Unpairing: a lead host that is no longer in the config ends its live members here, silently.
func TestSweep_UnpairedLeadHostEndsItsRemoteMembers(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-1", f.clock.Load()) // lead host-L
	f.pairHosts("host-L")
	f.m.endUnpairedRemoteMembers()
	if row, _, _ := f.m.store.RemoteMember("mk-1"); row.State != remoteActive {
		t.Fatalf("a paired host's member ended: %+v", row)
	}

	f.pairHosts("somebody-else") // host-L's entry was deleted
	f.m.endUnpairedRemoteMembers()
	if row, _, _ := f.m.store.RemoteMember("mk-1"); row.State != remoteEnded {
		t.Fatalf("the unpaired host's member is %s, want ended", row.State)
	}
	if role, _ := f.m.store.SessionRole("sid-1"); role != sessionRoleNone {
		t.Fatalf("role = %s: self relay must be on again", role)
	}
	if len(noticesOf(t, f.m.store, "mk-1")) != 0 || len(factsOf(t, f.m.store, "host-L")) != 0 {
		t.Fatal("an unpaired end sent a notice or a fact")
	}
}
