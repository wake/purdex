package team

import "encoding/json"

// ---- U24: the team roster (adopt spec D-U24-5; plan PL-1f′) ----
//
// The host-wide view of every live team, for an App that has no session
// inbox: GET /api/team is the caller's own team only. The daemon sends it
// as a team.roster HostEvent (a snapshot to each new subscriber, changed
// after every change) and answers it on GET /api/team/roster.

const RosterEventType = "team.roster" // HostEvent.Type

// RosterSession is one session of a team as the roster shows it. Live
// says where the values come from: the live registry entry (true), or the
// ones team.db stored (false).
type RosterSession struct {
	SessionID   string `json:"session_id"`
	Ref         string `json:"ref"`
	Address     string `json:"address"`
	Title       string `json:"title,omitempty"`
	Name        string `json:"name,omitempty"`         // the registry name
	TmuxSession string `json:"tmux_session,omitempty"` // the tmux session NAME; "" when not in tmux
	Live        bool   `json:"live"`
}

// RosterMember is an active member: its session plus how it joined.
type RosterMember struct {
	RosterSession
	State    MemberState `json:"state"`  // always MemberActive: the others leave the roster
	Origin   string      `json:"origin"` // MemberOriginSpawned | MemberOriginAdopted
	JoinedAt int64       `json:"joined_at"`
}

// TeamRoster is one live team.
type TeamRoster struct {
	ID        string         `json:"id"`
	HostID    string         `json:"host_id"`
	CreatedAt int64          `json:"created_at"`
	Lead      RosterSession  `json:"lead"`
	Members   []RosterMember `json:"members"` // active members, join order; never null (MarshalJSON)
}

// MarshalJSON writes no members as members:[], not null.
func (t TeamRoster) MarshalJSON() ([]byte, error) {
	type plain TeamRoster // no methods: avoids recursion
	if t.Members == nil {
		t.Members = []RosterMember{}
	}
	return json.Marshal(plain(t))
}

// Roster is the answer of GET /api/team/roster: the live teams, oldest first.
type Roster struct {
	Teams []TeamRoster `json:"teams"` // never null (MarshalJSON)
}

// MarshalJSON writes no teams as teams:[], not null.
func (r Roster) MarshalJSON() ([]byte, error) {
	type plain Roster
	if r.Teams == nil {
		r.Teams = []TeamRoster{}
	}
	return json.Marshal(plain(r))
}

// RosterEventValue is HostEvent.Value (JSON string) for RosterEventType:
// {op:"snapshot", teams} to each new subscriber, {op:"changed", teams}
// after every change.
type RosterEventValue struct {
	Op    string       `json:"op"` // "snapshot" | "changed"
	Teams []TeamRoster `json:"teams"`
}

// MarshalJSON writes no teams as teams:[], not null.
func (v RosterEventValue) MarshalJSON() ([]byte, error) {
	type plain RosterEventValue
	if v.Teams == nil {
		v.Teams = []TeamRoster{}
	}
	return json.Marshal(plain(v))
}
