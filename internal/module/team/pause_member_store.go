// internal/module/team/pause_member_store.go — a member's own quota pause and the pause of a held card (unattended quota pause spec
// 2026-10-12 §3.3, UP-2b).
package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// errMemberPaused / errMemberPausedAlready are what a MemberRelayGate returns when the lead's relay of a member found no pool left while
// the host is unattended: the member's `quota` pause was just written on the gate's transaction (errMemberPaused, which the create
// COMMITS, with no op), or the member already held it (errMemberPausedAlready, nothing written, rolled back).
var (
	errMemberPaused        = errors.New("member paused: no relay pool left")
	errMemberPausedAlready = errors.New("member already paused for quota")
)

// MemberPausePlan is a member's own `quota` pause: the member as it is now (ref and title recorded on the row), its team and lead, the
// unattended switch's Since and the time.
type MemberPausePlan struct {
	SessionID, Ref, Title, TeamID, LeadSessionID string
	OnSince, At                                  int64
}

// writeMemberPauseIn is a member's pause on tx: its `quota` row (role member) and the one notice that tells it. No push (spec §3.3:
// the phone hears about a lead or a solo session, never a member). It reports whether the row was new; nothing is written when the
// member already holds it, so a repeat is never told twice.
func writeMemberPauseIn(tx *sql.Tx, p MemberPausePlan) (bool, error) {
	res, err := tx.Exec(`INSERT INTO session_pauses (session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (session_id, cause) DO NOTHING`,
		p.SessionID, team.PauseCauseQuota, team.PauseRoleMember, p.TeamID, p.LeadSessionID, p.Ref, p.Title, p.OnSince, p.At)
	if err != nil {
		return false, fmt.Errorf("pause %s/%s: %w", p.SessionID, team.PauseCauseQuota, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`,
		p.SessionID, team.PauseOutboxNotice, team.PauseNoticeMemberQuota, p.At); err != nil {
		return false, fmt.Errorf("pause outbox %s: %w", p.SessionID, err)
	}
	return true, nil
}

// PauseHeldResult says what PauseHeldRow did.
type PauseHeldResult int

const (
	PauseHeldLost   PauseHeldResult = iota // the row was not open, or overdue, or its member is gone: nothing written
	PauseHeldPaused                        // the row and its op were cancelled and the pause written
)

// PauseHeldRow is the sweep's pause of a held card (spec §3.3), in ONE write transaction: CAS the row (still open and not overdue at
// `at`) to cancelled{paused_no_quota}; its linked op awaiting_approval → cancelled{paused_no_quota}; then the pause of the session the
// card was for — a self relay's origin (a lead takes its live local members along, and the phone is told) or a member relay's member
// (its own `quota` row only) — with its outbox rows. A decide that closed the row first, a timeout, or a member that has left makes the
// CAS or the re-check fail: PauseHeldLost, nothing written. A session that already holds the quota pause only has the card cancelled
// (nothing new is written for it). The caller kicks the outbox, announces the closed row and publishes the roster.
func (s *Store) PauseHeldRow(id string, onSince, at int64) (team.Approval, PauseHeldResult, error) {
	fail := func(err error) (team.Approval, PauseHeldResult, error) {
		return team.Approval{}, PauseHeldLost, fmt.Errorf("pause held row %s: %w", id, err)
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
	if err != nil || row.State != team.StateOpen || (row.Kind != team.KindSelfRelay && row.Kind != team.KindMemberRelay) {
		return row, PauseHeldLost, err
	}
	op, hasOp, err := opOfRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	member := MemberPausePlan{OnSince: onSince, At: at}
	subject := row.Origin.SessionID
	if row.Kind == team.KindMemberRelay {
		if !hasOp || (s.localHostID != "" && op.HostID != s.localHostID) {
			// A member that lives on another host cannot be paused from here (its notice and its message gate are that host's): its
			// card stays a card for a person.
			return row, PauseHeldLost, nil
		}
		if reason, err := recheckMemberRelayIn(tx, op); err != nil {
			return fail(err)
		} else if reason != "" {
			return row, PauseHeldLost, nil // the member or its team is gone: the boot's re-check cancels it, nobody is paused
		}
		member.SessionID, member.Ref, member.TeamID, member.LeadSessionID = op.SessionID, op.Ref, op.TeamID, row.Origin.SessionID
		if err := tx.QueryRow(`SELECT title FROM team_members WHERE session_id = ? AND team_id = ? AND state = 'active'`, op.SessionID, op.TeamID).Scan(&member.Title); err != nil {
			return fail(err)
		}
		subject = member.SessionID
	}
	n, err := closeRowIn(tx, id, Close{State: team.StateCancelled, DecidedAt: at, Auto: true, UnexpiredAt: at, Reason: team.RelayReasonPausedNoQuota}, "", 0)
	if err != nil {
		return fail(err)
	}
	if n != 1 {
		return row, PauseHeldLost, nil
	}
	if hasOp && op.State == team.RelayAwaitingApproval {
		if _, res, err := reportRelayIn(tx, op.ID, RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonPausedNoQuota, At: at}); err != nil || res != ReportApplied {
			if err == nil {
				err = fmt.Errorf("op %s did not take cancelled (%v)", op.ID, res)
			}
			return fail(err)
		}
	}
	if paused, err := hasPauseCauseIn(tx, subject, team.PauseCauseQuota); err != nil {
		return fail(err)
	} else if !paused {
		if row.Kind == team.KindMemberRelay {
			_, err = writeMemberPauseIn(tx, member)
		} else {
			err = s.writePauseIn(tx, PausePlan{SessionID: row.Origin.SessionID, Ref: row.Origin.Ref, Title: row.Origin.Title, OnSince: onSince, At: at})
		}
		if err != nil {
			return fail(err)
		}
	}
	cancelled, _, err := getRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	s.notifyOp(op.ID)
	return cancelled, PauseHeldPaused, nil
}
