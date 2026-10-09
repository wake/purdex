package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ModTeamRead answers the mod socket's team read (TI-5a) in one read transaction on the indexed queries of the role gate:
// no fork, no table scan. A lead gets its live team's active member count (every host: a remote active row is a member
// of the team) and label; a member, local or remote, is "member" with no count; anything else is "none".
func (s *Store) ModTeamRead(sessionID string) (team.ModRead, error) {
	if sessionID == "" {
		return team.ModRead{Role: team.SeatNone}, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return team.ModRead{}, fmt.Errorf("mod team read: %w", err)
	}
	defer tx.Rollback()
	role, err := sessionRoleIn(tx, sessionID)
	if err != nil {
		return team.ModRead{}, err
	}
	switch role {
	case sessionRoleLead:
		var id, label string
		err := tx.QueryRow(`SELECT id, team_label FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID).Scan(&id, &label)
		if errors.Is(err, sql.ErrNoRows) {
			return team.ModRead{Role: team.SeatNone}, nil
		}
		if err != nil {
			return team.ModRead{}, fmt.Errorf("mod team read %s: %w", sessionID, err)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id = ? AND state = 'active'`, id).Scan(&n); err != nil {
			return team.ModRead{}, fmt.Errorf("mod team read %s: %w", sessionID, err)
		}
		return team.ModRead{Role: team.SeatLead, Members: n, TeamLabel: label}, nil
	case sessionRoleMemberLocal, sessionRoleMemberRemote:
		return team.ModRead{Role: team.SeatMember}, nil
	}
	return team.ModRead{Role: team.SeatNone}, nil
}
