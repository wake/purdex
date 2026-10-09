package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// relayTransitions is the state machine of spec §8.1: from → the states a
// report may move the op to. A report of the op's current state is a
// no-op (idempotent per (op, state)); anything not listed is bad_transition.
var relayTransitions = map[team.RelayState]map[team.RelayState]bool{
	team.RelayAwaitingApproval: {team.RelayClaimed: true, team.RelayCancelled: true},
	team.RelayRequested:        {team.RelayClaimed: true, team.RelayCancelled: true, team.RelayFailed: true},
	team.RelayClaimed:          {team.RelayWriting: true, team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWriting:          {team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWritten:          {team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayCleared:          {team.RelayDone: true, team.RelayFailed: true},
}

// relayTransitionOK is relayTransitions per kind (RQ-2 §4.5): only the one state differs. A self op leaves
// awaiting_approval for claimed (its approval's close); a MEMBER op for requested (its member_relay row's approve) —
// it must not be claimed, and so relayed, before a person approves. Every other row of the map is shared.
func relayTransitionOK(kind team.RelayKind, from, to team.RelayState) bool {
	if from == team.RelayAwaitingApproval {
		if kind == team.RelayKindMember {
			return to == team.RelayRequested || to == team.RelayCancelled
		}
		return to == team.RelayClaimed || to == team.RelayCancelled
	}
	return relayTransitions[from][to]
}

// checkLineage guards the `cleared` transition inside ReportRelay's
// transaction (spec §8.4): the new session id and ref are set, the new
// session is not the old one, no other op has already cleared into the
// new session, and the new session is not an ancestor of the old one.
func checkLineage(tx *sql.Tx, cur team.RelayOp, r RelayReport) error {
	if r.NewSessionID == "" || r.NewRef == "" {
		return fmt.Errorf("%w: cleared needs new_session_id and new_ref", ErrBadRelayReport)
	}
	if r.NewSessionID == cur.SessionID {
		return fmt.Errorf("%w: new session equals the old one", ErrBadRelayReport)
	}
	var otherOp string
	err := tx.QueryRow(`SELECT op_id FROM session_lineage WHERE session_id = ?`, r.NewSessionID).Scan(&otherOp)
	if err == nil {
		return fmt.Errorf("%w: session %s already heads the lineage of op %s", ErrBadRelayReport, r.NewSessionID, otherOp)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Walk the old session's ancestors; the chain is finite because every
	// accepted row passed this check, so no cycle exists yet.
	for sid := cur.SessionID; sid != ""; {
		var pred string
		err := tx.QueryRow(`SELECT predecessor_session_id FROM session_lineage WHERE session_id = ?`, sid).Scan(&pred)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if pred == r.NewSessionID {
			return fmt.Errorf("%w: session %s is an ancestor of %s (cycle)", ErrBadRelayReport, r.NewSessionID, cur.SessionID)
		}
		sid = pred
	}
	return nil
}

// ErrClearedTargetHasRole refuses a cleared whose old session holds a live
// team role (lead, or active member of a live team) into a session that
// already holds one: a session holds at most one live role (P4-3 review
// R1). Nothing commits. It is a broken invariant, not a bad report (a
// /clear makes a fresh session): the handler answers 500, which the mod
// re-sends, and the op stays written for reconciliation (P6-4, #1735).
var ErrClearedTargetHasRole = errors.New("the new session already leads or is a member of a live team")

// ErrClearedRemoteMember refuses a cleared whose old session is a remote
// member (a team led on another host): nothing moves, the op is left as it was.
var ErrClearedRemoteMember = errors.New("the session is a member of a team led on another host; its relay cannot move that membership")

// The stored statusline reading belongs to the session that sent it, so a
// row that moves to a new session goes back to "no reading" (usage_at = 0,
// usage_pct NULL, the rest empty: what usageScan.reading treats as absent).
// Left as it was, the new session would show the old one's context until its
// own statusline arrives (PL-1f'3 review A-1).
const (
	resetMemberUsage = `usage_pct = NULL, usage_window = 0, usage_model = '', usage_effort = '', usage_at = 0`
	resetLeadUsage   = `lead_usage_pct = NULL, lead_usage_window = 0, lead_usage_model = '', lead_usage_effort = '', lead_usage_at = 0`
)

// moveTeamRoles is the team half of a cleared (spec §8.4), run in its
// lineage transaction after the lineage insert: the live team the old
// session leads now follows the new session and ref, and so does the old
// session's active member row of a live team. An ended team, and the rows
// of its members, stay as they ended (D4; P4-3 review H3). When the old
// session has a live role, a new session that already has one, either
// role, fails the whole cleared with ErrClearedTargetHasRole (R1).
func moveTeamRoles(tx *sql.Tx, oldSessionID string, r RelayReport) error {
	// A remote member's membership is bound to the session on this host and
	// to the lead host's command log; moving it needs a protocol this version
	// does not have, so a cleared of such a session fails whole (cross-host
	// team spec §12: no cross-host member relay yet).
	if role, err := memberRoleIn(tx, oldSessionID); err != nil {
		return err
	} else if role == sessionRoleMemberRemote {
		return fmt.Errorf("%w (%s)", ErrClearedRemoteMember, oldSessionID)
	}
	moving, err := hasLiveRoleIn(tx, oldSessionID)
	if err != nil {
		return err
	}
	if moving {
		// The unique indexes catch only the same role; check both (R1).
		if taken, err := hasLiveRoleIn(tx, r.NewSessionID); err != nil {
			return err
		} else if taken {
			return fmt.Errorf("%w (%s)", ErrClearedTargetHasRole, r.NewSessionID)
		}
	}
	if _, err := tx.Exec(`UPDATE teams SET lead_session_id = ?, lead_ref = ?, `+resetLeadUsage+`
		WHERE lead_session_id = ? AND ended_at = 0`, r.NewSessionID, r.NewRef, oldSessionID); err != nil {
		return fmt.Errorf("move lead: %w", err)
	}
	if _, err := tx.Exec(`UPDATE team_members SET session_id = ?, ref = ?, updated_at = ?, notice_armed = 1, `+resetMemberUsage+`
		WHERE session_id = ? AND state = 'active'
		  AND EXISTS (SELECT 1 FROM teams WHERE teams.id = team_members.team_id AND teams.ended_at = 0)`,
		r.NewSessionID, r.NewRef, r.At, oldSessionID); err != nil {
		return fmt.Errorf("move member: %w", err)
	}
	return nil
}

// hasLiveRoleIn reports whether sessionID leads a live team or is an
// active member of one, read in tx.
func hasLiveRoleIn(tx *sql.Tx, sessionID string) (bool, error) {
	if member, err := isLiveMemberIn(tx, sessionID); err != nil || member {
		return member, err
	}
	var one int
	err := tx.QueryRow(`SELECT 1 FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lead check %s: %w", sessionID, err)
	}
	return true, nil
}

// RelayReport is one transition: the target state, the new session id and
// ref (cleared only), the reason (failed / cancelled) and the time.
type RelayReport struct {
	State        team.RelayState
	NewSessionID string
	NewRef       string
	Reason       string
	At           int64
	// Expect, when set, makes the report conditional on the op still being as the caller judged it (state, updated_at,
	// seen_at): the sweeper's timeouts decide from a snapshot, and progress that landed since must win. A mismatch
	// answers ReportBadTransition with the op as it is, and writes nothing.
	Expect *RelayExpect
}

// RelayExpect is what a conditional report requires of the op's current row.
type RelayExpect struct {
	State     team.RelayState
	UpdatedAt int64
	SeenAt    int64
}

// ReportResult says what ReportRelay did.
type ReportResult int

const (
	ReportApplied       ReportResult = iota // the op moved to the reported state
	ReportNoop                              // the op was already in that state
	ReportBadTransition                     // the op's state does not lead to the reported one
)

// ReportRelay applies one transition under a write transaction: a report of
// the current state is ReportNoop (idempotent per (op, state)); a state
// relayTransitions does not allow is ReportBadTransition; otherwise the row
// is updated with a CAS on its state. For cleared, the lineage row is
// written in the same transaction (spec §8.4): session_lineage{new →
// old, old ref, op}, and the old session's live team and active member row
// move to the new session (moveTeamRoles); if any of it fails, nothing
// commits. The row after the attempt is returned in every case;
// ErrNoSuchRelayOp for an unknown id.
func (s *Store) ReportRelay(id string, r RelayReport) (team.RelayOp, ReportResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: begin: %w", id, err)
	}
	defer tx.Rollback()
	op, res, err := reportRelayIn(tx, id, r)
	if err != nil || res != ReportApplied {
		return op, res, err
	}
	if err := tx.Commit(); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: commit: %w", id, err)
	}
	s.notifyOp(id)
	return op, res, nil
}

// reportRelayIn is ReportRelay's statements on the caller's transaction
// (ReportRelay's own, or CreateSelfRelayApproved's), with ReportRelay's
// answers; the caller commits only a ReportApplied and rolls back on an
// error.
func reportRelayIn(tx *sql.Tx, id string, r RelayReport) (team.RelayOp, ReportResult, error) {
	// The first statement is a write, so SQLite takes the write lock at
	// once (same reasoning as peer_label.go Release): two concurrent
	// reports cannot both read the same state and both pass the CAS.
	if _, err := tx.Exec(`UPDATE relay_ops SET updated_at = updated_at WHERE id = ?`, id); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lock: %w", id, err)
	}
	cur, err := scanRelayOp(tx.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, ReportBadTransition, ErrNoSuchRelayOp
	}
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if r.Expect != nil && (cur.State != r.Expect.State || cur.UpdatedAt != r.Expect.UpdatedAt || cur.SeenAt != r.Expect.SeenAt) {
		return cur, ReportBadTransition, nil // progress landed since the caller looked: it wins
	}
	if cur.State == r.State {
		return cur, ReportNoop, nil
	}
	if !relayTransitionOK(cur.Kind, cur.State, r.State) {
		return cur, ReportBadTransition, nil
	}
	if r.State == team.RelayCleared {
		if err := checkLineage(tx, cur, r); err != nil {
			return cur, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
		}
	}
	next := cur
	next.State, next.Reason, next.UpdatedAt = r.State, r.Reason, r.At
	if r.State == team.RelayCleared {
		next.NewSessionID, next.NewRef = r.NewSessionID, r.NewRef
	}
	res, err := tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, new_session_id = ?, new_ref = ?, updated_at = ?
		WHERE id = ? AND state = ?`, string(next.State), next.Reason, next.NewSessionID, next.NewRef, next.UpdatedAt, id, string(cur.State))
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: state changed under the transaction", id)
	}
	if r.State == team.RelayCleared {
		// A plain INSERT: checkLineage has already proven the key is free,
		// so a conflict here is a real error and rolls the op back with it.
		if _, err := tx.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at)
			VALUES (?, ?, ?, ?, ?)`, r.NewSessionID, cur.SessionID, cur.Ref, id, r.At); err != nil {
			return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lineage: %w", id, err)
		}
		if err := moveTeamRoles(tx, cur.SessionID, r); err != nil {
			return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
		}
	}
	return next, ReportApplied, nil
}
