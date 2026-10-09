package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// The member_relay approval row's state machine (RQ-2a; spec 2026-10-09-rq2-member-relay-approval-spec.md §3–§4).
// INVARIANT: a member op is awaiting_approval iff its member_relay row is open, and every write that changes one
// changes the other in the SAME transaction. Nothing opens such a row yet (RQ-2b); tests insert them.

// Close reasons a failed approve-time re-check stores (close_reason).
const (
	memberRelayMemberGone = "member_gone"
	memberRelayTeamEnded  = "team_ended"
)

// kindOf is the kind of approval id; false when there is no such row or it cannot be read (the caller then takes the
// generic close, whose own statement reports the same problem).
func (s *Store) kindOf(id string) (team.Kind, bool) {
	var k string
	if err := s.db.QueryRow(`SELECT kind FROM approval_requests WHERE id = ?`, id).Scan(&k); err != nil {
		return "", false
	}
	return team.Kind(k), true
}

// lockApproval takes the write lock before any read (a no-op write), as closeSelfRelayApprovedIn does.
func lockApproval(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`UPDATE approval_requests SET id = id WHERE id = ?`, id)
	return err
}

// opOfRowIn is the member op linked to the row (relay_ops.request_id); ok is false when none.
func opOfRowIn(tx *sql.Tx, rowID string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(tx.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE request_id = ?`, rowID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	return op, err == nil, err
}

// moveOpWithRowIn moves the row's op the way the row's close implies (denied → cancelled{denied}, timeout →
// cancelled{timeout}, anything else → cancelled{abandoned}) inside the close's transaction. An op that is no longer
// awaiting (the invariant broken; the boot repairs it) is left as it is: the row's close must not fail for it.
// It returns the op's id when it moved, for the wake after the commit.
func (s *Store) moveOpWithRowIn(tx *sql.Tx, a team.Approval, at int64) (string, error) {
	op, ok, err := opOfRowIn(tx, a.ID)
	if err != nil || !ok || op.State != team.RelayAwaitingApproval {
		return "", err
	}
	if s.beforeMemberOpMove != nil {
		if err := s.beforeMemberOpMove(); err != nil {
			return "", err
		}
	}
	rep := opReportForClosedRow(a, at)
	if _, res, err := reportRelayIn(tx, op.ID, rep); err != nil || res != ReportApplied {
		if err == nil {
			err = fmt.Errorf("op %s did not take %s (%v)", op.ID, rep.State, res)
		}
		return "", err
	}
	return op.ID, nil
}

// closeMemberRelay is closeWhere for a member_relay row: the CAS and the op's move in one transaction. An approve
// is not closed here (CloseMemberRelayApproved is the only approve).
func (s *Store) closeMemberRelay(id string, c Close, guard string, guardArg int64) (team.Approval, bool, error) {
	fail := func(err error) (team.Approval, bool, error) {
		return team.Approval{}, false, fmt.Errorf("close member relay row %s: %w", id, err)
	}
	if c.State == team.StateApproved {
		return fail(errors.New("a member_relay row is approved by CloseMemberRelayApproved"))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if err := lockApproval(tx, id); err != nil {
		return fail(err)
	}
	n, err := closeRowIn(tx, id, c, guard, guardArg)
	if err != nil {
		return fail(err)
	}
	a, _, err := getRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	opID := ""
	if n == 1 {
		if opID, err = s.moveOpWithRowIn(tx, a, c.DecidedAt); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	if opID != "" {
		s.notifyOp(opID)
	}
	return a, n == 1, nil
}

// recheckMemberRelayIn is §4.3 step 1: the op awaits, its team is live, its session still has an active member row in
// that team. reason is "" when all hold, else the close_reason to cancel the row with.
func recheckMemberRelayIn(tx *sql.Tx, op team.RelayOp) (reason string, err error) {
	var ended int64
	if err := tx.QueryRow(`SELECT ended_at FROM teams WHERE id = ?`, op.TeamID).Scan(&ended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memberRelayTeamEnded, nil
		}
		return "", err
	}
	if ended != 0 {
		return memberRelayTeamEnded, nil
	}
	var one int
	err = tx.QueryRow(`SELECT 1 FROM team_members WHERE session_id = ? AND team_id = ? AND state = 'active'`, op.SessionID, op.TeamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return memberRelayMemberGone, nil
	}
	return "", err
}

// CloseMemberRelayApproved is the approve of a member_relay row — a click's or the daemon's — in ONE write
// transaction, in this order (RQ-2 §4.3): re-check; row CAS to approved; spend the lead's pool (only for an Auto close
// with SpendQuota); op awaiting_approval → requested. Any failure rolls all of it back. A failed re-check closes the
// row cancelled (reason member_gone | team_ended) and the op cancelled{abandoned} instead, in the same transaction;
// refused is that reason. won says this call closed the row (either way); a CAS lost to a deny or a timeout is
// not won and nothing is spent.
func (s *Store) CloseMemberRelayApproved(id string, c Close) (a team.Approval, won bool, refused string, err error) {
	fail := func(err error) (team.Approval, bool, string, error) {
		return team.Approval{}, false, "", fmt.Errorf("approve member relay %s: %w", id, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if err := lockApproval(tx, id); err != nil {
		return fail(err)
	}
	if cur, _, err := getRowIn(tx, id); err != nil {
		return fail(err)
	} else if cur.State != team.StateOpen { // a deny, a timeout or an abandonment committed first: a lost CAS, not an error
		return cur, false, "", nil
	}
	op, ok, err := opOfRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	if !ok || op.State != team.RelayAwaitingApproval {
		return fail(fmt.Errorf("its op is missing or no longer awaiting approval (%+v)", op))
	}
	reason, err := recheckMemberRelayIn(tx, op)
	if err != nil {
		return fail(err)
	}
	if reason != "" {
		n, err := closeRowIn(tx, id, Close{State: team.StateCancelled, DecidedAt: c.DecidedAt, Reason: reason}, "", 0)
		if err != nil {
			return fail(err)
		}
		cancelled, _, err := getRowIn(tx, id)
		if err != nil {
			return fail(err)
		}
		opID := ""
		if n == 1 {
			if opID, err = s.moveOpWithRowIn(tx, cancelled, c.DecidedAt); err != nil {
				return fail(err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fail(err)
		}
		s.notifyOp(opID)
		return cancelled, n == 1, reason, nil
	}
	n, err := closeRowIn(tx, id, c, "", 0)
	if err != nil {
		return fail(err)
	}
	if n == 0 { // a deny or a timeout committed first: roll back, nothing spent
		row, _, err := getRowIn(tx, id)
		if err != nil {
			return fail(err)
		}
		return row, false, "", nil
	}
	if c.Auto && c.SpendQuota {
		if err := spendPoolIn(tx, op.TeamID, c.DecidedAt); err != nil {
			return fail(err)
		}
		if c.SpentOut != nil {
			*c.SpentOut = true
		}
	}
	if s.beforeMemberOpMove != nil {
		if err := s.beforeMemberOpMove(); err != nil {
			return fail(err)
		}
	}
	if _, res, err := reportRelayIn(tx, op.ID, RelayReport{State: team.RelayRequested, At: c.DecidedAt}); err != nil || res != ReportApplied {
		if err == nil {
			err = fmt.Errorf("op %s did not take requested (%v)", op.ID, res)
		}
		return fail(err)
	}
	row, _, err := getRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	s.notifyOp(op.ID)
	return row, true, "", nil
}

// spendPoolIn spends one of the member_pool_left of the chain of the team's CURRENT lead (teams.lead_session_id), on
// tx. The chain root is walked on the same transaction; the UPDATE is guarded by member_pool_left >= 1. Zero rows is
// ErrQuotaExhausted.
func spendPoolIn(tx *sql.Tx, teamID string, at int64) error {
	var lead string
	if err := tx.QueryRow(`SELECT lead_session_id FROM teams WHERE id = ?`, teamID).Scan(&lead); err != nil {
		return fmt.Errorf("spend member pool: lead of team %s: %w", teamID, err)
	}
	root, _, err := chainRootIn(tx, lead)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE relay_quotas SET member_pool_left = member_pool_left - 1, updated_at = ?, rev = rev + 1
		WHERE root_session_id = ? AND member_pool_left >= 1`, at, root)
	if err != nil {
		return fmt.Errorf("spend member pool of %s: %w", root, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("spend member pool of %s: %w", root, err)
	} else if n == 0 {
		return ErrQuotaExhausted
	}
	return nil
}

// CancelMemberRelayIfGone is the boot's (and any caller's) re-check of an OPEN row on its own: when the team or the
// member is gone, the row closes cancelled and its op cancelled{abandoned} in one transaction. reason is "" when
// nothing was wrong (nothing written) or the row was not open.
func (s *Store) CancelMemberRelayIfGone(id string, at int64) (a team.Approval, won bool, reason string, err error) {
	fail := func(err error) (team.Approval, bool, string, error) {
		return team.Approval{}, false, "", fmt.Errorf("re-check member relay %s: %w", id, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if err := lockApproval(tx, id); err != nil {
		return fail(err)
	}
	row, _, err := getRowIn(tx, id)
	if err != nil || row.State != team.StateOpen {
		return row, false, "", err
	}
	op, ok, err := opOfRowIn(tx, id)
	if err != nil || !ok || op.State != team.RelayAwaitingApproval {
		return row, false, "", err
	}
	if reason, err = recheckMemberRelayIn(tx, op); err != nil || reason == "" {
		return row, false, "", err
	}
	n, err := closeRowIn(tx, id, Close{State: team.StateCancelled, DecidedAt: at, Reason: reason}, "", 0)
	if err != nil {
		return fail(err)
	}
	cancelled, _, err := getRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	opID := ""
	if n == 1 {
		if opID, err = s.moveOpWithRowIn(tx, cancelled, at); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	s.notifyOp(opID)
	return cancelled, n == 1, reason, nil
}
