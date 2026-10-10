package teammod

import (
	"errors"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A member relay ask's store (spec 2026-10-10-member-relay-ask §2, §3.1 step 3, §3.3): each rule of the write-first
// transaction, the closes, and retention.

func askOf(id, sid string, at int64) RelayAsk {
	return RelayAsk{ID: id, SessionID: sid, UsedPct: 71, Window: 1000000, CreatedAt: at, ExpiresAt: at + team.RelayAskHoldS*1000}
}

func TestCreateRelayAsk_StoresAnOpenAskForAnActiveLocalMember(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	got, replay, err := f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if err != nil || replay {
		t.Fatalf("create: %+v replay=%v err=%v", got, replay, err)
	}
	if got.State != team.RelayAskOpen || got.TeamID != uid(1) || got.SpawnOp != mr.SpawnOp || got.NotifiedAt != 0 || got.ExpiresAt != 1000+300_000 {
		t.Fatalf("stored ask: %+v", got)
	}
	back, ok, err := f.m.store.GetRelayAsk(rid(1))
	if err != nil || !ok || back != got {
		t.Fatalf("get: %+v ok=%v err=%v", back, ok, err)
	}
}

func TestCreateRelayAsk_Refusals(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	if _, _, err := f.m.store.CreateRelayAsk(askOf(rid(1), "sid-nobody", 1)); !errors.Is(err, ErrAskNotMember) {
		t.Fatalf("a stranger: %v", err)
	}
	// a remote member row
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'elsewhere:9' WHERE spawn_op = ?`, mr.SpawnOp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.store.CreateRelayAsk(askOf(rid(2), mr.SessionID, 1)); !errors.Is(err, ErrAskRemote) {
		t.Fatalf("a remote member: %v", err)
	}
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'h:1' WHERE spawn_op = ?`, mr.SpawnOp); err != nil {
		t.Fatal(err)
	}
	// an open relay op
	if err := f.m.store.CreateRelayOp(team.RelayOp{ID: rid(3), Kind: team.RelayKindMember, HostID: "h:1", SessionID: mr.SessionID, Ref: mr.Ref,
		TeamID: uid(1), State: team.RelayRequested, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.store.CreateRelayAsk(askOf(rid(4), mr.SessionID, 1)); !errors.Is(err, ErrAskRelayOpen) {
		t.Fatalf("an open relay: %v", err)
	}
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = 'done'`); err != nil {
		t.Fatal(err)
	}
	// a released member
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = ?`, mr.SpawnOp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.store.CreateRelayAsk(askOf(rid(5), mr.SessionID, 1)); !errors.Is(err, ErrAskNotMember) {
		t.Fatalf("a released member: %v", err)
	}
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused ask left %d rows", n)
	}
}

func TestCreateRelayAsk_OpenAskAndReplay(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	first, _, err := f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if err != nil {
		t.Fatal(err)
	}
	// the same request id: the stored ask, a replay
	again, replay, err := f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 9000))
	if err != nil || !replay || again != first {
		t.Fatalf("same id: %+v replay=%v err=%v", again, replay, err)
	}
	// another request id while one is open: the open ask, a replay (no second row)
	other, replay, err := f.m.store.CreateRelayAsk(askOf(rid(2), mr.SessionID, 9000))
	if err != nil || !replay || other.ID != rid(1) {
		t.Fatalf("second id: %+v replay=%v err=%v", other, replay, err)
	}
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows = %d", n)
	}
	// once it is closed, a new id is a new ask
	if _, err := f.m.store.ExpireRelayAsks(1000 + 300_000); err != nil {
		t.Fatal(err)
	}
	next, replay, err := f.m.store.CreateRelayAsk(askOf(rid(3), mr.SessionID, 400_000))
	if err != nil || replay || next.ID != rid(3) {
		t.Fatalf("after close: %+v replay=%v err=%v", next, replay, err)
	}
	// the stored answer of a closed ask's own id is that ask, closed
	old, replay, err := f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 400_000))
	if err != nil || !replay || old.State != team.RelayAskExpired {
		t.Fatalf("closed replay: %+v replay=%v err=%v", old, replay, err)
	}
}

