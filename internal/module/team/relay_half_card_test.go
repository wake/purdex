package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// ---- the idle card (300 s) and the half written for it: which close wins ----

// openIdleCardWithHalfOp opens an idle card (the switch off) and turns its op into a claimed half op, as a half the mod is writing
// for that card; it returns the op and the card's request id.
func (f *fixture) openIdleCardWithHalfOp(sid string) (team.RelayOp, string) {
	f.t.Helper()
	_, resp, body := f.beginRaw2(halfBeginReq(sid, false))
	if f.row(resp.RequestID).State != team.StateOpen {
		f.t.Fatalf("no open card: %s", body)
	}
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET mode = ?, state = ?, handoff_path = ? WHERE id = ?`,
		team.RelayModeHalf, string(team.RelayClaimed), "/p/half.md", resp.Op.ID); err != nil {
		f.t.Fatal(err)
	}
	return resp.Op, resp.RequestID
}

// A half written while its card is still open closes the card cancelled, and the card says why: close_reason "half".
// Mutation gates: the reason left out → red; the card left open → red.
func TestHalfWritten_ClosesItsOpenCardWithTheHalfReason(t *testing.T) {
	f := newFixture(t)
	op, card := f.openIdleCardWithHalfOp("sid-1")
	if code, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting}); code != http.StatusOK {
		t.Fatalf("writing: %d %+v", code, ae)
	}
	if code, got, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); code != http.StatusOK || got.Reason != team.RelayReasonHalf {
		t.Fatalf("written: %d %+v %+v", code, got, ae)
	}
	if row := f.row(card); row.State != team.StateCancelled || row.CloseReason != team.RelayReasonHalf || f.closeReasonOf(card) != "half" {
		t.Fatalf("card = %s / %q, want cancelled / half", row.State, row.CloseReason)
	}
}

// A report that ends an op with another reason closes the card with none, as before.
func TestOpEndedOtherwise_ClosesItsCardWithoutAReason(t *testing.T) {
	f := newFixture(t)
	_, resp, _ := f.beginRaw2(halfBeginReq("sid-1", false))
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = ? WHERE id = ?`, string(team.RelayClaimed), resp.Op.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, ae := f.report(resp.Op.ID, team.RelayReportRequest{State: team.RelayFailed, Error: "boom"}); code != http.StatusOK {
		t.Fatalf("failed: %d %+v", code, ae)
	}
	if row := f.row(resp.RequestID); row.State != team.StateCancelled || f.closeReasonOf(resp.RequestID) != "" {
		t.Fatalf("card = %s / %q, want cancelled with no reason", row.State, f.closeReasonOf(resp.RequestID))
	}
}

// The deadline of an idle card is 300 s, and a card never outlives into a half: a half op is born approved (beginHalf closes its row at
// begin, no card), so no half is ever being written under an OPEN card, and the sweeper cannot time out a half's card under it. What
// follows the timeout is a NEW op: the card's op ends cancelled{timeout}, the mod begins a half (a new op, a new approved row), writes
// it, and the session is on the fresh list. This pins that order end to end, with the sweeper first:
// card open → past 300 s → sweeper: card timeout, op cancelled{timeout} → half begins, writing, written → op cancelled{half}, and
// GET /api/relay/fresh lists the session, so the App learns of the file.
// Mutation gates: the fresh row not written for a half that follows a timed-out card → red; the timed-out op left claimed/open
// (a second op refused with relay_op_open) → red.
func TestIdleCardDeadline_TimeoutThenAHalfReachesTheFreshList(t *testing.T) {
	f := newFixture(t)
	_, card, _ := f.beginRaw2(halfBeginReq("sid-1", false))
	if f.row(card.RequestID).State != team.StateOpen {
		t.Fatal("no open idle card")
	}
	f.advance(team.SelfRelayIdleDeadlineS*1000 + 1)
	f.m.tick()
	if row := f.row(card.RequestID); row.State != team.StateTimeout {
		t.Fatalf("card = %s after the sweep, want timeout", row.State)
	}
	cardOp, _, _ := f.m.store.GetRelayOp(card.Op.ID)
	if cardOp.State != team.RelayCancelled || cardOp.Reason != team.RelayReasonTimeout {
		t.Fatalf("the card's op = %s / %q, want cancelled / timeout", cardOp.State, cardOp.Reason)
	}
	if v := f.getFresh(); len(v.Sessions) != 0 {
		t.Fatalf("fresh before the half: %+v", v.Sessions)
	}

	half := f.halfOp("sid-1") // begin --half (a new op, born approved), then writing
	if half.ID == card.Op.ID {
		t.Fatal("the half reused the timed-out card's op")
	}
	if code, got, ae := f.report(half.ID, team.RelayReportRequest{State: team.RelayWritten}); code != http.StatusOK || got.State != team.RelayCancelled || got.Reason != team.RelayReasonHalf {
		t.Fatalf("written: %d %+v %+v", code, got, ae)
	}
	v := f.getFresh()
	if len(v.Sessions) != 1 || v.Sessions[0].SessionID != "sid-1" || v.Sessions[0].OpID != half.ID {
		t.Fatalf("fresh = %+v, want sid-1 with the half's op %s", v.Sessions, half.ID)
	}
	if row := f.row(card.RequestID); row.State != team.StateTimeout {
		t.Fatalf("the timed-out card changed to %s", row.State)
	}
}
