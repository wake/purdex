package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// Adopt in the store (adopt plan PL-1b, adopt spec D-U24-2): the approve of an `adopt` request
// re-checks every refusal of its creation under the write lock and, when none applies, takes the
// session in as an `adopted` member in the same transaction. The registry is the one thing the
// store cannot read: the caller tells it (adoptCheck) just before the transaction.

// adoptCheck is what only the caller can say, read just before the transaction (decision 4): the
// host this daemon is, and whether the registry still lists the target session as alive.
type adoptCheck struct {
	HostID     string
	TargetLive bool
}

// adoptSeatsUsed counts the places a team holds (seatsTakenSQL, the spawn cap check's count).
func adoptSeatsUsed(q dbtx, teamID string) (used, limit int, err error) {
	err = q.QueryRow(`SELECT json_extract(grant_json, '$.max_members') FROM teams WHERE id = ?`, teamID).Scan(&limit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("seats of team %s: %w", teamID, err)
	}
	if used, err = seatsTaken(q, teamID, ""); err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// SeatsUsed is a team's places used and its limit, for the create-time check (PL-1c) outside a
// transaction. An unknown team is 0, 0.
func (s *Store) SeatsUsed(teamID string) (used, limit int, err error) {
	return adoptSeatsUsed(s.db, teamID)
}

// OpenAdoptForTarget is the open adopt request for the target session, if any: at most one may be open
// per target (the create invariant).
func (s *Store) OpenAdoptForTarget(targetSessionID string) (team.Approval, bool, error) {
	return s.OpenAdoptForTargetOn("", targetSessionID)
}

// OpenAdoptForTargetOn is OpenAdoptForTarget for the session of the host targetHostID ("" = a session of the lead's own
// host): a session id is only meaningful together with its host, so another host announcing the same id cannot block it.
func (s *Store) OpenAdoptForTargetOn(targetHostID, targetSessionID string) (team.Approval, bool, error) {
	a, _, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests
		WHERE kind = ? AND state = 'open' AND json_extract(payload_json, '$.target_session_id') = ?
		AND COALESCE(json_extract(payload_json, '$.target_host_id'), '') = ?
		ORDER BY created_at, id LIMIT 1`, string(team.KindAdopt), targetSessionID, targetHostID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, false, nil
	}
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("open adopt for %s: %w", targetSessionID, err)
	}
	return a, true, nil
}

// adoptRefusal re-checks, on tx under the write lock, every refusal of D-U24-2 in the create order and
// answers the first that applies ("" when none does).
func adoptRefusal(tx *sql.Tx, id, rowHostID string, p team.AdoptPayload, chk adoptCheck) (string, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM teams WHERE id = ? AND lead_session_id = ? AND ended_at = 0`, p.TeamID, p.LeadSessionID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return team.ErrNotLead, nil
	case err != nil:
		return "", fmt.Errorf("re-check lead: %w", err)
	}
	remote := p.TargetHostID != ""
	switch {
	case !remote && rowHostID != chk.HostID, remote && p.TargetHostID == chk.HostID:
		return team.ErrRemoteUnsupported, nil
	case !chk.TargetLive:
		return team.ErrAdoptTargetNotFound, nil
	case !remote && p.TargetSessionID == p.LeadSessionID: // another host's session is never the lead, whatever id it announces
		return team.ErrAdoptSelf, nil
	}
	if remote {
		// A session on another host: it cannot lead a team here, and it is a member only through a live row of THAT host
		// (the member host decides the rest when it applies the command).
		err = tx.QueryRow(`SELECT 1 FROM team_members WHERE host_id = ? AND session_id = ? AND state IN `+liveRemoteStates, p.TargetHostID, p.TargetSessionID).Scan(&one)
		switch {
		case err == nil:
			return team.ErrAdoptAlreadyMember, nil
		case !errors.Is(err, sql.ErrNoRows):
			return "", fmt.Errorf("re-check remote member: %w", err)
		}
	} else {
		err = tx.QueryRow(`SELECT 1 FROM teams WHERE lead_session_id = ? AND ended_at = 0`, p.TargetSessionID).Scan(&one)
		switch {
		case err == nil:
			return team.ErrAdoptTargetIsLead, nil
		case !errors.Is(err, sql.ErrNoRows):
			return "", fmt.Errorf("re-check target lead: %w", err)
		}
		member, err := isLiveMemberIn(tx, p.TargetSessionID)
		if err != nil {
			return "", err
		}
		if member {
			return team.ErrAdoptAlreadyMember, nil
		}
	}
	err = tx.QueryRow(`SELECT 1 FROM approval_requests WHERE kind = ? AND state = 'open' AND id <> ?
		AND json_extract(payload_json, '$.target_session_id') = ? AND COALESCE(json_extract(payload_json, '$.target_host_id'), '') = ?`,
		string(team.KindAdopt), id, p.TargetSessionID, p.TargetHostID).Scan(&one)
	switch {
	case err == nil:
		return team.ErrRequestOpen, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("re-check open adopt: %w", err)
	}
	used, limit, err := adoptSeatsUsed(tx, p.TeamID)
	if err != nil {
		return "", err
	}
	if used >= limit {
		return team.ErrTeamFull, nil
	}
	return "", nil
}

