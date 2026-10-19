package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// codex R2 on #3556: the lead host must not record the name a remote target had when the request was made — it may have been renamed
// since. The member host says which persona code the session has when it applies the adopt (member_code), and the lead host records
// that; an older member host sends none, and the request's name stands. Mutation gate: ignore member_code → red; never send it → red.
func TestNamePairsAdopt_ARenamedRemoteTargetIsRecordedUnderItsCurrentCode(t *testing.T) {
	setup := func(t *testing.T) (*fixture, *fakeLeadNames) {
		f, _ := adoptFixture(t)
		n := leadHostNames(f)
		ipeers.SetPool(map[string]string{"anna": "Anna", "mia": "Mia", "hiro": "Hiro"}, []string{"anna", "mia", "hiro"})
		t.Cleanup(ipeers.ResetPool)
		rec := remoteSession(rtSession, "_rt1234")
		rec.Name = "purdex-88-mia" // what the request carries
		f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return []ipeers.PeerRecord{rec}, nil }
		f.adoptOK(uid(30), remoteTarget)
		f.approveClick(uid(30))
		return f, n
	}
	settle := func(t *testing.T, f *fixture, out team.AdoptOutcome) {
		t.Helper()
		if _, err := f.m.store.SettleCommand(uid(30), answerOf(uid(30), out), f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("the member host says the session is hiro now", func(t *testing.T) {
		f, n := setup(t)
		out := applied(rtSession, "_rt1234")
		out.MemberCode = strp("hiro")
		settle(t, f, out)
		if n.count("pair:anna:hiro") != 1 || n.count("pair:") != 1 {
			t.Fatalf("calls = %v, want the pair under hiro, the code it has now", n.log())
		}
	})
	t.Run("the member host says it has no persona code now: no pair, not the old name", func(t *testing.T) {
		f, n := setup(t)
		out := applied(rtSession, "_rt1234")
		out.MemberCode = strp("") // renamed to something that is no persona's, after the request
		settle(t, f, out)
		if n.count("pair:") != 0 {
			t.Fatalf("calls = %v, want none: a newer member host said the session has no persona code", n.log())
		}
	})
	t.Run("an older member host sends no code: the request's name stands", func(t *testing.T) {
		f, n := setup(t)
		settle(t, f, applied(rtSession, "_rt1234"))
		if n.count("pair:anna:mia") != 1 || n.count("pair:") != 1 {
			t.Fatalf("calls = %v", n.log())
		}
	})
}

// The member host reports the persona code its session has when the adopt is applied.
func TestCommands_AdoptOutcomeCarriesTheTargetsCurrentCode(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.m.names = &fakeLeadNames{codes: map[string]string{"sid-t": "hiro"}}
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w", Title: "m", Name: "scratch"})
	code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t"))
	var ans team.TeamCommandAnswer
	var out team.AdoptOutcome
	if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK || json.Unmarshal(ans.Outcome, &out) != nil {
		t.Fatalf("%d %s (%v)", code, body, err)
	}
	if out.MemberCode == nil || *out.MemberCode != "hiro" {
		t.Fatalf("outcome = %s, want member_code hiro", ans.Outcome)
	}
}

// A target with no persona code is reported as an explicit empty code, not as an absent field (which an older host would also be).
func TestCommands_AdoptOutcomeSaysWhenTheTargetHasNoCode(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.m.names = &fakeLeadNames{codes: map[string]string{}}
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w", Title: "m", Name: "scratch"})
	code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t"))
	var ans team.TeamCommandAnswer
	if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s (%v)", code, body, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(ans.Outcome, &raw); err != nil {
		t.Fatal(err)
	}
	if v, has := raw["member_code"]; !has || v != "" {
		t.Fatalf("outcome = %s, want member_code present and empty", ans.Outcome)
	}
}

func strp(s string) *string { return &s }