func TestExpireRelayAsks_AtTheDeadlineAndNotBefore(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if n, err := f.m.store.ExpireRelayAsks(1000 + 299_999); err != nil || n != 0 {
		t.Fatalf("before: n=%d err=%v", n, err)
	}
	if n, err := f.m.store.ExpireRelayAsks(1000 + 300_000); err != nil || n != 1 {
		t.Fatalf("at the deadline: n=%d err=%v", n, err)
	}
	a, _, _ := f.m.store.GetRelayAsk(rid(1))
	if a.State != team.RelayAskExpired || a.ClosedAt != 1000+300_000 {
		t.Fatalf("expired ask: %+v", a)
	}
}

func TestWithdrawRelayAsks(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	// the member is still active: nothing to withdraw
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(2000); err != nil || n != 0 {
		t.Fatalf("active: n=%d err=%v", n, err)
	}
	// by compaction
	ok, err := f.m.store.WithdrawRelayAsk(mr.SessionID, team.RelayAskWithdrawCompacted, 3000)
	if err != nil || !ok {
		t.Fatalf("compacted: ok=%v err=%v", ok, err)
	}
	a, _, _ := f.m.store.GetRelayAsk(rid(1))
	if a.State != team.RelayAskWithdrawn || a.Reason != team.RelayAskWithdrawCompacted || a.ClosedAt != 3000 {
		t.Fatalf("withdrawn: %+v", a)
	}
	if ok, _ := f.m.store.WithdrawRelayAsk(mr.SessionID, team.RelayAskWithdrawCompacted, 3000); ok {
		t.Fatal("a closed ask is not withdrawn twice")
	}
	// by the member leaving: released, then the team ending
	f.m.store.CreateRelayAsk(askOf(rid(2), mr.SessionID, 4000))
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = ?`, mr.SpawnOp); err != nil {
		t.Fatal(err)
	}
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(5000); err != nil || n != 1 {
		t.Fatalf("released: n=%d err=%v", n, err)
	}
	a, _, _ = f.m.store.GetRelayAsk(rid(2))
	if a.State != team.RelayAskWithdrawn || a.Reason != team.RelayAskWithdrawMemberLeft {
		t.Fatalf("member_left: %+v", a)
	}
}

func TestWithdrawAsksOfInactiveMembers_ATeamThatEnded(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 2000 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(3000); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestMarkAskNotified_OnceAndOnlyWhileOpen(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if ok, err := f.m.store.MarkAskNotified(rid(1), 1500); err != nil || !ok {
		t.Fatalf("first: ok=%v err=%v", ok, err)
	}
	if ok, _ := f.m.store.MarkAskNotified(rid(1), 1600); ok {
		t.Fatal("notified twice")
	}
	a, _, _ := f.m.store.GetRelayAsk(rid(1))
	if a.NotifiedAt != 1500 {
		t.Fatalf("notified_at = %d", a.NotifiedAt)
	}
}

func TestListUnnotifiedAsks_OpenAndUnexpiredOnly(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	list, err := f.m.store.ListUnnotifiedAsks(1000 + 299_999)
	if err != nil || len(list) != 1 || list[0].ID != rid(1) {
		t.Fatalf("unnotified: %+v err=%v", list, err)
	}
	if list, _ := f.m.store.ListUnnotifiedAsks(1000 + 300_000); len(list) != 0 {
		t.Fatalf("an expired ask is never retried: %+v", list)
	}
	f.m.store.MarkAskNotified(rid(1), 1500)
	if list, _ := f.m.store.ListUnnotifiedAsks(2000); len(list) != 0 {
		t.Fatalf("a notified ask: %+v", list)
	}
}

// Retention: closed rows older than 30 days go; an open ask never does, however old.
func TestPruneRelayAsks_NeverDeletesAnOpenAsk(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("3")
	f.m.store.CreateRelayAsk(askOf(rid(1), mr.SessionID, 1000))
	if n, err := f.m.store.PruneRelayAsks(1 << 40); err != nil || n != 0 {
		t.Fatalf("an open ask: n=%d err=%v", n, err)
	}
	f.m.store.ExpireRelayAsks(1000 + 300_000)
	if n, _ := f.m.store.PruneRelayAsks(1000 + 300_000); n != 0 {
		t.Fatalf("closed at the cut-off exactly: n=%d", n)
	}
	if n, err := f.m.store.PruneRelayAsks(1000 + 300_001); err != nil || n != 1 {
		t.Fatalf("closed before the cut-off: n=%d err=%v", n, err)
	}
}
