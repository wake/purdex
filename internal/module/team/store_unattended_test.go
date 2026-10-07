package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// unattendedClose is the Close of the daemon's own approve (U23): decided
// by {kind unattended, label 無人值守模式}, no addr.
func unattendedClose(at int64, g *team.Grant) Close {
	by := team.UnattendedClient()
	return Close{State: team.StateApproved, DecidedAt: at, DecidedBy: &by, Grant: g}
}

// leadApproveIn is the create-time approve of lead request id from sid:
// the click's statements (closeLeadApprovedIn) on CreateApproved's transaction.
func leadApproveIn(id, sid string, c Close) func(tx *sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := closeLeadApprovedIn(tx, id, c, leadTeam(id, sid, "_abc123", *c.Grant, c.DecidedAt))
		return err
	}
}

// selfRelayRow is the self_relay approval row of op, keyed by its request id.
func selfRelayRow(op team.RelayOp) team.Approval {
	payload, _ := json.Marshal(team.SelfRelayPayload{OpID: op.ID, UsedPercentage: 72.4, Window: 200_000})
	return team.Approval{
		ID: op.RequestID, Kind: team.KindSelfRelay, HostID: op.HostID,
		Origin:  team.Origin{SessionID: op.SessionID, Ref: op.Ref, PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen, CreatedAt: op.CreatedAt,
		DeadlineAt: op.CreatedAt + team.SelfRelayDeadlineS*1000, LeaseUntil: op.CreatedAt + team.LeaseS*1000,
	}
}

// assertInvisible fails unless the pool's other connections — every reader
// outside the transaction: a snapshot, a list, a poll — see neither request
// id nor relay op opID ("" skips the op) of session sid.
func assertInvisible(t *testing.T, s *Store, id, sid, opID string) {
	t.Helper()
	open, err := s.ListOpen()
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	for _, a := range open {
		if a.ID == id {
			t.Errorf("ListOpen sees %s open before the commit", id)
		}
	}
	if _, ok, err := s.Get(id); err != nil || ok {
		t.Errorf("Get(%s) before the commit: ok=%v err=%v, want absent", id, ok, err)
	}
	if opID == "" {
		return
	}
	if _, ok, err := s.GetRelayOp(opID); err != nil || ok {
		t.Errorf("GetRelayOp(%s) before the commit: ok=%v err=%v, want absent", opID, ok, err)
	}
	if op, ok, err := s.OpenRelayOpBySession(sid); err != nil || ok {
		t.Errorf("OpenRelayOpBySession(%s) before the commit: %+v ok=%v err=%v, want none", sid, op, ok, err)
	}
}

// assertDecidedByUnattended fails unless a is approved at at by the daemon.
func assertDecidedByUnattended(t *testing.T, a team.Approval, at int64) {
	t.Helper()
	if a.State != team.StateApproved || a.DecidedAt != at || a.DecidedBy == nil || *a.DecidedBy != team.UnattendedClient() {
		t.Fatalf("row = %s at %d by %+v, want approved at %d by %+v (no addr)", a.State, a.DecidedAt, a.DecidedBy, at, team.UnattendedClient())
	}
}

// Decision 1 (Review focus 1): a request the daemon approves at create is
// inserted and approved in one write transaction, so its first committed
// state is approved. The seam runs between the insert and the approve:
// inside the transaction the row is open; on the pool's other connections
// (WAL readers see the last commit) ListOpen and Get see nothing, so no
// snapshot and no poll can show it open. Mutation gate: commit the insert
// before the approve (two transactions) → red.
func TestCreateApproved_NeverVisibleOpen(t *testing.T) {
	s := openTestStore(t)
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	ran := false
	s.afterApprovedInsert = func(tx *sql.Tx) error {
		ran = true
		if in, _, err := getRowIn(tx, "id-1"); err != nil || in.State != team.StateOpen {
			t.Errorf("inside the transaction: %s err=%v, want the inserted open row", in.State, err)
		}
		assertInvisible(t, s, "id-1", "sid-1", "")
		return nil
	}
	got, err := s.CreateApproved(openApproval("id-1", "sid-1", 1000), "h1", leadApproveIn("id-1", "sid-1", unattendedClose(2000, &g)))
	if err != nil || !ran {
		t.Fatalf("CreateApproved: err=%v seam ran=%v", err, ran)
	}
	assertDecidedByUnattended(t, got, 2000)
	if stored, hash, err := s.getRow("id-1"); err != nil || hash != "h1" || !reflect.DeepEqual(stored, got) {
		t.Fatalf("stored = %+v hash %q err=%v, want the answered row with hash h1", stored, hash, err)
	}
}

// A lead request approved at create has its team, created in the same
// transaction with the grant it was approved with (spec §6.2).
func TestCreateApproved_LeadCreatesTheTeam(t *testing.T) {
	s := openTestStore(t)
	g := team.Grant{MaxMembers: 2, Roots: []string{"/w"}}
	got, err := s.CreateApproved(openApproval("id-1", "sid-1", 1000), "h1", leadApproveIn("id-1", "sid-1", unattendedClose(2000, &g)))
	if err != nil || !reflect.DeepEqual(got.Grant, &g) {
		t.Fatalf("CreateApproved: grant %+v err=%v, want %+v", got.Grant, err, g)
	}
	tm, ok, err := s.LiveTeamByLead("sid-1")
	if err != nil || !ok || tm.ID != "id-1" || tm.RequestID != "id-1" || !reflect.DeepEqual(tm.Grant, g) || tm.CreatedAt != 2000 {
		t.Fatalf("team = %+v ok=%v err=%v, want team id-1 with %+v", tm, ok, err, g)
	}
}

// A refusal of the approve's own re-checks rolls the insert back with it:
// no row and no team, and the refusal is the error (the create answers it).
func TestCreateApproved_RefusalWritesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*testing.T, *Store)
		want  error
	}{
		"the origin became a member": {func(t *testing.T, s *Store) {
			seedTeam(t, s, "team-x", "lead-sid", 500)
			seedMember(t, s, "op-1", "team-x", "sid-1", 600)
		}, ErrMemberCannotLead},
		"the origin already leads": {func(t *testing.T, s *Store) { seedTeam(t, s, "team-x", "sid-1", 500) }, ErrLeadHasTeam},
	} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			tc.setup(t, s)
			g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
			if _, err := s.CreateApproved(openApproval("id-1", "sid-1", 1000), "h1", leadApproveIn("id-1", "sid-1", unattendedClose(2000, &g))); !errors.Is(err, tc.want) {
				t.Fatalf("CreateApproved: err=%v, want %v", err, tc.want)
			}
			if _, ok, err := s.Get("id-1"); err != nil || ok {
				t.Fatalf("row after the refusal: ok=%v err=%v, want none", ok, err)
			}
			if n := countTeams(t, s); n != 1 {
				t.Fatalf("teams after the refusal = %d, want only the seeded one", n)
			}
		})
	}
}

