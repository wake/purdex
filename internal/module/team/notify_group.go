package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// NotifySeatOf is what team.db stores about the seat of the session, for the notification title's group name
// (notify-title spec §3.1): a lead, a member of a team led on this host, or a member of a team led elsewhere (its own row
// holds the team's name and the lead as the lead's host told it). A session with no seat is SeatNone; a store error is an
// error, never none. The lead's recorded title and address are the request-time ones (the same rule as the roster's
// leadStoredSession): the address only while the lead's ref is still the one it was recorded with.
func (s *Store) NotifySeatOf(sessionID string) (team.NotifySeat, error) {
	if sessionID == "" {
		return team.NotifySeat{Role: team.SeatNone}, nil
	}
	var (
		seat      team.NotifySeat
		requestID string
	)
	err := s.db.QueryRow(`SELECT team_name, lead_session_id, lead_ref, request_id FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID).
		Scan(&seat.TeamName, &seat.LeadSessionID, &seat.LeadRef, &requestID)
	switch {
	case err == nil:
		seat.Role = team.SeatLead
		return s.withRecordedLead(seat, requestID)
	case !errors.Is(err, sql.ErrNoRows):
		return team.NotifySeat{}, fmt.Errorf("notify seat %s: %w", sessionID, err)
	}
	err = s.db.QueryRow(`SELECT t.team_name, t.lead_session_id, t.lead_ref, t.request_id FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state IN ('active', 'killing') AND t.ended_at = 0`, sessionID).
		Scan(&seat.TeamName, &seat.LeadSessionID, &seat.LeadRef, &requestID)
	switch {
	case err == nil:
		seat.Role = team.SeatMember
		return s.withRecordedLead(seat, requestID)
	case !errors.Is(err, sql.ErrNoRows):
		return team.NotifySeat{}, fmt.Errorf("notify seat %s: %w", sessionID, err)
	}
	err = s.db.QueryRow(`SELECT team_name, lead_ref, lead_address, lead_title FROM remote_members WHERE member_session_id = ? AND state = 'active'`, sessionID).
		Scan(&seat.TeamName, &seat.LeadRef, &seat.LeadAddress, &seat.LeadTitle)
	switch {
	case err == nil:
		seat.Role = team.SeatMemberRemote
		return seat, nil
	case !errors.Is(err, sql.ErrNoRows):
		return team.NotifySeat{}, fmt.Errorf("notify seat %s: %w", sessionID, err)
	}
	return team.NotifySeat{Role: team.SeatNone}, nil
}

// withRecordedLead adds the lead's title and address as its lead request recorded them. A missing request row leaves
// them empty (the label then falls to the ref).
func (s *Store) withRecordedLead(seat team.NotifySeat, requestID string) (team.NotifySeat, error) {
	req, ok, err := s.Get(requestID)
	if err != nil {
		return team.NotifySeat{}, fmt.Errorf("notify seat %s: request row: %w", seat.LeadSessionID, err)
	}
	if !ok {
		return seat, nil
	}
	seat.LeadTitle = req.Origin.Title
	if req.Origin.Ref == seat.LeadRef {
		seat.LeadAddress = req.Origin.Address
	}
	return seat, nil
}

// NotifyGroupOf is the display name of the team the session sits in, as the App's panel shows it (spec §1): `team_name`,
// else the lead's label. A lead of this host is read from the registry first (its title now), then from what was
// recorded, as the roster does; a remote member only has what its lead's host told it. "" = no seat.
func (m *Module) NotifyGroupOf(sessionID string) (string, error) {
	seat, err := m.store.NotifySeatOf(sessionID)
	if err != nil {
		return "", err
	}
	if seat.Role == team.SeatNone {
		return "", nil
	}
	if seat.TeamName != "" {
		return seat.TeamName, nil
	}
	if seat.LeadSessionID != "" && m.origins != nil {
		live, err := m.origins.ResolveOriginsBySession([]string{seat.LeadSessionID})
		if err != nil {
			m.logf("[team] notify group: registry unavailable, using what team.db recorded: %v", err)
		} else if o, ok := live[seat.LeadSessionID]; ok {
			ref := o.Ref
			if ref == "" {
				ref = seat.LeadRef
			}
			return team.LeadLabel(o.Title, o.Address, ref), nil
		}
	}
	return seat.GroupName(), nil
}
