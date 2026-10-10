package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// codex attack: a relay with no created_at must not skip the age refusal (it is a new kind: no older lead to be compatible with).
// Mutation gate: treat zero as "absent" for relay → the command applies (red).
func TestRelayCmd_WithoutCreatedAtIsExpired(t *testing.T) {
	s := relayVoidSetup(t)
	cmd := relayCmdFor(cmdUUID1, "mk-1", relayOpID)
	cmd.CreatedAt = 0
	res := mustApply(t, s, relayPlanFor(cmd))
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandExpired {
		t.Fatalf("relay without created_at = %d %s, want 409 %s", res.Status, res.Body, team.ErrCommandExpired)
	}
	if _, ok := opState(t, s, relayOpID); ok {
		t.Fatal("a relay without created_at opened its op")
	}
}

// codex attack: an op id already used (another command, or an unrelated ended op) is a stored refusal, not a 500 on every retry.
// Mutation gate: no id check → ApplyTeamCommand errors (red).
func TestRelayCmd_AReusedOpIDIsARefusal(t *testing.T) {
	s := relayVoidSetup(t)
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	mustReport(t, s, relayOpID, RelayReport{State: team.RelayCancelled, Reason: "x", At: 2000})
	again := relayPlanFor(relayCmdFor(cmdUUID2, "mk-1", relayOpID))
	res := mustApply(t, s, again)
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandIDConflict {
		t.Fatalf("reused op id = %d %s, want 409 %s", res.Status, res.Body, team.ErrCommandIDConflict)
	}
	if replay := mustApply(t, s, again); !replay.Replayed || replay.Status != res.Status {
		t.Fatalf("retry = %+v, want the stored refusal", replay)
	}
}

// codex R1+attack: a void belongs to a team like every command; another team's void cancels nothing.
// Mutation gate: no team comparison → undone (red).
func TestRelayVoid_OfAnotherTeamTouchesNothing(t *testing.T) {
	s := relayVoidSetup(t)
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	v := voidCmd(cmdUUID2, cmdUUID1)
	v.TeamID = "team-other"
	res := mustApply(t, s, plan(v, false, nil))
	if res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandNotYourMember {
		t.Fatalf("void of another team = %d %s, want 409 %s", res.Status, res.Body, team.ErrCommandNotYourMember)
	}
	if op, _ := opState(t, s, relayOpID); op.State != team.RelayRequested {
		t.Fatalf("op = %s, want untouched requested", op.State)
	}
}

// codex R1: an op a void cancelled wakes the long-poll that waits on it, after the commit. Mutation gate: no notify → red.
func TestRelayVoid_UndoneWakesTheOpsWaiters(t *testing.T) {
	s := relayVoidSetup(t)
	var woken []string
	s.opChanged = func(id string) { woken = append(woken, id) }
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	woken = nil
	mustApply(t, s, plan(voidCmd(cmdUUID2, cmdUUID1), false, nil))
	if len(woken) != 1 || woken[0] != relayOpID {
		t.Fatalf("woken = %v, want [%s]", woken, relayOpID)
	}
}

// codex R1: a queued relay_failed starts the facts pump at once, like moved. Mutation gate: no pump → red.
func TestRelayFailedFact_StartsTheFactsPump(t *testing.T) {
	s := relayVoidSetup(t)
	pumped := 0
	s.onFacts = func() { pumped++ }
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	mustReport(t, s, relayOpID, RelayReport{State: team.RelayFailed, Reason: "x", At: 2000})
	if pumped != 1 {
		t.Fatalf("pumped %d times, want 1", pumped)
	}
	own := team.RelayOp{ID: "op-own", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_rmk-1", State: team.RelayClaimed, CreatedAt: 1000, UpdatedAt: 1000}
	if err := s.CreateRelayOp(own); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, "op-own", RelayReport{State: team.RelayFailed, Reason: "x", At: 3000})
	if pumped != 1 {
		t.Fatalf("a person's own failed relay pumped the facts (%d)", pumped)
	}
}
