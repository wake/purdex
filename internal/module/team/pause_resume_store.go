// internal/module/team/pause_resume_store.go — leaving a pause: the resume group (unattended quota pause spec 2026-10-12 §3.4, UP-3a).
package teammod

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/team"
)

// ResumeScope picks the pause rows a resume group takes: every row (the switch went off), or only those written in another on-period
// than OnSinceNot (the reconciliation while the switch is on).
type ResumeScope struct {
	All        bool
	OnSinceNot int64
	// Release picks the rows a person releases (UP-3b): every row of Release, and the `lead`-cause rows of the members it paused with
	// it. A member's own quota row stays. The solo / lead notice is the release text, not the switch's.
	Release string
}

func (sc ResumeScope) where() (string, []any) {
	switch {
	case sc.Release != "":
		return "(session_id = ? OR (cause = 'lead' AND lead_session_id = ?))", []any{sc.Release, sc.Release}
	case sc.All:
		return "1", nil
	}
	return "on_since <> ?", []any{sc.OnSinceNot}
}

// ResumeResult says what a resume group did: the pause rows deleted and the sessions that are no longer paused at all.
type ResumeResult struct {
	Rows, Sessions int
}

// maxBounceListLines caps the lines of one bounce list; the rest is counted in the last line.
const maxBounceListLines = 20

