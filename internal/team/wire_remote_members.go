// internal/team/wire_remote_members.go
package team

// The member host's admin view of the sessions here that a lead on another host adopted (cross-host team spec
// §3.2, X2c). Both routes are the admin's (TokenAuth like every /api/team route): the App and `pdx peers host
// allow-team <alias> off --end-members` use them.
const (
	RemoteMembersRoute    = "/api/team/remote-members"     // GET
	RemoteMembersEndRoute = "/api/team/remote-members/end" // POST {mk}
)

// RemoteMemberView is one live remote member.
type RemoteMemberView struct {
	MK              string `json:"mk"`
	MemberSessionID string `json:"member_session_id"`
	Ref             string `json:"ref"`
	Title           string `json:"title"`
	Cwd             string `json:"cwd"`
	TeamID          string `json:"team_id"`
	TeamName        string `json:"team_name"`
	TeamLabel       string `json:"team_label,omitempty"` // "" until the lead host sends team.appearance
	TeamColor       *int   `json:"team_color,omitempty"` // absent = automatic (the App's hash of team_id)
	LeadHostID      string `json:"lead_host_id"`
	LeadAlias       string `json:"lead_alias"` // "" when the lead host is no longer paired
	LeadAddress     string `json:"lead_address"`
	Origin          string `json:"origin"` // adopted | spawned
	State           string `json:"state"`  // active (only live rows are listed)
	CreatedAt       int64  `json:"created_at"`
}

// RemoteMembersResponse is GET's body: the live rows, oldest first (created_at ascending); never null.
type RemoteMembersResponse struct {
	Members []RemoteMemberView `json:"members"`
}

// RemoteMemberEndRequest is POST's body.
type RemoteMemberEndRequest struct {
	MK string `json:"mk"`
}

// RemoteMemberEndResponse is POST's 200 body.
type RemoteMemberEndResponse struct {
	MK    string `json:"mk"`
	State string `json:"state"` // ended
}

// RemoteMemberEndError is POST's error body: 400 bad_request, 404 not_found, 409 not_live (State is its state now).
type RemoteMemberEndError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
	State  string `json:"state,omitempty"`
}
