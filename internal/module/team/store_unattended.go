package teammod

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ErrMemberRelayIsLeads is returned by CreateSelfRelayApproved when the
// origin is an active member of a live team (U13): a member's relay is the
// lead's, so nothing was written.
var ErrMemberRelayIsLeads = errors.New("the session is an active member of a live team: its relay is the lead's")

// CreateSelfRelayApproved is the begin of a self relay the daemon approves
// itself (U23 unattended), CreateApproved's counterpart for the op and its
// row: in one write transaction it inserts op (awaiting_approval) and a
// (open), runs the click's approve statements (closeSelfRelayApprovedIn)
// and moves the op to what the approved row implies (claimed), so neither
// the op nor the row is ever committed open. op must be a's — one relay,
// as sameSelfRelay checks — and awaiting approval. A session that became an active member of a
// live team rolls everything back with ErrMemberRelayIsLeads (U13); a
// session with an open op, with ErrRelayOpOpen (the table's floor beneath
// the begin's check); any other failure, or a close that is no approval,
// writes nothing either. It returns the row and the op as committed.
func (s *Store) CreateSelfRelayApproved(op team.RelayOp, a team.Approval, hash string, c Close) (team.Approval, team.RelayOp, error) {
	fail := func(err error) (team.Approval, team.RelayOp, error) {
		return team.Approval{}, team.RelayOp{}, fmt.Errorf("create approved self relay %s: %w", a.ID, err)
	}
	if err := sameSelfRelay(op, a); err != nil {
		return fail(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at once.
	if err := insertRelayOpIn(tx, op); err != nil {
		return fail(err)
	}
	if _, err := insertRowIn(tx, a, hash, ""); err != nil {
		return fail(err)
	}
	if err := s.atApprovedInsert(tx); err != nil {
		return fail(err)
	}
	if _, member, err := s.closeSelfRelayApprovedIn(tx, a.ID, c, a.Origin.SessionID); err != nil {
		return fail(err)
	} else if member {
		return fail(ErrMemberRelayIsLeads)
	}
	after, err := approvedRowIn(tx, a.ID)
	if err != nil {
		return fail(err)
	}
	claimed, res, err := reportRelayIn(tx, op.ID, opReportForClosedRow(after, c.DecidedAt))
	if err != nil {
		return fail(err)
	}
	if res != ReportApplied {
		return fail(fmt.Errorf("op %s is %s; the approve could not claim it", op.ID, claimed.State))
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return after, claimed, nil
}

// sameSelfRelay is nil when op and a are one self relay, as the click's
// begin builds them (relay_handler.go): a self_relay row and an awaiting
// self op of the same host, session and ref, the op's RequestID the row's
// id and the row's payload op_id the op's id. The member re-check reads
// a.Origin.SessionID, so an op of another session would be claimed
// unchecked (U13).
func sameSelfRelay(op team.RelayOp, a team.Approval) error {
	var p team.SelfRelayPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return fmt.Errorf("%s row %s: payload: %w", a.Kind, a.ID, err)
	}
	if a.Kind != team.KindSelfRelay || op.Kind != team.RelayKindSelf || op.State != team.RelayAwaitingApproval ||
		op.HostID != a.HostID || op.SessionID != a.Origin.SessionID || op.Ref != a.Origin.Ref ||
		op.RequestID != a.ID || p.OpID != op.ID {
		return fmt.Errorf("op %s (%s, %s, host %q, session %q, ref %q, request %q) is not the awaiting self op of %s row %s (host %q, session %q, ref %q, payload op %q)",
			op.ID, op.Kind, op.State, op.HostID, op.SessionID, op.Ref, op.RequestID,
			a.Kind, a.ID, a.HostID, a.Origin.SessionID, a.Origin.Ref, p.OpID)
	}
	return nil
}

// ListAutoApproved is one page of the "while you were away" list (D-U23-6,
// decision 17): the requests the daemon approved itself (decided_by.kind
// unattended) with decided_at >= since — the switch's last off→on — and,
// when before > 0, decided_at < before (the cursor), newest first
// (decided_at DESC, id). A page holds limit rows, except that it never ends
// inside one millisecond: when the next row shares the last row's
// decided_at, every row of that millisecond joins the page, so the next
// page (before = the last row's decided_at) skips nothing and repeats
// nothing. truncated says older rows remain. since 0 (never on) answers an
// empty page without a query. Never nil.
func (s *Store) ListAutoApproved(since, before int64, limit int) (rows []team.Approval, truncated bool, err error) {
	out := []team.Approval{}
	if since == 0 {
		return out, false, nil
	}
	if limit < 1 {
		return nil, false, fmt.Errorf("list auto-approved: limit %d is not positive", limit)
	}
	q, err := s.db.Query(`SELECT `+selectCols+` FROM approval_requests
		WHERE state = 'approved' AND decided_at >= ? AND (? = 0 OR decided_at < ?)
		  AND json_extract(decided_by_json, '$.kind') = ?
		ORDER BY decided_at DESC, id`, since, before, before, team.ClientKindUnattended)
	if err != nil {
		return nil, false, fmt.Errorf("list auto-approved: %w", err)
	}
	defer q.Close()
	for q.Next() {
		a, _, err := scanRow(q)
		if err != nil {
			return nil, false, fmt.Errorf("list auto-approved: %w", err)
		}
		if len(out) >= limit && a.DecidedAt != out[len(out)-1].DecidedAt {
			return out, true, nil // a row of an older millisecond remains
		}
		out = append(out, a)
	}
	if err := q.Err(); err != nil {
		return nil, false, fmt.Errorf("list auto-approved: %w", err)
	}
	return out, false, nil
}
