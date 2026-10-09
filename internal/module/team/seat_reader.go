package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// RootSessionOf walks one session's predecessors to the root of its relay chain; a session absent from lineage is
// its own root. A cycle (never written, but a raw row could) stops at the last unseen session, as ChainRoots does.
func (s *Store) RootSessionOf(sessionID string) (string, error) {
	seen := map[string]bool{sessionID: true}
	sid := sessionID
	for {
		var p string
		err := s.db.QueryRow(`SELECT predecessor_session_id FROM session_lineage WHERE session_id = ?`, sid).Scan(&p)
		if errors.Is(err, sql.ErrNoRows) {
			return sid, nil
		}
		if err != nil {
			return "", fmt.Errorf("read lineage %s: %w", sid, err)
		}
		if seen[p] {
			return sid, nil
		}
		seen[p] = true
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
			WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, team.SeatMember
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
