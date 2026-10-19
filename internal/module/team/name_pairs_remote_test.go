package teammod

import (
	"context"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The pairs of a forwarded spawn and of an adopt are written on the lead host (name pool v2 spec §6.1, §5.4).

// leadHostNames is a name service for the lead host: sid-1 (the team's lead) is "anna", the adopt target sid-2 is "mia".
func leadHostNames(f *fixture) *fakeLeadNames {
	n := &fakeLeadNames{codes: map[string]string{"sid-1": "anna", "sid-2": "mia"}}
	f.m.names = n
	return n
}

func (f *fixture) setRemoteSpawnPoolName(id, name string) {
	f.t.Helper()
	if _, err := f.m.store.db.Exec(`UPDATE remote_spawns SET pool_name = ? WHERE id = ?`, name, id); err != nil {
		f.t.Fatal(err)
	}
}

// The member host reports the registration; the lead host writes the pair: its team's lead and the name the op carries. The same
// fact again (a retry after a lost answer) writes nothing more. Mutation gate: no call in handleTeamFact → red; record in a replay → red.
func TestNamePairsRemote_ARegisteredFactWritesOnePair(t *testing.T) {
	f := factFixture(t)
	n := leadHostNames(f)
	f.remoteSpawn("op1", "lead:1", "")
	f.setRemoteSpawnPoolName("op1", "mia")
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
		t.Fatalf("fact = %d %s", code, body)
	}
	if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
		t.Fatalf("replay = %d %s", code, body)
	}
	if n.count("pair:") != 1 {
		t.Fatalf("calls after the replay = %v, want still one pair", n.log())
	}
}

func TestNamePairsRemote_NothingForANamelessOpAFailureOrAConflict(t *testing.T) {
	t.Run("an op without a name", func(t *testing.T) {
		f := factFixture(t)
		n := leadHostNames(f)
		f.remoteSpawn("op1", "lead:1", "")
		if code, _ := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
			t.Fatalf("fact = %d", code)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v", n.log())
		}
	})
	t.Run("a spawn that failed", func(t *testing.T) {
		f := factFixture(t)
		n := leadHostNames(f)
		f.remoteSpawn("op1", "lead:1", "")
		f.setRemoteSpawnPoolName("op1", "mia")
		if code, _ := f.postFact(leadPrincipal(), spawnFailedFact(factUUID1, "op1", team.SpawnReasonCreateFailed)); code != 200 {
			t.Fatalf("fact = %d", code)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want no pair for a spawn that did not register", n.log())
		}
	})
	t.Run("a session that is already a member here", func(t *testing.T) {
		// the fact still answers applied, but the op is failed session_conflict: the member never joined
		f := factFixture(t)
		n := leadHostNames(f)
		f.remoteSpawn("op1", "lead:1", "")
		f.setRemoteSpawnPoolName("op1", "mia")
		seedMember(t, f.m.store, "op-other", uid(1), "msid-9", 1)
		if code, _ := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
			t.Fatalf("fact = %d", code)
		}
		if st, reason := f.spawnState("op1"); st != "failed" || reason != "session_conflict" {
			t.Fatalf("op = %s{%s}, want failed session_conflict", st, reason)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want no pair for an op that failed", n.log())
		}
	})
}

// An adopt, approved by a click or at create (a person's own request): one pair, the lead's code and the target's. A deny writes none.
// Mutation gate: no call at either place → red.
func TestNamePairsAdopt_ApprovalWritesOnePair(t *testing.T) {
	t.Run("a click", func(t *testing.T) {
		f := newFixture(t)
		n := leadHostNames(f)
		f.approveLead(uid(1))
		a := f.adoptOK(uid(10), "_def456")
		if n.count("pair:") != 0 {
			t.Fatalf("calls before the decision = %v", n.log())
		}
		if code, _ := f.decide(a.ID, "approve"); code != 200 {
			t.Fatalf("decide = %d", code)
		}
		if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
			t.Fatalf("calls = %v", n.log())
		}
	})
	t.Run("approved at create", func(t *testing.T) {
		f := newFixture(t)
		n := leadHostNames(f)
		f.approveLead(uid(1))
		f.personAdopt = true
		f.adoptOK(uid(10), "_def456")
		if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
			t.Fatalf("calls = %v", n.log())
		}
	})
	t.Run("a deny", func(t *testing.T) {
		f := newFixture(t)
		n := leadHostNames(f)
		f.approveLead(uid(1))
		a := f.adoptOK(uid(10), "_def456")
		if code, _ := f.decide(a.ID, "deny"); code != 200 {
			t.Fatalf("deny = %d", code)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want none for a deny", n.log())
		}
	})
}