// adoptApprovedIn is the approve of an `adopt` row on the caller's transaction (a click through
// CloseAdoptApproved; the daemon's own approve at create, PL-1c, on its own): the write lock first,
// every refusal again, and then either
//   - a refusal: the row closes cancelled with the code as its close_reason (refused = the code), or
//   - the approve: an `active` row of the target in an ENDED team becomes `released` (its place is
//     long gone and it would break team_members_one_member), the row closes as c, and m is inserted.
//
// n is the close's RowsAffected: 0 means another writer closed the row first and nothing was written.
// m must be the adopted member the request describes (key = the request id, origin adopted, active,
// the `adopted` notice owed); anything else is an error.
func adoptApprovedIn(tx *sql.Tx, id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow, cmd *Command) (n int64, refused string, err error) {
	if c.State != team.StateApproved {
		return 0, "", fmt.Errorf("adopt approve of %s needs an approved close (state %q)", id, c.State)
	}
	// A remote target (spec §4.3): the approval is the user's consent, so the row is `joining` on the member host, no
	// notice is owed from here (the member host tells its session), and the adopt command is enqueued in this transaction.
	remote := p.TargetHostID != ""
	wantState, wantNotice := team.MemberActive, team.NoticeAdopted
	if remote {
		wantState, wantNotice = team.MemberJoining, ""
	}
	if m.SpawnOp != id || m.Origin != team.MemberOriginAdopted || m.State != wantState || m.TeamID != p.TeamID ||
		m.SessionID != p.TargetSessionID || m.NoticePending != wantNotice || (remote && m.HostID != p.TargetHostID) ||
		(remote != (cmd != nil)) || (cmd != nil && (cmd.ID != id || cmd.Kind != CmdAdopt || cmd.MK != id || cmd.HostID != p.TargetHostID || cmd.TeamID != p.TeamID)) {
		return 0, "", fmt.Errorf("adopt member row of %s does not describe the request (key %q, origin %q, state %q, team %q, session %q, notice %q)",
			id, m.SpawnOp, m.Origin, m.State, m.TeamID, m.SessionID, m.NoticePending)
	}
	// A write first, so SQLite takes the write lock before the reads below (as closeSelfRelayApprovedIn).
	if _, err = tx.Exec(`UPDATE approval_requests SET id = id WHERE id = ?`, id); err != nil {
		return 0, "", fmt.Errorf("lock adopt %s: %w", id, err)
	}
	// The caller's payload must be the stored one: every re-check, the retirement and the insert below act on
	// p, and the approval is about what the row says (a wrong or stale p would put another session in another team).
	var rowHostID, kind, payloadJSON, originSession string
	if err = tx.QueryRow(`SELECT host_id, kind, payload_json, origin_session_id FROM approval_requests WHERE id = ?`, id).
		Scan(&rowHostID, &kind, &payloadJSON, &originSession); err != nil {
		return 0, "", fmt.Errorf("read adopt %s: %w", id, err)
	}
	stored, perr := team.AdoptPayloadOf(team.Approval{ID: id, Kind: team.Kind(kind), Payload: json.RawMessage(payloadJSON)})
	if perr != nil {
		return 0, "", fmt.Errorf("stored adopt %s: %w", id, perr)
	}
	if stored != p || originSession != p.LeadSessionID {
		return 0, "", fmt.Errorf("adopt %s: the payload given is not the stored one (or its lead is not the request's origin)", id)
	}
	if refused, err = adoptRefusal(tx, id, rowHostID, p, chk); err != nil {
		return 0, "", err
	}
	if refused != "" {
		n, err = closeRowIn(tx, id, Close{State: team.StateCancelled, DecidedAt: c.DecidedAt, UnexpiredAt: c.UnexpiredAt, Reason: refused}, "", 0)
		if err != nil || n == 0 {
			return 0, "", err // lost the CAS: someone else's close stands, and nothing is ours to report
		}
		return n, refused, nil
	}
	if n, err = closeRowIn(tx, id, c, "", 0); err != nil || n == 0 {
		return 0, "", err
	}
	if !remote {
		if _, err = tx.Exec(`UPDATE team_members SET state = ?, ended_at = ?, updated_at = ?
			WHERE session_id = ? AND state = 'active'`, string(team.MemberReleased), c.DecidedAt, c.DecidedAt, p.TargetSessionID); err != nil {
			return 0, "", fmt.Errorf("retire the target's row of an ended team: %w", err)
		}
	}
	inserted, err := insertMemberRowIn(context.Background(), tx, m)
	if err != nil {
		return 0, "", err
	}
	if !inserted { // the key is taken: committing the close without the member would report an adoption that did not happen
		return 0, "", fmt.Errorf("adopt member key %s is already a team_members row", id)
	}
	if cmd != nil {
		if err := enqueueCommandIn(tx, *cmd, c.DecidedAt); err != nil {
			return 0, "", err // the row does not exist without its command
		}
	}
	return n, "", nil
}

// CloseAdoptApproved is adoptApprovedIn in its own transaction: the click's approve of an adopt request.
// a is the row after the attempt (the winner's close for a loser too); won says this call closed it;
// refused is the code the request was cancelled with, when a re-check failed.
func (s *Store) CloseAdoptApproved(id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow) (a team.Approval, won bool, refused string, err error) {
	return s.CloseAdoptApprovedWith(id, c, p, chk, m, nil)
}

// CloseAdoptApprovedWith is CloseAdoptApproved with the adopt command of a remote target (nil for a same-host one), enqueued
// in the same transaction.
func (s *Store) CloseAdoptApprovedWith(id string, c Close, p team.AdoptPayload, chk adoptCheck, m memberRow, cmd *Command) (a team.Approval, won bool, refused string, err error) {
	fail := func(err error) (team.Approval, bool, string, error) {
		return team.Approval{}, false, "", fmt.Errorf("approve adopt %s: %w", id, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	n, refused, err := adoptApprovedIn(tx, id, c, p, chk, m, cmd)
	if err != nil {
		return fail(err)
	}
	a, _, err = getRowIn(tx, id)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return a, n == 1, refused, nil
}
