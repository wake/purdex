package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	after, claimed, _, err := s.createSelfRelay(op, a, hash, c, nil, nil)
	return after, claimed, err
}

// CreateSelfRelayApprovedThen is CreateSelfRelayApproved with one more statement set run on the same transaction after the op is
// claimed and before it commits (a person's own /relay ends the session's pauses there, RT-1a): if then fails nothing is written.
func (s *Store) CreateSelfRelayApprovedThen(op team.RelayOp, a team.Approval, hash string, c Close, then func(tx *sql.Tx) error) (team.Approval, team.RelayOp, error) {
	after, claimed, _, err := s.createSelfRelay(op, a, hash, c, nil, then)
	return after, claimed, err
}

// CreateSelfRelayOrPause is SpendOrPause for a self / idle relay begin (unattended quota pause spec §3.3): CreateSelfRelayApproved
// with one more outcome. In ONE write transaction that takes the writer lock first: (1) a session that already holds its
// `quota` pause is answered PauseAlready, nothing written; (2) the op and its row are inserted and approved as before, spending
// a unit; (3) when that finds the chain out of quota, the op and row are rolled back to a savepoint and the pause is written
// in the same transaction (the session's `quota` row, a `lead` row for each live local member of a lead, the outbox rows) and the
// answer is PausePaused, no op. A quota PUT cannot slip between the check and the pause: it waits for the lock.
func (s *Store) CreateSelfRelayOrPause(op team.RelayOp, a team.Approval, hash string, c Close, plan PausePlan) (team.Approval, team.RelayOp, PauseOutcome, error) {
	return s.createSelfRelay(op, a, hash, c, &plan, nil)
}

func (s *Store) createSelfRelay(op team.RelayOp, a team.Approval, hash string, c Close, plan *PausePlan, then func(tx *sql.Tx) error) (team.Approval, team.RelayOp, PauseOutcome, error) {
	fail := func(err error) (team.Approval, team.RelayOp, PauseOutcome, error) {
		return team.Approval{}, team.RelayOp{}, PauseNone, fmt.Errorf("create approved self relay %s: %w", a.ID, err)
	}
	if err := sameSelfRelay(op, a); err != nil {
		return fail(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	if plan != nil {
		// The writer lock first (a write statement that touches no row), so what is read next cannot change before it is written.
		if _, err := tx.Exec(`DELETE FROM pause_bounces WHERE 0`); err != nil {
			return fail(err)
		}
		if s.afterPauseLock != nil {
			s.afterPauseLock()
		}
		if paused, err := hasPauseCauseIn(tx, plan.SessionID, team.PauseCauseQuota); err != nil {
			return fail(err)
		} else if paused {
			return team.Approval{}, team.RelayOp{}, PauseAlready, nil
		}
		if _, err := tx.Exec(`SAVEPOINT open_relay`); err != nil {
			return fail(err)
		}
	}
	// The first statement of the plain begin is a write, so SQLite takes the write lock at once.
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
		if plan != nil && errors.Is(err, ErrQuotaExhausted) {
			if _, err := tx.Exec(`ROLLBACK TO open_relay`); err != nil {
				return fail(err)
			}
			if err := s.writePauseIn(tx, *plan); err != nil {
				return fail(err)
			}
			if err := tx.Commit(); err != nil {
				return fail(fmt.Errorf("commit: %w", err))
			}
			return team.Approval{}, team.RelayOp{}, PausePaused, nil
		}
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
	if then != nil {
		if err := then(tx); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	s.notifyOp(claimed.ID)
	return after, claimed, PauseNone, nil
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

// decisionKinds is the kinds the unattended log covers (team.DecisionLogKind), as query arguments.
var decisionKinds = []team.Kind{team.KindLead, team.KindAdopt, team.KindSelfRelay, team.KindMemberRelay, team.KindSpawnResources}

// ListRecentDecisions is the newest limit decisions of the team approvals for the unattended log, whoever decided: approved,
// denied and timeout rows of the log's kinds, and the lead / adopt requests the daemon refused while the switch was on
// (cancelled with close_reason unattended_refused, which team.DecisionLogEntry lists as the denial it is). A withdrawn or
// abandoned request, an open one and the chat prompts are not decisions. Newest first by the time the log shows: a timeout's
// deadline_at, decided_at for the rest; the larger id first within one millisecond. Each state is read on its own index range
// (state, decided_at / deadline_at) cut at limit, then merged, so the cost does not grow with the table. Never nil.
func (s *Store) ListRecentDecisions(limit int) ([]team.Approval, error) {
	if s.beforeListRecentDecisions != nil {
		if err := s.beforeListRecentDecisions(); err != nil {
			return nil, fmt.Errorf("list recent decisions: %w", err)
		}
	}
	if limit < 1 {
		return []team.Approval{}, nil
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(decisionKinds)), ",")
	read := func(where, order string, args ...any) ([]team.Approval, error) {
		a := make([]any, 0, len(decisionKinds)+len(args)+1)
		for _, k := range decisionKinds {
			a = append(a, string(k))
		}
		a = append(a, args...)
		a = append(a, limit)
		q, err := s.db.Query(`SELECT `+selectCols+` FROM approval_requests WHERE kind IN (`+in+`) AND `+where+` ORDER BY `+order+` DESC, id DESC LIMIT ?`, a...)
		if err != nil {
			return nil, err
		}
		defer q.Close()
		var out []team.Approval
		for q.Next() {
			row, _, err := scanRow(q)
			if err != nil {
				return nil, err
			}
			out = append(out, row)
		}
		return out, q.Err()
	}
	all := []team.Approval{}
	for _, r := range []struct {
		where, order string
		args         []any
	}{
		{`state = ?`, `decided_at`, []any{string(team.StateApproved)}},
		{`state = ?`, `decided_at`, []any{string(team.StateDenied)}},
		{`state = ? AND close_reason = ? AND kind IN ('lead', 'adopt')`, `decided_at`, []any{string(team.StateCancelled), team.ErrUnattendedRefused}},
		{`state = ?`, `deadline_at`, []any{string(team.StateTimeout)}},
	} {
		rows, err := read(r.where, r.order, r.args...)
		if err != nil {
			return nil, fmt.Errorf("list recent decisions: %w", err)
		}
		all = append(all, rows...)
	}
	at := func(a team.Approval) int64 {
		if a.State == team.StateTimeout {
			return a.DeadlineAt
		}
		return a.DecidedAt
	}
	sort.SliceStable(all, func(i, j int) bool {
		if ai, aj := at(all[i]), at(all[j]); ai != aj {
			return ai > aj
		}
		return all[i].ID > all[j].ID
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
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
