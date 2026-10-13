package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ErrUnansweredMember: the card's session is a member of a team now, so its relay is the lead's and the card is not an ask that can
// go unanswered (the approve path cancels such a card, U13): nothing was written and the sweeper times the card out as before.
var ErrUnansweredMember = errors.New("the card's session is a member: it times out, it is not left unanswered")

// MarkUnanswered is the store half of markUnanswered, in ONE write transaction: the CAS on (id, open, unanswered_at 0, the deadline the
// caller saw) sets unanswered_at = now and the sentinel deadline; the session's relay_unanswered row (role lead when it leads a live
// team, else solo; members are not touched) and, only when that row is new, its notice and the phone's push. false (nothing written)
// when another close, mark or deadline change won.
func (s *Store) MarkUnanswered(a team.Approval, now int64) (bool, error) {
	fail := func(err error) (bool, error) { return false, fmt.Errorf("mark unanswered %s: %w", a.ID, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// The write lock first (a write that touches no row, as every transaction here that reads before it writes): the member read below
	// is then of the state this transaction commits, not of a snapshot another connection can outdate.
	if _, err := tx.Exec(`UPDATE approval_requests SET id = id WHERE id = ?`, a.ID); err != nil {
		return fail(err)
	}
	// A session that was taken in as a member since the card opened is not paused on its own account.
	if role, err := memberRoleIn(tx, a.Origin.SessionID); err != nil {
		return fail(err)
	} else if role.isMember() {
		return false, ErrUnansweredMember
	}
	res, err := tx.Exec(`UPDATE approval_requests SET unanswered_at = ?, deadline_at = ?
		WHERE id = ? AND state = 'open' AND unanswered_at = 0 AND deadline_at = ?`, now, team.UnansweredDeadlineMs, a.ID, a.DeadlineAt)
	if err != nil {
		return fail(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fail(err)
	} else if n != 1 {
		return false, nil
	}
	sid := a.Origin.SessionID
	teamID, _, err := s.liveLocalMembersIn(tx, sid)
	if err != nil {
		return fail(err)
	}
	role, leadSession := team.PauseRoleSolo, ""
	if teamID != "" {
		role, leadSession = team.PauseRoleLead, sid
	}
	ins, err := tx.Exec(`INSERT INTO session_pauses (session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?) ON CONFLICT (session_id, cause) DO NOTHING`,
		sid, team.PauseCauseUnanswered, role, teamID, leadSession, a.Origin.Ref, a.Origin.Title, now)
	if err != nil {
		return fail(err)
	}
	if n, err := ins.RowsAffected(); err != nil {
		return fail(err)
	} else if n == 1 {
		name := a.Origin.Title
		if name == "" {
			name = a.Origin.Ref
		}
		for _, o := range []struct{ kind, text string }{
			{team.PauseOutboxNotice, team.PauseNoticeUnanswered},
			{team.PauseOutboxPush, fmt.Sprintf(team.PausePushUnansweredFmt, name)},
		} {
			if _, err := tx.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`, sid, o.kind, o.text, now); err != nil {
				return fail(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return true, nil
}

// querier is what leaveUnansweredIn needs beyond dbtx: the reads of the bounce list.
type querier interface {
	dbtx
	Query(query string, args ...any) (*sql.Rows, error)
}

// leaveUnansweredIn runs inside closeRowIn when a close WON, on the closer's own transaction: a row that was unanswered takes the
// session's relay_unanswered pause with it, the resume notice of how the card ended (approved / denied / anything else) and the
// list of the messages bounced meanwhile. A session that still holds another cause stays paused and is told nothing yet. A row
// that was never unanswered costs one read.
func leaveUnansweredIn(ex dbtx, id string, c Close) error {
	var unanswered int64
	var sid string
	switch err := ex.QueryRow(`SELECT unanswered_at, origin_session_id FROM approval_requests WHERE id = ?`, id).Scan(&unanswered, &sid); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("read unanswered of %s: %w", id, err)
	}
	if unanswered == 0 {
		return nil
	}
	text := team.PauseResumedUnansweredEnded
	switch c.State {
	case team.StateApproved:
		text = team.PauseResumedUnansweredApproved
	case team.StateDenied:
		text = team.PauseResumedUnansweredDenied
	}
	return endUnansweredPauseIn(ex, sid, text, c.DecidedAt)
}

// endUnansweredPauseIn takes sid's relay_unanswered pause away on the closer's own transaction and, when no other cause stands,
// tells the session text and lists the messages bounced meanwhile (and drops them). A card's close (leaveUnansweredIn) and a member
// ask's closes (RT-2b T23) both end here. The pause already gone (a release took it) tells nothing.
func endUnansweredPauseIn(ex dbtx, sid, text string, at int64) error {
	q, ok := ex.(querier)
	if !ok {
		return fmt.Errorf("end unanswered pause of %s: not on a transaction", sid)
	}
	res, err := ex.Exec(`DELETE FROM session_pauses WHERE session_id = ? AND cause = ?`, sid, team.PauseCauseUnanswered)
	if err != nil {
		return fmt.Errorf("end unanswered pause of %s: %w", sid, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	var left int
	if err := ex.QueryRow(`SELECT COUNT(*) FROM session_pauses WHERE session_id = ?`, sid).Scan(&left); err != nil {
		return fmt.Errorf("end unanswered pause of %s: %w", sid, err)
	}
	if left > 0 {
		return nil // another cause stands: not resumed, told nothing, its bounces kept
	}
	outbox := func(t string) error {
		if _, err := ex.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`, sid, team.PauseOutboxNotice, t, at); err != nil {
			return fmt.Errorf("end unanswered pause of %s: outbox: %w", sid, err)
		}
		return nil
	}
	if err := outbox(text); err != nil {
		return err
	}
	list, err := bounceListIn(q, sid)
	if err != nil {
		return fmt.Errorf("end unanswered pause of %s: %w", sid, err)
	}
	if list != "" {
		if err := outbox(list); err != nil {
			return err
		}
		if _, err := ex.Exec(`DELETE FROM pause_bounces WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("end unanswered pause of %s: %w", sid, err)
		}
	}
	return nil
}

// OpenUnansweredCardOf is the id of sid's open card that stopped it (unanswered_at set), or "" when it has none.
func (s *Store) OpenUnansweredCardOf(sid string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM approval_requests WHERE origin_session_id = ? AND state = 'open' AND unanswered_at > 0
		ORDER BY created_at DESC LIMIT 1`, sid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open unanswered card of %s: %w", sid, err)
	}
	return id, nil
}