// CreateApproved commits only a row its approve left approved: an approve
// that closes nothing, or closes it another way, is an error and writes
// nothing; so is an id already in use (the caller answers a replay under
// createMu before it), which leaves the stored row as it was.
func TestCreateApproved_MisuseWritesNothing(t *testing.T) {
	for name, approveIn := range map[string]func(*sql.Tx) error{
		"an approve that closes nothing": func(*sql.Tx) error { return nil },
		"an approve that denies": func(tx *sql.Tx) error {
			_, err := closeRowIn(tx, "id-1", Close{State: team.StateDenied, DecidedAt: 2000}, "", 0)
			return err
		},
	} {
		s := openTestStore(t)
		if _, err := s.CreateApproved(openApproval("id-1", "sid-1", 1000), "h1", approveIn); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, ok, err := s.Get("id-1"); err != nil || ok {
			t.Errorf("%s: row ok=%v err=%v, want none", name, ok, err)
		}
	}
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h0"); err != nil {
		t.Fatal(err)
	}
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	if _, err := s.CreateApproved(openApproval("id-1", "sid-1", 1000), "h1", leadApproveIn("id-1", "sid-1", unattendedClose(2000, &g))); err == nil {
		t.Error("an id in use: no error")
	}
	if a, hash, err := s.getRow("id-1"); err != nil || a.State != team.StateOpen || hash != "h0" || countTeams(t, s) != 0 {
		t.Fatalf("an id in use: stored %s hash %q err=%v, want the open row untouched and no team", a.State, hash, err)
	}
}

