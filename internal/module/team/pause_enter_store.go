// internal/module/team/pause_enter_store.go — entering a pause (unattended quota pause spec 2026-10-12 §3.3, UP-2a).
package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// PauseOutcome says what CreateSelfRelayOrPause did.
type PauseOutcome int

const (
	PauseNone    PauseOutcome = iota // a unit was spent and the op opened (or the call failed: see the error)
	PausePaused                      // no quota: the pause was written, no op
	PauseAlready                     // the session already holds its `quota` pause: nothing written
)

// PausePlan is what a pause needs beyond the begin itself: the session's identity as it is now (the ref and title recorded on
// the rows), the unattended switch's Since (on_since) and the time of the pause.
type PausePlan struct {
	SessionID, Ref, Title string
	OnSince, At           int64
}

func hasPauseCauseIn(q dbtx, sessionID, cause string) (bool, error) {
	var one int
	err := q.QueryRow(`SELECT 1 FROM session_pauses WHERE session_id = ? AND cause = ?`, sessionID, cause).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pause of %s: %w", sessionID, err)
	}
	return true, nil
}

// pauseMember is a live local member a lead's pause takes along.
type pauseMember struct{ SessionID, Ref, Title string }

// liveLocalMembersIn is LiveLocalMembersOfLead's predicate on a transaction: the `active` rows of the lead's live team that live
// on THIS host. It returns the team id as well ("" when the session leads no live team).
func (s *Store) liveLocalMembersIn(tx *sql.Tx, leadSessionID string) (teamID string, members []pauseMember, err error) {
	switch err := tx.QueryRow(`SELECT id FROM teams WHERE lead_session_id = ? AND ended_at = 0`, leadSessionID).Scan(&teamID); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil, nil
	case err != nil:
		return "", nil, fmt.Errorf("live team of %s: %w", leadSessionID, err)
	}
	rows, err := tx.Query(`SELECT m.session_id, m.ref, m.title FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE t.lead_session_id = ? AND m.state = 'active' AND t.ended_at = 0 AND (? = '' OR m.host_id = ?) ORDER BY m.created_at, m.spawn_op`,
		leadSessionID, s.localHostID, s.localHostID)
	if err != nil {
		return "", nil, fmt.Errorf("live members of %s: %w", leadSessionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var m pauseMember
		if err := rows.Scan(&m.SessionID, &m.Ref, &m.Title); err != nil {
			return "", nil, fmt.Errorf("live members of %s: %w", leadSessionID, err)
		}
		members = append(members, m)
	}
	return teamID, members, rows.Err()
}

// writePauseIn is the pause itself, on the begin's transaction: the session's `quota` row (a lead's role is `lead`, anything
// else `solo`); for a lead, a `lead` row for each live local member; and the outbox rows in the order they are sent — the
// session's notice, each newly paused member's notice, then the push. A member that already holds a `lead` row is not told again.
func (s *Store) writePauseIn(tx *sql.Tx, p PausePlan) error {
	teamID, members, err := s.liveLocalMembersIn(tx, p.SessionID)
	if err != nil {
		return err
	}
	role, leadSession := team.PauseRoleSolo, ""
	if teamID != "" {
		role, leadSession = team.PauseRoleLead, p.SessionID
	}
	insert := func(sid, cause, role, ref, title string) (bool, error) {
		res, err := tx.Exec(`INSERT INTO session_pauses (session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (session_id, cause) DO NOTHING`,
			sid, cause, role, teamID, leadSession, ref, title, p.OnSince, p.At)
		if err != nil {
			return false, fmt.Errorf("pause %s/%s: %w", sid, cause, err)
		}
		n, err := res.RowsAffected()
		return n == 1, err
	}
	outbox := func(sid, kind, text string) error {
		if _, err := tx.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`, sid, kind, text, p.At); err != nil {
			return fmt.Errorf("pause outbox %s: %w", sid, err)
		}
		return nil
	}
	if _, err := insert(p.SessionID, team.PauseCauseQuota, role, p.Ref, p.Title); err != nil {
		return err
	}
	if err := outbox(p.SessionID, team.PauseOutboxNotice, team.PauseNoticeSelf); err != nil {
		return err
	}
	for _, m := range members {
		created, err := insert(m.SessionID, team.PauseCauseLead, team.PauseRoleMember, m.Ref, m.Title)
		if err != nil {
			return err
		}
		if created {
			if err := outbox(m.SessionID, team.PauseOutboxNotice, team.PauseNoticeLeadPaused); err != nil {
				return err
			}
		}
	}
	// the phone is told about a lead or a solo session, never about a member (spec §3.3)
	name := p.Title
	if name == "" {
		name = p.Ref
	}
	text := name + " 已暫停：接力額度用完，等你接手"
	if len(members) > 0 {
		text += fmt.Sprintf("，含 %d 個 member", len(members))
	}
	return outbox(p.SessionID, team.PauseOutboxPush, text)
}
