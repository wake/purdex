package team

import "encoding/json"

// ---- The pause of an unattended session that has no relay quota left ----
// (docs/specs/2026-10-12-unattended-quota-pause-spec-plan.md §3.1, UP-1a)
//
// A paused session is the session's own state, kept in team.db as one row per (session, cause). It is paused iff it has any row.

// PausedRoute is GET /api/team/paused: the sessions that are paused now.
const PausedRoute = "/api/team/paused"

// Causes of a pause. A session may hold both at once: its own quota ran out, and its lead was paused.
const (
	PauseCauseQuota = "quota" // the session's own relay found no quota
	PauseCauseLead  = "lead"  // its lead was paused and the daemon paused its members with it
)

// Roles a paused session has: a lead of a live team, a live local member of one, or neither.
const (
	PauseRoleSolo   = "solo"
	PauseRoleLead   = "lead"
	PauseRoleMember = "member"
)

// PausedSession is one paused session of GET /api/team/paused. Causes is never empty; Since is the earliest of its rows;
// LeadRef is the ref of the team's lead ("" for a solo session); Bounced counts the messages bounced and not yet listed to it.
type PausedSession struct {
	SessionID string   `json:"session_id"`
	Ref       string   `json:"ref"`
	Title     string   `json:"title"`
	Role      string   `json:"role"`
	Causes    []string `json:"causes"`
	Since     int64    `json:"since"`
	LeadRef   string   `json:"lead_ref"`
	Bounced   int      `json:"bounced"`
}

// PausedView is the answer of GET /api/team/paused.
type PausedView struct {
	Sessions []PausedSession `json:"sessions"` // never null (MarshalJSON)
}

// MarshalJSON writes no sessions as sessions:[], not null; and no causes as [].
func (v PausedView) MarshalJSON() ([]byte, error) {
	type plain PausedView
	if v.Sessions == nil {
		v.Sessions = []PausedSession{}
	}
	return json.Marshal(plain(v))
}

// RosterPause is the pause of a roster session: its causes (sorted) and since when. A session that is not paused has none:
// RosterSession.Paused is null.
type RosterPause struct {
	Causes []string `json:"causes"`
	Since  int64    `json:"since"`
}
