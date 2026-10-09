package teammod

import (
	"database/sql"
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

// autoApprovedQuery is the rows the daemon approved itself (decided_by
// kind unattended) decided at or after a since, then tail; its arguments
// are since, team.ClientKindUnattended, then tail's. A decided_by_json
// that is not valid JSON never reaches json_extract (which would fail the
// whole list): it is no decider of the daemon's, so the row is skipped. The daemon's decider has no addr (a
// click's always carries the caller's RemoteAddr), so a row from before RQ-0, when a decide request could name
// the kind `unattended` itself, is not taken for an automatic approval.
func autoApprovedQuery(tail string) string {
	return `SELECT ` + selectCols + ` FROM approval_requests
		WHERE state = 'approved' AND decided_at >= ?
		  AND CASE WHEN json_valid(decided_by_json) THEN json_extract(decided_by_json, '$.kind') END = ?
		  AND CASE WHEN json_valid(decided_by_json) THEN COALESCE(json_extract(decided_by_json, '$.addr'), '') END = '' ` + tail
}

// The tails of ListAutoApproved's three reads: a page (before, before,
// limit + 1), the rest of a millisecond (its decided_at, the page's last
// id) and whether an older millisecond remains (that decided_at).
const (
	autoApprovedPage  = `AND (? = 0 OR decided_at < ?) ORDER BY decided_at DESC, id LIMIT ?`
	autoApprovedRest  = `AND decided_at = ? AND id > ? ORDER BY id`
	autoApprovedOlder = `AND decided_at < ? LIMIT 1`
)

// ListAutoApproved is one page of the "while you were away" list (D-U23-6,
// decision 17): the requests the daemon approved itself (decided_by.kind
// unattended) with decided_at >= since — the switch's last off→on — and,
// when before > 0, decided_at < before (the cursor), newest first
// (decided_at DESC, id). A page holds limit rows, read as limit + 1 (an
// index range of approval_requests_state_decided), except that it never
// ends inside one millisecond: when row limit + 1 shares the last row's
// decided_at, the rest of that millisecond (by id) joins the page, so the
// next page (before = the last row's decided_at) skips nothing and repeats
// nothing. truncated says older rows remain. The reads share one
// transaction, so they see one commit. since 0 (never on) answers an empty
// page without a query. Never nil.
func (s *Store) ListAutoApproved(since, before int64, limit int) (rows []team.Approval, truncated bool, err error) {
	if since == 0 {
		return []team.Approval{}, false, nil
	}
	fail := func(err error) ([]team.Approval, bool, error) {
		return nil, false, fmt.Errorf("list auto-approved: %w", err)
	}
	if s.beforeListAutoApproved != nil {
		if err := s.beforeListAutoApproved(); err != nil {
			return fail(err)
		}
	}
	if limit < 1 {
		return fail(fmt.Errorf("limit %d is not positive", limit))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	read := func(tail string, args ...any) ([]team.Approval, error) {
		return autoApprovedIn(tx, autoApprovedQuery(tail), append([]any{since, team.ClientKindUnattended}, args...)...)
	}
	page, err := read(autoApprovedPage, before, before, limit+1)
	if err != nil {
		return fail(err)
	}
	if len(page) <= limit {
		return page, false, nil
	}
	last := page[limit-1]
	if page[limit].DecidedAt != last.DecidedAt {
		return page[:limit], true, nil // a row of an older millisecond remains
	}
	rest, err := read(autoApprovedRest, last.DecidedAt, last.ID)
	if err != nil {
		return fail(err)
	}
	older, err := read(autoApprovedOlder, last.DecidedAt)
	if err != nil {
		return fail(err)
	}
	return append(page[:limit], rest...), len(older) > 0, nil
}

// autoApprovedIn is the rows of query on tx, in its order. Never nil.
func autoApprovedIn(tx *sql.Tx, query string, args ...any) ([]team.Approval, error) {
	q, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []team.Approval{}
	for q.Next() {
		a, _, err := scanRow(q)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, q.Err()
}
