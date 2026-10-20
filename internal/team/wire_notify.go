package team

import "strings"

// What the notification title (notify-title spec §1, §3.1) asks of the team module: the display name of the team a
// session sits in, without the agent module importing the team module.

// NotifyGroupKey is the service-registry key of the NotifyGrouper.
const NotifyGroupKey = "team.notify-group"

// NotifyGrouper answers a session's team display name: the same name the App's panel shows (`team_name`, else the lead's
// label), "" when the session has no seat. It does I/O (team.db, the registry), so a caller on a hot path reads it from a
// background goroutine. A store error is an error, never "".
type NotifyGrouper interface {
	NotifyGroupOf(sessionID string) (string, error)
}

// NotifySeat is what team.db stores about the seat of one session, for the display name: the team's name, and the lead as
// recorded (a lead host's own session, or what a remote member was told). Role is a Seat* constant.
type NotifySeat struct {
	Role          string
	TeamName      string
	LeadSessionID string // the lead's session on this host; "" when the lead is on another host
	LeadRef       string
	LeadAddress   string
	LeadTitle     string
}

// LeadLabel is the lead's display name, as the App's `labelOf`: its title, else the name part of its address (after the
// last `/`), else its ref.
func LeadLabel(title, address, ref string) string {
	if title != "" {
		return title
	}
	if name := address[strings.LastIndex(address, "/")+1:]; name != "" {
		return name
	}
	return ref
}

// GroupName is the team's display name from what is stored: `team_name`, else the lead's label.
func (s NotifySeat) GroupName() string {
	if s.TeamName != "" {
		return s.TeamName
	}
	return LeadLabel(s.LeadTitle, s.LeadAddress, s.LeadRef)
}