// ResumePauses is the resume group (spec §3.4), in ONE write transaction: delete the rows scope picks; then, for each session whose
// LAST cause is now gone, enqueue in this order its resume notice, its bounce list (and delete its bounces) and — for a lead or solo
// `quota` pause whose mod speaks the reask protocol (newMod) — a release token and the `reask` control. A session that still has
// another cause is told nothing and keeps its bounces. The rows of a lead or a solo session are enqueued before those of a member
// (the outbox's FIFO is the order); a member paused for its own quota is told, and each lead gets one notice listing the members it
// had paused that way, after all the sessions' own. A mod that does not speak the protocol gets the fallback text and no token.
func (s *Store) ResumePauses(scope ResumeScope, at int64, newMod func(sessionID string) bool, newToken func() string) (ResumeResult, error) {
	fail := func(err error) (ResumeResult, error) { return ResumeResult{}, fmt.Errorf("resume pauses: %w", err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// The writer lock first (a write that touches no row): the pause verdict takes it before it bounces, so a message is either
	// bounced and listed here or delivered, never bounced with no one left to list it.
	if _, err := tx.Exec(`DELETE FROM pause_bounces WHERE 0`); err != nil {
		return fail(err)
	}
	cond, args := scope.where()
	rows, err := tx.Query(`SELECT session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since FROM session_pauses WHERE `+cond+`
		ORDER BY (role = 'member'), since, session_id, cause`, args...)
	if err != nil {
		return fail(err)
	}
	var taken []PauseRow
	for rows.Next() {
		var p PauseRow
		if err := rows.Scan(&p.SessionID, &p.Cause, &p.Role, &p.TeamID, &p.LeadSessionID, &p.Ref, &p.Title, &p.OnSince, &p.Since); err != nil {
			rows.Close()
			return fail(err)
		}
		taken = append(taken, p)
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	if len(taken) == 0 {
		return ResumeResult{}, nil
	}
	if _, err := tx.Exec(`DELETE FROM session_pauses WHERE `+cond, args...); err != nil {
		return fail(err)
	}
	var order []string // the sessions in the order their first row came
	bySession := map[string][]PauseRow{}
	for _, p := range taken {
		if _, ok := bySession[p.SessionID]; !ok {
			order = append(order, p.SessionID)
		}
		bySession[p.SessionID] = append(bySession[p.SessionID], p)
	}
	outbox := func(sid, kind, text, reaskID string) error {
		if _, err := tx.Exec(`INSERT INTO pause_outbox (session_id, kind, text, reask_id, created_at) VALUES (?, ?, ?, ?, ?)`, sid, kind, text, reaskID, at); err != nil {
			return fmt.Errorf("pause outbox %s: %w", sid, err)
		}
		return nil
	}
	var leadOrder []string
	lists := map[string][]string{} // lead session → the members it had paused for their own quota
	var res ResumeResult
	res.Rows = len(taken)
	for _, sid := range order {
		var left int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM session_pauses WHERE session_id = ?`, sid).Scan(&left); err != nil {
			return fail(err)
		}
		if left > 0 {
			continue // another cause stands: not resumed, told nothing
		}
		res.Sessions++
		ps := bySession[sid]
		quota := false
		for _, p := range ps {
			quota = quota || p.Cause == team.PauseCauseQuota
		}
		first := ps[0]
		reask := false
		var text string
		switch {
		case first.Role == team.PauseRoleMember && quota:
			text = team.PauseResumedMemberQuota
			name := first.Ref
			if first.Title != "" {
				name = first.Title + "（" + first.Ref + "）"
			}
			if first.LeadSessionID != "" {
				if _, ok := lists[first.LeadSessionID]; !ok {
					leadOrder = append(leadOrder, first.LeadSessionID)
				}
				lists[first.LeadSessionID] = append(lists[first.LeadSessionID], name)
			}
		case first.Role == team.PauseRoleMember:
			text = team.PauseResumedMemberLead
		default:
			reask = quota && newMod != nil && newMod(sid)
			text = team.PauseResumedSoloOldMod
			if reask {
				text = team.PauseResumedSolo
				if scope.Release != "" {
					text = team.PauseReleasedSolo
				}
			}
		}
		if err := outbox(sid, team.PauseOutboxNotice, text, ""); err != nil {
			return fail(err)
		}
		list, err := bounceListIn(tx, sid)
		if err != nil {
			return fail(err)
		}
		if list != "" {
			if err := outbox(sid, team.PauseOutboxNotice, list, ""); err != nil {
				return fail(err)
			}
			if _, err := tx.Exec(`DELETE FROM pause_bounces WHERE session_id = ?`, sid); err != nil {
				return fail(err)
			}
		}
		if reask {
			token := newToken()
			if _, err := tx.Exec(`INSERT INTO pause_release_tokens (token, session_id, created_at) VALUES (?, ?, ?)`, token, sid, at); err != nil {
				return fail(err)
			}
			if err := outbox(sid, team.PauseOutboxReask, team.PauseReaskControlPrefix+token, token); err != nil {
				return fail(err)
			}
		}
	}
	for _, lead := range leadOrder {
		if err := outbox(lead, team.PauseOutboxNotice, fmt.Sprintf(team.PauseMemberQuotaListFmt, strings.Join(lists[lead], "、")), ""); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return res, nil
}

// bounceListIn is the text that lists the messages bounced to sid while it was paused, oldest first: "" when there were none.
func bounceListIn(tx *sql.Tx, sid string) (string, error) {
	rows, err := tx.Query(`SELECT from_addr, at, first_line FROM pause_bounces WHERE session_id = ? ORDER BY at, from_addr, text_hash`, sid)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var from, first string
		var at int64
		if err := rows.Scan(&from, &at, &first); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s %s：%s", time.UnixMilli(at).Format("15:04"), from, first))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	n := len(lines)
	if n > maxBounceListLines {
		lines = append(lines[:maxBounceListLines], fmt.Sprintf("…另 %d 則", n-maxBounceListLines))
	}
	return fmt.Sprintf(team.PauseBouncedFmt, n) + "\n" + strings.Join(lines, "\n"), nil
}

// FinishReaskOutbox is the outbox's delete of a reask row that has been sent: in ONE transaction the token's 24 h restart at `at`
// (a token lives a day from its delivery, not from the transaction that wrote it) and the row goes. If the token cannot be
// updated the row stays and is sent again (the mod drops a repeated id), so a delivered reask never has a token the next
// reconciliation would prune.
func (s *Store) FinishReaskOutbox(seq int64, token string, at int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("finish reask %d: %w", seq, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE pause_release_tokens SET created_at = ? WHERE token = ?`, at, token); err != nil {
		return fmt.Errorf("finish reask %d: touch token: %w", seq, err)
	}
	if _, err := tx.Exec(`DELETE FROM pause_outbox WHERE seq = ?`, seq); err != nil {
		return fmt.Errorf("finish reask %d: %w", seq, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish reask %d: %w", seq, err)
	}
	return nil
}

// PruneReleaseTokens deletes the release tokens written before `before` (unix ms), except those a reask still waiting in the
// outbox refers to: a daemon that was down (or an outbox blocked) for a day must not send a control whose token is gone.
func (s *Store) PruneReleaseTokens(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM pause_release_tokens WHERE created_at < ?
		AND token NOT IN (SELECT reask_id FROM pause_outbox WHERE reask_id <> '')`, before)
	if err != nil {
		return 0, fmt.Errorf("prune release tokens: %w", err)
	}
	return res.RowsAffected()
}
