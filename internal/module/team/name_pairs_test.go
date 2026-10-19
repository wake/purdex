package teammod

import (
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The lead/member pairs of the name history (name pool v2 spec §6.1): one when a spawned member's op is registered, one when an adopt
// is approved; every failure is only logged.

// pairNames is a name service for the spawn fixture: sid-1 (the lead) is "anna", and the member's reserved name is "mia".
func pairNames(f *fixture) *fakeLeadNames {
	n := &fakeLeadNames{codes: map[string]string{"sid-1": "anna"}, reserve: "mia"}
	f.m.names = n
	return n
}

func TestNamePairs_ARegisteredLocalSpawnRecordsLeadAndOpName(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := pairNames(f)
	f.register("%0", "sid-m1")
	if op := f.runOp(1, root, namedOp); op.State != team.SpawnDone {
		t.Fatalf("op = %+v, want done", op)
	}
	if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
}

// A spawn that does not register writes no pair (mutation: record at accept → the failed spawn counts).
func TestNamePairs_ASpawnThatFailsRecordsNothing(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := pairNames(f)
	if op := f.runOp(1, root, namedOp); op.Reason != team.SpawnReasonStartTimeout {
		t.Fatalf("op = %+v, want a start timeout", op)
	}
	if n.count("pair:") != 0 {
		t.Fatalf("calls = %v", n.log())
	}
}

func TestNamePairs_NothingWithoutAnOpNameOrALeadCode(t *testing.T) {
	t.Run("op without a name", func(t *testing.T) {
		f, root := newSpawnFixture(t, 1)
		n := pairNames(f)
		f.register("%0", "sid-m1")
		if op := f.runOp(1, root, nil); op.State != team.SpawnDone {
			t.Fatalf("op = %+v", op)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v", n.log())
		}
	})
	t.Run("lead without a persona code", func(t *testing.T) {
		f, root := newSpawnFixture(t, 1)
		n := pairNames(f)
		n.codes = nil
		f.register("%0", "sid-m1")
		if op := f.runOp(1, root, namedOp); op.State != team.SpawnDone {
			t.Fatalf("op = %+v", op)
		}
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v", n.log())
		}
	})
}

func TestNamePairs_AFailedWriteIsOnlyLogged(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := pairNames(f)
	n.pairErr = errors.New("meta.db locked")
	f.register("%0", "sid-m1")
	if op := f.runOp(1, root, namedOp); op.State != team.SpawnDone {
		t.Fatalf("op = %+v, want done", op)
	}
	if n.count("pair:anna:mia") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
}

// A forwarded op records on the member host, from the lead's address and the name this host reserved for it. (The command does not
// carry a name yet: the op's pool_name is set here by hand.)
func TestNamePairs_ARegisteredRemoteSpawnRecordsTheLeadsCodeFromItsAddress(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	n := pairNames(f)
	f.holdRunners()
	cmd := spawnCommand(cmdUUID1, root)
	cmd.Lead.Address = "lead/purdex-88-anna"
	if code, body := f.postCmd(leadPrincipal(), cmd); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	op, _, err := f.m.store.GetSpawnOp(cmdUUID1)
	if err != nil {
		t.Fatal(err)
	}
	op.PoolName, op.SessionID = "mia", "sid-m1"
	f.register("%0", "sid-m1")
	f.m.spawnFinishRemote(op, &team.Origin{SessionID: "sid-m1", Ref: ipeers.RefID("sid-m1"), PID: 31, ProcStart: "ps"})
	if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
}

func TestNamePairs_AddressCode(t *testing.T) {
	for in, want := range map[string]string{
		"lead/purdex-88-anna": "anna", "purdex-88-anna": "anna", "mlab/_abc123": "", "lead/x [lead01]": "", "": "",
	} {
		if got := addressCode(in); got != want {
			t.Errorf("addressCode(%q) = %q, want %q", in, got, want)
		}
	}
}
