package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// D3 (codex attack): the team is the OLD session's member row, read before the move. A solo session whose new session
// already is somebody's member must not be stamped with that team. Mutation gate: read the stamp from the new session → red.
func TestRelayStore_ClearedDoesNotStampASoloOpFromTheTargetsTeam(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "L1", 1000)
	seedMember(t, s, "sp-1", "team-1", "M-other", 1000)

	claimedOp(t, s, "op-s", "SOLO", "_solo11")
	mustReport(t, s, "op-s", RelayReport{State: team.RelayCleared, NewSessionID: "M-other", NewRef: "_nnn222", At: 6000})
	if op, _, _ := s.GetRelayOp("op-s"); op.TeamID != "" {
		t.Fatalf("a solo self op team_id = %q, want empty (the target's team is not the source's)", op.TeamID)
	}
}

// codex R1: a member whose kill is in flight is not relayed by hand (moveTeamRoles moves active rows only, so the
// membership would stay behind). Mutation gate: allow killing → a card opens (red).
func TestManualBegin_AKillingMemberIsRefused(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if err := f.m.store.SetMemberState("op-1", team.MemberKilling, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	code, body := f.beginManual("sid-1")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrRelayUnsupported {
		t.Fatalf("killing member manual begin: %d %s, want 409 %s", code, body, team.ErrRelayUnsupported)
	}
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("approvals opened: %+v", open)
	}
}

// codex critic: a member can enter killing AFTER the card opened (ClaimMemberKilling does not wait for an awaiting
// approval); the approve and the reconcile must then cancel, or the seat is stranded on the old session.
// Mutation gates: drop the killing check at the approve → approved (red); at closedRowReport → claimed (red).
func TestManualApprove_ARowOfAMemberThatStartedBeingKilledIsCancelled(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	out := f.manualOpened("sid-1")
	if err := f.m.store.SetMemberState("op-1", team.MemberKilling, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	code, body := f.decide(out.RequestID, "approve")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("approve: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled {
		t.Fatalf("op = %s, want cancelled", op.State)
	}
}

func TestManualReconcile_AnApprovedRowOfAKillingMemberIsCancelled(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	out := f.manualOpened("sid-1")
	if _, won, err := f.m.store.CloseIfOpen(out.RequestID, Close{State: team.StateApproved, DecidedAt: 1}); err != nil || !won {
		t.Fatalf("approve in the store: won=%v err=%v", won, err)
	}
	if err := f.m.store.SetMemberState("op-1", team.MemberKilling, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.m.reconcileRelays()
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled {
		t.Fatalf("op = %s (%s), want cancelled", op.State, op.Reason)
	}
}