// Decision 1 for a self relay: the op and its row are inserted, approved
// and the op claimed in one transaction. The seam sees, outside it,
// neither the op nor the row; afterwards the op is claimed (the mod's wait
// answers approved at once) and the row approved by the daemon.
func TestCreateSelfRelayApproved_OpClaimedRowApprovedNeverOpen(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "sid-1", "_abc123", 1000)
	a := selfRelayRow(op)
	ran := false
	s.afterApprovedInsert = func(tx *sql.Tx) error {
		ran = true
		in, _, err := getRowIn(tx, a.ID)
		inOp, opErr := scanRelayOp(tx.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, op.ID))
		if err != nil || opErr != nil || in.State != team.StateOpen || inOp.State != team.RelayAwaitingApproval {
			t.Errorf("inside the transaction: row %s (%v), op %s (%v); want open and awaiting_approval", in.State, err, inOp.State, opErr)
		}
		assertInvisible(t, s, a.ID, "sid-1", op.ID)
		return nil
	}
	row, gotOp, err := s.CreateSelfRelayApproved(op, a, "h1", unattendedClose(2000, nil))
	if err != nil || !ran {
		t.Fatalf("CreateSelfRelayApproved: err=%v seam ran=%v", err, ran)
	}
	assertDecidedByUnattended(t, row, 2000)
	if row.Grant != nil || row.Kind != team.KindSelfRelay {
		t.Fatalf("row = %s with grant %+v, want a self_relay row with no grant", row.Kind, row.Grant)
	}
	if gotOp.ID != "op-1" || gotOp.State != team.RelayClaimed || gotOp.UpdatedAt != 2000 {
		t.Fatalf("op = %s %s at %d, want op-1 claimed at 2000", gotOp.ID, gotOp.State, gotOp.UpdatedAt)
	}
	if stored, ok, err := s.GetRelayOp("op-1"); err != nil || !ok || !reflect.DeepEqual(stored, gotOp) {
		t.Fatalf("stored op = %+v ok=%v err=%v, want %+v", stored, ok, err, gotOp)
	}
	if byReq, ok, err := s.RelayOpByRequest(a.ID); err != nil || !ok || byReq.ID != "op-1" {
		t.Fatalf("op by request = %+v ok=%v err=%v", byReq, ok, err)
	}
	if stored, hash, err := s.getRow(a.ID); err != nil || hash != "h1" || !reflect.DeepEqual(stored, row) {
		t.Fatalf("stored row = %+v hash %q err=%v, want the answered row", stored, hash, err)
	}
}

// U13 inside the transaction: a session that is an active member of a live
// team gets no self relay; the op and the row roll back together.
func TestCreateSelfRelayApproved_MemberRollsBack(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-x", "lead-sid", 500)
	seedMember(t, s, "m-1", "team-x", "sid-1", 600)
	op := selfOp("op-1", "sid-1", "_abc123", 1000)
	a := selfRelayRow(op)
	if _, _, err := s.CreateSelfRelayApproved(op, a, "h1", unattendedClose(2000, nil)); !errors.Is(err, ErrMemberRelayIsLeads) {
		t.Fatalf("CreateSelfRelayApproved: err=%v, want ErrMemberRelayIsLeads", err)
	}
	if _, ok, err := s.Get(a.ID); err != nil || ok {
		t.Fatalf("row: ok=%v err=%v, want none", ok, err)
	}
	if _, ok, err := s.GetRelayOp("op-1"); err != nil || ok {
		t.Fatalf("op: ok=%v err=%v, want none", ok, err)
	}
}

// A self relay the store cannot create approved writes nothing: an op that
// is not the row's or not awaiting approval, a close that is no approval,
// and a session that already has an open op (the table's floor beneath the
// begin's check: ErrRelayOpOpen).
func TestCreateSelfRelayApproved_MisuseWritesNothing(t *testing.T) {
	type tc struct {
		op    func(team.RelayOp) team.RelayOp
		c     Close
		setup func(*testing.T, *Store)
		want  error
	}
	same := func(op team.RelayOp) team.RelayOp { return op }
	for name, tc := range map[string]tc{
		"another request's op": {op: func(op team.RelayOp) team.RelayOp { op.RequestID = "req-x"; return op }, c: unattendedClose(2000, nil)},
		"an op past approval":  {op: func(op team.RelayOp) team.RelayOp { op.State = team.RelayRequested; return op }, c: unattendedClose(2000, nil)},
		"a deny":               {op: same, c: Close{State: team.StateDenied, DecidedAt: 2000}},
		"an open op already": {op: same, c: unattendedClose(2000, nil), want: ErrRelayOpOpen, setup: func(t *testing.T, s *Store) {
			if err := s.CreateRelayOp(selfOp("op-0", "sid-1", "_abc123", 900)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			if tc.setup != nil {
				tc.setup(t, s)
			}
			base := selfOp("op-1", "sid-1", "_abc123", 1000)
			_, _, err := s.CreateSelfRelayApproved(tc.op(base), selfRelayRow(base), "h1", tc.c)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err=%v, want an error (%v)", err, tc.want)
			}
			if _, ok, err := s.Get(base.RequestID); err != nil || ok {
				t.Fatalf("row: ok=%v err=%v, want none", ok, err)
			}
			if _, ok, err := s.GetRelayOp("op-1"); err != nil || ok {
				t.Fatalf("op: ok=%v err=%v, want none", ok, err)
			}
		})
	}
}
