package teammod

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Accepting (spec 2026-10-10-member-relay-ask §3.2): the lead's `pdx relay _<ref>` marks the open ask accepted inside
// CreateMemberRelayOp's transaction, after the op insert and before the commit.

// openAsk stores an open ask for the fixture's member at the fixture clock.
func (f *fixture) openAsk(id string) RelayAsk {
	f.t.Helper()
	now := f.clock.Load()
	a, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: id, SessionID: "sid-m1", UsedPct: 72, Window: 1000000, CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000})
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f *fixture) ask(id string) RelayAsk {
	f.t.Helper()
	a, ok, err := f.m.store.GetRelayAsk(id)
	if err != nil || !ok {
		f.t.Fatalf("ask %s: ok=%v err=%v", id, ok, err)
	}
	return a
}

func TestRelayCreate_AcceptsTheOpenAsk(t *testing.T) {
	f := gateFixture(t, false, false, 0)
	f.openAsk(rid(500))
	code, op, _ := f.createRelay(rid(501), "/tmp/10.sock", "_mem001")
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	a := f.ask(rid(500))
	if a.State != team.RelayAskAccepted || a.OpID != op.ID || a.ClosedAt == 0 {
		t.Fatalf("ask after the relay: %+v", a)
	}
}

// A create that is refused leaves the ask open (here: the member's mod is too old for a member relay).
func TestRelayCreate_ARefusedCreateLeavesTheAskOpen(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("1") // protocol 1 < MinMemberRelayModVersion
	f.openAsk(rid(510))
	if code, _, ae := f.createRelay(rid(511), "/tmp/10.sock", "_mem001"); code != 409 || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("create: %d %+v", code, ae)
	}
	if a := f.ask(rid(510)); a.State != team.RelayAskOpen || a.OpID != "" {
		t.Fatalf("ask: %+v", a)
	}
}

// A window that has passed is not accepted, even before the sweeper closes the row (D9: afterwards the relay is an
// ordinary lead-initiated one).
func TestRelayCreate_DoesNotAcceptAnAskPastItsWindow(t *testing.T) {
	f := gateFixture(t, false, false, 0)
	f.openAsk(rid(520))
	f.clock.Add(team.RelayAskHoldS*1000 + 1)
	if code, _, _ := f.createRelay(rid(521), "/tmp/10.sock", "_mem001"); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if a := f.ask(rid(520)); a.State != team.RelayAskOpen || a.OpID != "" {
		t.Fatalf("ask: %+v", a)
	}
}

// Fault injection: the ask update fails → no op, no pool spend, the ask still open. Mutation gate: do the update
// outside the op transaction (or after the commit) → the op survives or the ask is accepted without an op (red).
func TestRelayCreate_AFailureAtTheAcceptRollsEverythingBack(t *testing.T) {
	boom := errors.New("boom")
	for _, pool := range []int{2, 0} {
		f := gateFixture(t, true, true, pool)
		f.openAsk(rid(530))
		f.m.store.afterAskAccept = func(*sql.Tx) error { return boom }
		code, _, _ := f.createRelay(rid(531), "/tmp/10.sock", "_mem001")
		if code != 500 || countRelayOps(t, f) != 0 || f.rowCount("member_relay") != 0 || f.poolLeft("sid-1") != pool {
			t.Errorf("pool %d: %d, ops %d, rows %d, pool %d", pool, code, countRelayOps(t, f), f.rowCount("member_relay"), f.poolLeft("sid-1"))
		}
		if a := f.ask(rid(530)); a.State != team.RelayAskOpen || a.OpID != "" {
			t.Errorf("pool %d: ask %+v", pool, a)
		}
	}
}

// The seam runs after the update, on a path where an ask was accepted: a failure there still rolls the update back.
func TestRelayCreate_AcceptIsInsideTheTransaction(t *testing.T) {
	f := gateFixture(t, false, false, 0)
	f.openAsk(rid(540))
	var seen string
	f.m.store.afterAskAccept = func(tx *sql.Tx) error {
		tx.QueryRow(`SELECT state FROM relay_asks WHERE id = ?`, rid(540)).Scan(&seen)
		return errors.New("stop")
	}
	f.createRelay(rid(541), "/tmp/10.sock", "_mem001")
	if seen != team.RelayAskAccepted {
		t.Fatalf("the seam saw state %q; the update must already be made when it runs", seen)
	}
	if a := f.ask(rid(540)); a.State != team.RelayAskOpen {
		t.Fatalf("rolled back: %+v", a)
	}
}

// Pool 0 under unattended mode: the op is awaiting_approval with its own RQ2 row, and the ask is still accepted.
func TestRelayCreate_AcceptsTheAskWhenTheOpAwaitsApproval(t *testing.T) {
	f := gateFixture(t, true, true, 0)
	f.openAsk(rid(550))
	code, op, _ := f.createRelay(rid(551), "/tmp/10.sock", "_mem001")
	if code != 201 || op.State != team.RelayAwaitingApproval {
		t.Fatalf("create: %d %+v", code, op)
	}
	if a := f.ask(rid(550)); a.State != team.RelayAskAccepted || a.OpID != op.ID {
		t.Fatalf("ask: %+v", a)
	}
}
