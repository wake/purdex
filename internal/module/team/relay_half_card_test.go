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

// The deadline of an idle card is 300 s. The two closes race at it, and whichever comes first stands (the second's compare-and-set loses):
//   - the half is written before the sweeper's tick: the card is cancelled / half and the sweeper finds nothing to time out;
//   - the sweeper ticks first: the card is timeout, and a half written after it leaves it so (a half written late is not recorded on a
//     card that already ended as timeout).
//
// Mutation gates: the sweeper closes an already closed card again → red; the half's close overwrites a timeout → red.
func TestIdleCardDeadline_HalfWrittenBeforeTheSweepWins(t *testing.T) {
	f := newFixture(t)
	op, card := f.openIdleCardWithHalfOp("sid-1")
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting})
	f.advance(team.SelfRelayIdleDeadlineS*1000 + 1) // past the deadline, the sweeper has not run yet
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
	f.m.tick()
	if row := f.row(card); row.State != team.StateCancelled || row.CloseReason != team.RelayReasonHalf {
		t.Fatalf("card = %s / %q, want cancelled / half (the half was written first)", row.State, row.CloseReason)
	}
}

func TestIdleCardDeadline_SweepBeforeTheHalfLeavesTimeout(t *testing.T) {
	f := newFixture(t)
	op, card := f.openIdleCardWithHalfOp("sid-1")
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting})
	f.advance(team.SelfRelayIdleDeadlineS*1000 + 1)
	f.m.tick()
	if row := f.row(card); row.State != team.StateTimeout {
		t.Fatalf("card = %s after the sweep, want timeout", row.State)
	}
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
	if row := f.row(card); row.State != team.StateTimeout || row.CloseReason != "" {
		t.Fatalf("card = %s / %q after the half, want it still timeout: a half written late does not rewrite how the card ended", row.State, row.CloseReason)
	}
}