// A remote target is known by the name the request carried; a name that is not a persona's writes nothing.
func TestNamePairsAdopt_ARemoteTargetIsKnownByItsName(t *testing.T) {
	f := newFixture(t)
	n := leadHostNames(f)
	ipeers.SetPool(map[string]string{"anna": "Anna", "mia": "Mia"}, []string{"anna", "mia"}) // personas, as the peers module binds them
	t.Cleanup(ipeers.ResetPool)
	f.m.recordAdoptPair(team.AdoptPayload{TeamID: uid(1), LeadSessionID: "sid-1", TargetHostID: "air26:1", TargetSessionID: "far-sid", TargetName: "purdex-88-mia"}, "ap-1")
	if n.count("pair:anna:mia") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
	f.m.recordAdoptPair(team.AdoptPayload{TeamID: uid(1), LeadSessionID: "sid-1", TargetHostID: "air26:1", TargetSessionID: "far-sid", TargetName: "scratch"}, "ap-2")
	if n.count("pair:") != 1 {
		t.Fatalf("calls = %v, want a name without a persona code to write nothing", n.log())
	}
}

// codex R1 on #3556: an adopt of a session on another host is not a pair when it is approved — the member host can still refuse it,
// or the session may turn out to be a member here — but when the member host answers applied and the row becomes active. A refusal
// writes none. Mutation gate: record at the approval → red; no call after the settle → red.
func TestNamePairsAdopt_ARemoteTargetIsRecordedWhenItBecomesActive(t *testing.T) {
	setup := func(t *testing.T) (*fixture, *fakeLeadNames) {
		f, _ := adoptFixture(t)
		n := leadHostNames(f)
		ipeers.SetPool(map[string]string{"anna": "Anna", "mia": "Mia"}, []string{"anna", "mia"}) // personas, as the peers module binds them
		t.Cleanup(ipeers.ResetPool)
		rec := remoteSession(rtSession, "_rt1234")
		rec.Name = "purdex-88-mia"
		f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return []ipeers.PeerRecord{rec}, nil }
		f.adoptOK(uid(30), remoteTarget)
		f.approveClick(uid(30))
		if n.count("pair:") != 0 {
			t.Fatalf("calls at the approval = %v, want none yet", n.log())
		}
		return f, n
	}
	settle := func(t *testing.T, f *fixture, res peersmod.CallResult) {
		t.Helper()
		if _, err := f.m.store.SettleCommand(uid(30), res, f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("applied", func(t *testing.T) {
		f, n := setup(t)
		settle(t, f, answerOf(uid(30), applied(rtSession, "_rt1234")))
		if st, _ := f.memberRowState(uid(30)); st != rowActive {
			t.Fatalf("row = %s, want active", st)
		}
		if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
			t.Fatalf("calls = %v", n.log())
		}
	})
	t.Run("a late applied for a row that is no longer joining", func(t *testing.T) {
		f, n := setup(t)
		if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = ?`, uid(30)); err != nil {
			t.Fatal(err)
		}
		settle(t, f, answerOf(uid(30), applied(rtSession, "_rt1234")))
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want none: the row had left joining, the late answer changed nothing", n.log())
		}
	})
	t.Run("refused", func(t *testing.T) {
		f, n := setup(t)
		settle(t, f, refusedBy("host_not_allowed"))
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want none for an adopt the member host refused", n.log())
		}
	})
}
