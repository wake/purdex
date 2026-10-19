package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// RootSessionOf walks one session's predecessors to the root of its relay chain; a session absent from lineage is
// its own root. Same answer as ChainRoots (both walk with chainRoot).
func (s *Store) RootSessionOf(sessionID string) (string, error) {
	return chainRoot(sessionID, func(sid string) (string, bool, error) {
		var p string
		err := s.db.QueryRow(`SELECT predecessor_session_id FROM session_lineage WHERE session_id = ?`, sid).Scan(&p)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read lineage %s: %w", sid, err)
		}
		return p, true, nil
	})
}

// chainRoot follows predecessors to the session that has none. A cycle (never written, but a damaged row could be)
// has no root: it answers the smallest session id on the cycle, so every session of one chain gets the same key
// whichever one the walk starts from.
func chainRoot(sid string, pred func(string) (string, bool, error)) (string, error) {
	pos := map[string]int{sid: 0}
	path := []string{sid}
	for {
		p, ok, err := pred(sid)
		if err != nil {
			return "", err
		}
		if !ok {
			return sid, nil
		}
		if at, seen := pos[p]; seen {
			root := path[at]
			for _, c := range path[at:] {
				if c < root {
					root = c
				}
			}
			return root, nil
		}
		pos[p] = len(path)
		path = append(path, p)
		sid = p
	}
}

// SeatOf is the single role gate (sessionRoleIn) plus the id of the team that role came from, both read in one
// transaction so the answer is one point in time (a remote member that just joined a local team is never half of each).
func (s *Store) SeatOf(sessionID string) (team.Seat, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return team.Seat{}, fmt.Errorf("seat %s: %w", sessionID, err)
	}
	defer tx.Rollback()
	role, err := sessionRoleIn(tx, sessionID)
	if err != nil {
		return team.Seat{}, err
	}
	var query, wire string
	switch role {
	case sessionRoleLead:
		query, wire = `SELECT id FROM teams WHERE lead_session_id = ? AND ended_at = 0`, team.SeatLead
	case sessionRoleMemberLocal:
		query, wire = `SELECT m.team_id, t.lead_session_id FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.session_id = ? AND m.state IN ('active', 'killing') AND t.ended_at = 0`, team.SeatMember
	case sessionRoleMemberRemote:
		query, wire = `SELECT team_id, lead_host_id, lead_session_id FROM remote_members WHERE member_session_id = ? AND state = 'active'`, team.SeatMemberRemote
	default:
		return team.Seat{Role: team.SeatNone}, nil
	}
	seat := team.Seat{Role: wire}
	dest := []any{&seat.TeamID}
	switch wire {
	case team.SeatMemberRemote: // where the lead is, for a reader that has to ask its host (the journal)
		dest = append(dest, &seat.LeadHostID, &seat.LeadSessionID)
	case team.SeatMember: // the lead of its team on this host (the name backfill follows the lead's pairs)
		dest = append(dest, &seat.LeadSessionID)
	}
	err = tx.QueryRow(query, sessionID).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return team.Seat{Role: team.SeatNone}, nil
	}
	if err != nil {
		return team.Seat{}, fmt.Errorf("seat %s: %w", sessionID, err)
	}
	return seat, nil
}

// InRelay says whether sessionID is in a relay op that has not finished, as the session being relayed or as the new session it
// produced (team.RelayGuard).
func (s *Store) InRelay(sessionID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM relay_ops
		WHERE state NOT IN ('done', 'failed', 'cancelled') AND (session_id = ? OR new_session_id = ?) LIMIT 1`, sessionID, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("in relay %s: %w", sessionID, err)
	}
	return true, nil
}

// PredecessorOf is the session a relay replaced with sid (one session_lineage row, read by its primary key).
func (s *Store) PredecessorOf(sessionID string) (string, int64, bool, error) {
	var prev string
	var at int64
	err := s.db.QueryRow(`SELECT predecessor_session_id, at FROM session_lineage WHERE session_id = ?`, sessionID).Scan(&prev, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("read predecessor %s: %w", sessionID, err)
	}
	return prev, at, true, nil
}
