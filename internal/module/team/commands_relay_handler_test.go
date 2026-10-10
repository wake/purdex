package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func relayWire(id, mk, opID string) team.TeamCommand {
	c := relayCmdFor(id, mk, opID)
	c.ToHostID, c.CreatedAt = "h:1", 0 // no created_at: the age check is the store test's
	return c
}

// relayHTTPFixture is a fixture with a remote member of the paired lead host, a mod that said hello at protocol 4, and a
// fake notice seam.
func relayHTTPFixture(t *testing.T) (*fixture, *fakeNoticeDeliverer) {
	t.Helper()
	f := newFixture(t)
	f.setLeadHost(true)
	d := &fakeNoticeDeliverer{}
	f.m.teamNotices = d
	if err := f.m.store.InsertRemoteMember(newRemote("mk-1", "sid-1", "lead:1", f.clock.Load())); err != nil {
		t.Fatal(err)
	}
	f.m.mu.Lock()
	f.m.modSeen["sid-1"] = helloInfo{ModVersion: "4", Agent: "claude", At: f.clock.Load()}
	f.m.mu.Unlock()
	return f, d
}

// The route applies the kind, opens the op, and tells the member's mod to claim it through the notice seam: M's control
// message, the lead as sender, the member's session as target. Mutations: kind not in the route's switch → 400 (red); no control
// → red; control to another session → red.
func TestCommands_RelayOverHTTPSendsTheControlThroughTheSeam(t *testing.T) {
	f, d := relayHTTPFixture(t)
	code, body := f.postCmd(leadPrincipal(), relayWire(cmdUUID1, "mk-1", relayOpID))
	var ans team.TeamCommandAnswer
	if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s (%v)", code, body, err)
	}
	var out team.RelayCommandOutcome
	if err := json.Unmarshal(ans.Outcome, &out); err != nil || out.State != team.RelayCommandAccepted {
		t.Fatalf("outcome = %s", ans.Outcome)
	}
	waitFor(t, func() bool { return len(d.calls()) == 1 })
	n := d.calls()[0]
	if n.Text != team.RelayControlPrefix+relayOpID || n.Target.AgentSessionID != "sid-1" || n.LeadHostID != "lead:1" || n.Lead.SessionID != "lead-sid" {
		t.Fatalf("control = %+v", n)
	}
	if op, ok := opState(t, f.m.store, relayOpID); !ok || op.State != team.RelayRequested {
		t.Fatalf("op = %+v ok=%v", op, ok)
	}
}

// The mod's hello is what the version check reads, per session: a member whose mod never said hello (or an old one) is
// relay_unsupported and nothing is sent or opened. Mutation: skip the check → red.
func TestCommands_RelayWithoutAModHelloIsUnsupported(t *testing.T) {
	f, d := relayHTTPFixture(t)
	f.m.mu.Lock()
	f.m.modSeen["sid-1"] = helloInfo{ModVersion: "1", At: f.clock.Load()}
	f.m.mu.Unlock()
	code, body := f.postCmd(leadPrincipal(), relayWire(cmdUUID1, "mk-1", relayOpID))
	if code != http.StatusConflict || errCode(t, body) != team.ErrRelayUnsupported {
		t.Fatalf("%d %s, want 409 %s", code, body, team.ErrRelayUnsupported)
	}
	if _, ok := opState(t, f.m.store, relayOpID); ok || len(d.calls()) != 0 {
		t.Fatal("an unsupported relay opened an op or sent a control")
	}
}

// A control is sent only for a fresh application of the command or its replay while the op is still requested; a replay
// after the claim sends nothing (the op is no longer requested). Mutation: send on every replay → red.
func TestCommands_RelayReplayAfterTheClaimSendsNoSecondControl(t *testing.T) {
	f, d := relayHTTPFixture(t)
	cmd := relayWire(cmdUUID1, "mk-1", relayOpID)
	f.postCmd(leadPrincipal(), cmd)
	waitFor(t, func() bool { return len(d.calls()) == 1 })
	mustReport(t, f.m.store, relayOpID, RelayReport{State: team.RelayClaimed, At: f.clock.Load() + 1})
	if code, _ := f.postCmd(leadPrincipal(), cmd); code != http.StatusOK {
		t.Fatalf("replay = %d", code)
	}
	f.m.sweepWG.Wait()
	if n := len(d.calls()); n != 1 {
		t.Fatalf("%d controls, want the one", n)
	}
}

// A relay needs the id of an op (a UUID v4) and an mk; a void names a relay too.
func TestCommands_RelayValidation(t *testing.T) {
	f, _ := relayHTTPFixture(t)
	for _, c := range []team.TeamCommand{relayWire(cmdUUID1, "", relayOpID), relayWire(cmdUUID1, "mk-1", ""), relayWire(cmdUUID1, "mk-1", cmdBadUID)} {
		if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusBadRequest || errCode(t, body) != team.ErrCommandBadRequest {
			t.Fatalf("%+v: %d %s, want 400 bad_request", c, code, body)
		}
	}
}
