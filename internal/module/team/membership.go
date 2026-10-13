package teammod

import (
	"database/sql"
	"errors"
	"fmt"
)

// Membership implements team.MembershipReader (badge spec §3.1). It is its own read, not SessionRole: that ranks the lead
// first and has a state set of its own (it keeps a `killing` seat's role), while the badge asks only "is this session an
// active member of a team still running". The ended-team filter matters: when a team ends its member rows stay `active`
// (#2757), and those sessions are plain sessions again.
func (s *Store) Membership(sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	var one int
	switch err := s.db.QueryRow(`SELECT 1 FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID).Scan(&one); {
	case err == nil:
		return false, nil // a lead is not a member
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("membership of %s: %w", sessionID, err)
	}
	loc, locArgs := s.local("m.host_id") // a seat on another host runs there, and its events reach that host's daemon
	args := append([]any{sessionID}, locArgs...)
	switch err := s.db.QueryRow(`SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0 AND `+loc, args...).Scan(&one); {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("membership of %s: %w", sessionID, err)
	}
	_, ok, err := s.ActiveRemoteMemberBySession(sessionID)
	if err != nil {
		return false, fmt.Errorf("membership of %s: %w", sessionID, err)
	}
	return ok, nil
}
