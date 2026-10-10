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
		query, wire = `SELECT m.team_id FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.session_id = ? AND m.state IN ('active', 'killing') AND t.ended_at = 0`, team.SeatMember
	case sessionRoleMemberRemote:
		query, wire = `SELECT team_id FROM remote_members WHERE member_session_id = ? AND state = 'active'`, team.SeatMemberRemote
	default:
		return team.Seat{Role: team.SeatNone}, nil
	}
	var teamID string
	err = tx.QueryRow(query, sessionID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return team.Seat{Role: team.SeatNone}, nil
	}
	if err != nil {
		return team.Seat{}, fmt.Errorf("seat %s: %w", sessionID, err)
	}
	return team.Seat{TeamID: teamID, Role: wire}, nil
}
