package teammod

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// A member's FINAL ask (used ≥ RelayLastAskPct) that its lead lets lapse does not expire: the member stops, paused with cause
// relay_unanswered, until the lead answers - `pdx relay _ref` (accept) or `pdx relay _ref --deny` - or the ask ends (RT-2b T23).

// unansweredAsk is one ask MarkFinalAsksUnanswered marked, with the member it paused.
type unansweredAsk struct {
	Ask    RelayAsk
	Member memberRow
	Team   team.Team
}

// MarkFinalAsksUnanswered marks every open final ask of a member of this host whose window has passed at now, once: unanswered_at =
// now, expires_at = the far sentinel, and in the SAME transaction the member's relay_unanswered row (role member) and its notice.
// It returns the asks it marked, for the lead's notice.
func (s *Store) MarkFinalAsksUnanswered(now int64) ([]unansweredAsk, error) {
	fail := func(err error) ([]unansweredAsk, error) {
		return nil, fmt.Errorf("mark final relay asks unanswered: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// the write lock first, as every transaction here that reads before it writes
	if _, err := tx.Exec(`UPDATE relay_asks SET id = id WHERE state = 'open' AND unanswered_at = 0 AND expires_at <= ? AND used_pct >= ?`, now, team.RelayLastAskPct); err != nil {
		return fail(err)
	}
	loc, locArgs := s.local("m.host_id")
	rows, err := tx.Query(`SELECT `+qualify("a", relayAskCols)+` FROM relay_asks a
		WHERE a.state = 'open' AND a.unanswered_at = 0 AND a.expires_at <= ? AND a.used_pct >= ?
		AND EXISTS (SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.spawn_op = a.spawn_op AND m.session_id = a.session_id AND m.state = 'active' AND t.ended_at = 0 AND `+loc+`)
		ORDER BY a.created_at, a.id`, append([]any{now, team.RelayLastAskPct}, locArgs...)...)
	if err != nil {
		return fail(err)
	}
	var asks []RelayAsk
	for rows.Next() {
		a, err := scanRelayAsk(rows)
		if err != nil {
			rows.Close()
			return fail(err)
		}
		asks = append(asks, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fail(err)
	}
	rows.Close()
	var out []unansweredAsk
	for _, a := range asks {
		res, err := tx.Exec(`UPDATE relay_asks SET unanswered_at = ?, expires_at = ? WHERE id = ? AND state = 'open' AND unanswered_at = 0 AND expires_at <= ?`,
			now, team.UnansweredDeadlineMs, a.ID, now)
		if err != nil {
			return fail(err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}
		var mr memberRow
		var t team.Team
		var grantJSON string
		err = tx.QueryRow(`SELECT `+qualify("m", memberCols)+`, `+qualify("t", teamCols)+`
			FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.spawn_op = ? AND m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, a.SpawnOp, a.SessionID).
			Scan(append(mr.dest(), teamDest(&t, &grantJSON)...)...)
		if err == nil {
			err = decodeTeamGrant(&t, grantJSON)
		}
		if err != nil {
			return fail(err)
		}
		ins, err := tx.Exec(`INSERT INTO session_pauses (session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?) ON CONFLICT (session_id, cause) DO NOTHING`,
			a.SessionID, team.PauseCauseUnanswered, team.PauseRoleMember, t.ID, t.LeadSessionID, mr.Ref, mr.Title, now)
		if err != nil {
			return fail(err)
		}
		if n, _ := ins.RowsAffected(); n == 1 {
			if _, err := tx.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`,
				a.SessionID, team.PauseOutboxNotice, team.PauseNoticeUnansweredMember, now); err != nil {
				return fail(err)
			}
		}
		a.UnansweredAt, a.ExpiresAt = now, team.UnansweredDeadlineMs
		out = append(out, unansweredAsk{Ask: a, Member: mr, Team: t})
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return out, nil
}

// leaveAskAcceptedIn ends the unanswered pause of a member whose open ask the lead's relay is accepting, in the op's transaction:
// called with the ask row's unanswered_at read BEFORE the accept's UPDATE.
func leaveAskAcceptedIn(tx *sql.Tx, sessionID string, now int64) error {
	return endUnansweredPauseIn(tx, sessionID, team.PauseResumedMemberAccepted, now)
}

// markFinalAsks is the sweeper's step (liveness tick) after the expiry: the final asks that lapsed pause their members, and each
// lead is told. Caller holds no lock; the store call is one transaction.
func (m *Module) markFinalAsks() {
	if !m.memberAutoOn() { // relay.member_auto off: a final ask that lapses stays open and nobody is paused (ExpireRelayAsks leaves it be)
		return
	}
	m.askMu.Lock()
	marked, err := m.store.MarkFinalAsksUnanswered(m.now())
	m.askMu.Unlock()
	if err != nil {
		m.logf("[team] sweep final relay asks: %v", err)
		return
	}
	if len(marked) == 0 {
		return
	}
	m.rosterChanged()
	m.kickPauseOutbox()
	for _, u := range marked {
		u := u
		m.logf("[team] final relay ask %s of member %s unanswered: the member is paused", u.Ask.ID, u.Member.Ref)
		address, title := m.memberNoticeName(u.Member)
		ref := strings.TrimPrefix(u.Member.Ref, "_")
		text := fmt.Sprintf(team.RelayAskUnansweredLeadFmt, address, ref, title, ref, ref)
		m.markFinalAsksNoticeFor(u, text)
	}
}

// askAnswered is what follows a commit that may have resumed a member its unanswered ask had paused: the roster and the outbox.
// Both are cheap when nothing changed.
func (m *Module) askAnswered() {
	m.rosterChanged()
	m.kickPauseOutbox()
}

// markFinalAsksNoticeFor tells the lead that u's member is paused, off the sweeper's path: behind a close of the ask (askMu), and only
// while the ask is still unanswered - a lead that accepted or denied between the mark and this send is not asked again (codex R1).
func (m *Module) markFinalAsksNoticeFor(u unansweredAsk, text string) {
	m.goTracked(func() {
		m.askMu.Lock()
		defer m.askMu.Unlock()
		if cur, ok, err := m.store.GetRelayAsk(u.Ask.ID); err != nil || !ok || cur.State != team.RelayAskOpen || cur.UnansweredAt == 0 {
			return
		}
		m.noticeToLead(u.Member, u.Team, text, "final relay ask unanswered")
	})
}
