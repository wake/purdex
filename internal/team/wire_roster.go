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
	// Model and Effort are what a member was spawned with (the row's values);
	// empty for a lead, which was not spawned by the team. Context is the
	// session's last statusline reading, live else persisted, the same value
	// GET /api/team answers for a member; nil until one was ever reported.
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Context *MemberContext `json:"context,omitempty"`
	// RelayQuota is the numbers of the session's relay chain (#2062); always present.
	RelayQuota RelayQuota `json:"relay_quota"`
}

// RosterMember is an active member: its session plus how it joined.
type RosterMember struct {
	RosterSession
	State    MemberState `json:"state"`  // always MemberActive: the others leave the roster
	Origin   string      `json:"origin"` // MemberOriginSpawned | MemberOriginAdopted
	JoinedAt int64       `json:"joined_at"`
	// Task is the member's current task as the lead set it (plan T-3b, D-7): its in_progress task (the
	// newest updated_at, then the highest seq), else its newest pending one; absent when it has neither.
	// Subject only: a task's last turn / report text is never in the roster. Additive.
	Task *RosterTask `json:"task,omitempty"`
}

// RosterTask is a roster member's current task.
type RosterTask struct {
	ID      string     `json:"id"` // the display id (TaskDisplayID)
	Subject string     `json:"subject"`
	Status  TaskStatus `json:"status"`
}

// TeamRoster is one live team.
type TeamRoster struct {
	ID        string         `json:"id"`
	HostID    string         `json:"host_id"`
	TeamName  string         `json:"team_name"`  // always present, "" = none
	TeamLabel string         `json:"team_label"` // always present, "" = none
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
