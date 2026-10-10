package teammod

import (
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// L: a mirror open ask + the lead's relay of that remote member → the ask is accepted by the forwarded op, in the op's
// transaction (MR-3a-1's generic UPDATE reaches a mirror: its session_id is the op's). Mutation (on the MR-3a-1 side): skip the
// UPDATE for a remote op → the ask stays open (red).
func TestRelayAskFact_ALeadsRelayAcceptsTheMirror(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	fact := relayAskFact(factUUID1, "mk1", rid(900), 71, 300)
	if code, body := f.postFact(memberPrincipal(), fact); code != http.StatusOK {
		t.Fatalf("fact: %d %s", code, body)
	}
	if a := f.ask(rid(900)); a.State != team.RelayAskOpen {
		t.Fatalf("mirror = %+v", a)
	}
	op := f.relayForwarded(fwdOp)
	if a := f.ask(rid(900)); a.State != "accepted" || a.OpID != op.ID {
		t.Fatalf("mirror after the lead's relay = %+v, want accepted by %s", a, op.ID)
	}
}

// codex R1+attack: the capability that was read is the lead host the transaction writes to. A member re-adopted to another
// lead between the two is not written for (the caller reads again). Mutation: no comparison → the ask and the fact are written
// for the unchecked host (red).
func TestRemoteAsk_TheCheckedLeadHostIsTheOneWrittenFor(t *testing.T) {
	f, _ := remoteAskFixture(t)
	_, _, err := f.m.store.CreateRemoteRelayAsk(RelayAsk{ID: rid(710), SessionID: "sid-1", UsedPct: 71, Window: 1000, CreatedAt: 1000, ExpiresAt: 301_000}, "host-other")
	if !errors.Is(err, ErrAskLeadChanged) {
		t.Fatalf("err = %v, want ErrAskLeadChanged", err)
	}
	if n := askRows(t, f.m.store); n != 0 {
		t.Fatalf("%d ask rows written for an unchecked lead host", n)
	}
	if got := askFactsOf(t, f.m.store); len(got) != 0 {
		t.Fatalf("facts written: %+v", got)
	}
}

// codex R1+attack: a retry of an ask that was committed (the answer was lost) gets the stored ask, even when the lead host's
// capabilities are unreadable or downgraded now: the capability gate is for a NEW ask. Mutation: gate before the replay lookup →
// relay_unsupported (red).
func TestRemoteAsk_ARetryOfACommittedAskIsAnsweredFromTheStore(t *testing.T) {
	f, fc := remoteAskFixture(t)
	f.postAsk(rid(711), "sid-1", 71)
	fc.capsErr = http.ErrHandlerTimeout
	for _, id := range []string{rid(711), rid(712)} { // the same request, then another one while the ask is open
		code, resp, ae := f.postAsk(id, "sid-1", 72)
		if code != http.StatusOK || resp.ID != rid(711) || !resp.Replay {
			t.Fatalf("retry %s: %d %+v %+v, want the stored ask", id, code, resp, ae)
		}
	}
	if got := askFactsOf(t, f.m.store); len(got) != 1 {
		t.Fatalf("%d facts, want 1", len(got))
	}
	fc.caps["host-L"] = ipeers.TeamCaps{}
	fc.capsErr = nil
	if code, _, ae := f.postAsk(rid(713), "sid-other", 72); code == http.StatusOK {
		t.Fatalf("an unknown session was answered: %+v", ae)
	}
}

// codex attack: M closed the first ask and made a new one while L's old mirror of the session is still open: the new ask
// replaces it (the old one is withdrawn) instead of being swallowed by the one-open index and remembered as "ignored" for ever.
// Mutation: the unqualified ON CONFLICT DO NOTHING → no new mirror, no notice (red).
func TestRelayAskFact_ANewAskReplacesTheSessionsOldMirror(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(910), 71, 300))
	code, body := f.postFact(leadPrincipal(), relayAskFact(askFactUUID, "mk1", rid(911), 73, 300))
	if code != http.StatusOK {
		t.Fatalf("second ask: %d %s", code, body)
	}
	if old := f.ask(rid(910)); old.State != "withdrawn" {
		t.Fatalf("old mirror = %+v, want withdrawn", old)
	}
	if cur := f.ask(rid(911)); cur.State != team.RelayAskOpen || cur.UsedPct != 73 {
		t.Fatalf("new mirror = %+v, want open", cur)
	}
}

// codex attack: an ask id already used by another row (a local ask of this host, or another member's) is a refusal that is
// stored, not a silent "ignored": the row keeps what it was. Mutation: ON CONFLICT DO NOTHING for any row → ignored (red).
func TestRelayAskFact_AnAskIDOfAnotherRowIsAConflict(t *testing.T) {
	f, _ := leadAskFixture(t)
	if _, err := f.m.store.db.Exec(`INSERT INTO relay_asks (id, team_id, spawn_op, session_id, used_pct, window, state, created_at, expires_at)
		VALUES (?, ?, 'op-other', 'sid-other', 50, 1000, 'open', 1, 9999999999999)`, rid(912), uid(1)); err != nil {
		t.Fatal(err)
	}
	code, body := f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(912), 71, 300))
	if code != http.StatusConflict || errCode(t, body) != team.ErrCommandIDConflict {
		t.Fatalf("fact: %d %s, want 409 %s", code, body, team.ErrCommandIDConflict)
	}
	if a := f.ask(rid(912)); a.SessionID != "sid-other" || a.UsedPct != 50 {
		t.Fatalf("the other row changed: %+v", a)
	}
}

// codex critic: the refusal has no side effect — the session's existing open mirror is still open after an id_conflict.
// Mutation gate: withdraw the old mirror before looking at who owns the id → the old mirror is withdrawn (red).
func TestRelayAskFact_AnIDConflictLeavesTheSessionsMirrorOpen(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(920), 71, 300))
	if _, err := f.m.store.db.Exec(`INSERT INTO relay_asks (id, team_id, spawn_op, session_id, used_pct, window, state, created_at, expires_at)
		VALUES (?, ?, 'op-other', 'sid-other', 50, 1000, 'open', 1, 9999999999999)`, rid(921), uid(1)); err != nil {
		t.Fatal(err)
	}
	code, body := f.postFact(leadPrincipal(), relayAskFact(askFactUUID, "mk1", rid(921), 73, 300))
	if code != http.StatusConflict || errCode(t, body) != team.ErrCommandIDConflict {
		t.Fatalf("fact: %d %s, want 409 %s", code, body, team.ErrCommandIDConflict)
	}
	if a := f.ask(rid(920)); a.State != team.RelayAskOpen {
		t.Fatalf("the session's mirror = %+v, want still open", a)
	}
	if a := f.ask(rid(921)); a.SessionID != "sid-other" || a.UsedPct != 50 {
		t.Fatalf("the other row changed: %+v", a)
	}
}

// codex attack: the member-left withdrawal ties the remote row to the ask by team too: a row of another team with the same mk and
// session does not keep it open. Mutation: drop the team comparison → the ask stays open (red).
func TestRemoteAsk_TheSweeperChecksTheTeamOfTheRemoteRow(t *testing.T) {
	f, _ := remoteAskFixture(t)
	f.postAsk(rid(714), "sid-1", 71)
	if _, err := f.m.store.db.Exec(`UPDATE relay_asks SET team_id = 'another-team' WHERE id = ?`, rid(714)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(f.clock.Load()); err != nil || n != 1 {
		t.Fatalf("withdrawn %d (%v), want 1: the remote row is of team-L, the ask of another team", n, err)
	}
}
