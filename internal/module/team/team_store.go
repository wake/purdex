package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// ErrLeadHasTeam is returned by CloseLeadApproved when the request's origin
// already leads a live team (spec §6.2 already_lead): the partial unique
// index teams_one_live_per_lead refused the insert, and the close was
// rolled back with it, so the row is still open.
var ErrLeadHasTeam = errors.New("the session already leads a live team")

// teamSchema is the P4 teams table (spec §7.1). It is run by OpenStore
// after relaySchema; every statement is idempotent, so it is safe on a
// team.db written before it existed. A team's id is the id of the lead
// request that approved it (plan v3 deviation 1), so request_id = id.
// ended_at = 0 while the team is live; one live team per lead session.
const teamSchema = `
	CREATE TABLE IF NOT EXISTS teams (
		id              TEXT PRIMARY KEY,
		host_id         TEXT    NOT NULL,
		lead_session_id TEXT    NOT NULL,
		lead_ref        TEXT    NOT NULL,
		grant_json      TEXT    NOT NULL,
		request_id      TEXT    NOT NULL UNIQUE,
		created_at      INTEGER NOT NULL,
		ended_at        INTEGER NOT NULL DEFAULT 0,
		end_reason      TEXT    NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX IF NOT EXISTS teams_one_live_per_lead ON teams (lead_session_id) WHERE ended_at = 0;`

const teamCols = `id, host_id, lead_session_id, lead_ref, grant_json, request_id, created_at, ended_at, end_reason`

func scanTeam(r rowScanner) (team.Team, error) {
	var t team.Team
	var grantJSON string
	if err := r.Scan(&t.ID, &t.HostID, &t.LeadSessionID, &t.LeadRef, &grantJSON, &t.RequestID,
		&t.CreatedAt, &t.EndedAt, &t.EndReason); err != nil {
		return team.Team{}, err
	}
	if err := json.Unmarshal([]byte(grantJSON), &t.Grant); err != nil {
		return team.Team{}, fmt.Errorf("decode grant of team %s: %w", t.ID, err)
	}
	return t, nil
}

// CloseLeadApproved is the approve of a lead request (spec §6.2: "Approval
// creates the team (§7.1) in the same transaction"). In one transaction it
// runs the open CAS (closeRowIn) and, only when that changed the row,
// inserts t. Either both land or neither does: when the origin already
// leads a live team the insert violates teams_one_live_per_lead, both roll
// back and the error is ErrLeadHasTeam — the row stays open. Otherwise the
// returned row and won are what closeWhere returns: the row after the
// attempt (the winner's close, for a loser too) and whether this call won.
// c must be an approval with a grant, and t's id and request id must be
// the request's id; anything else is an error and writes nothing.
func (s *Store) CloseLeadApproved(id string, c Close, t team.Team) (team.Approval, bool, error) {
	fail := func(err error) (team.Approval, bool, error) {
		return team.Approval{}, false, fmt.Errorf("approve lead %s: %w", id, err)
	}
	if c.State != team.StateApproved || c.Grant == nil {
		return fail(fmt.Errorf("a team needs an approval with a grant (state %q, grant set %v)", c.State, c.Grant != nil))
	}
	if t.ID != id || t.RequestID != id {
		return fail(fmt.Errorf("team id %q and request id %q must both be the request's id", t.ID, t.RequestID))
	}
	grantJSON, err := json.Marshal(t.Grant)
	if err != nil {
		return fail(fmt.Errorf("encode grant: %w", err))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at once.
	n, err := closeRowIn(tx, id, c, "", 0)
	if err != nil {
		return fail(err)
	}
	if n == 1 {
		if _, err := tx.Exec(`INSERT INTO teams (`+teamCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')`,
			t.ID, t.HostID, t.LeadSessionID, t.LeadRef, string(grantJSON), t.RequestID, t.CreatedAt); err != nil {
			if strings.Contains(err.Error(), "teams.lead_session_id") { // the partial unique index teams_one_live_per_lead
				return fail(ErrLeadHasTeam)
			}
			return fail(fmt.Errorf("insert team: %w", err))
		}
	}
	a, _, err := getRowIn(tx, id) // ErrNoSuchApproval (wrapped) for an unknown id
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return a, n == 1, nil
}

// LiveTeamByLead returns the live team (ended_at = 0) the session leads, if
// any; an ended team does not count (spec §6.2 already_lead).
func (s *Store) LiveTeamByLead(sessionID string) (team.Team, bool, error) {
	t, err := scanTeam(s.db.QueryRow(`SELECT `+teamCols+` FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Team{}, false, nil
	}
	if err != nil {
		return team.Team{}, false, fmt.Errorf("live team by lead %s: %w", sessionID, err)
	}
	return t, true, nil
}

// ListLiveTeams returns every live team, oldest first. Never nil.
func (s *Store) ListLiveTeams() ([]team.Team, error) {
	rows, err := s.db.Query(`SELECT ` + teamCols + ` FROM teams WHERE ended_at = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list live teams: %w", err)
	}
	defer rows.Close()
	out := []team.Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("list live teams: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list live teams: %w", err)
	}
	return out, nil
}

// EndTeam ends a live team (spec §7.1) with reason at at, in one guarded
// UPDATE that leaves the team as it is when it already ended; when its
// lead is no longer leadSessionID, the lead the caller saw (a relay moved
// it since: P4-3's cleared transaction); or when that lead has a relay op
// in claimed, writing or written — a team never ends mid-relay (the old
// session id leaves the registry about 0.6 s after a relay's /clear while
// its op is still written). The relay guard is in the statement itself, so
// a relay claimed after the caller looked still wins. ended says whether
// this call ended it. Members are not touched (D4).
func (s *Store) EndTeam(id, leadSessionID, reason string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE teams SET ended_at = ?, end_reason = ?
		WHERE id = ? AND lead_session_id = ? AND ended_at = 0
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		at, reason, id, leadSessionID, leadSessionID)
	if err != nil {
		return false, fmt.Errorf("end team %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("end team %s rows affected: %w", id, err)
	}
	return n == 1, nil
}
