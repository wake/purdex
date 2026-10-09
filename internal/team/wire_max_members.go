package team

// ---- PUT /api/team/max-members: the App changes a team's member limit ----

const MaxMembersRoute = "/api/team/max-members" // PUT

// MaxMembersPutRequest is the body. The App's alone: Client.Kind is the caller's own claim (told, not enforced, as the
// unattended switch and the relay quota); no pdx command writes it.
type MaxMembersPutRequest struct {
	TeamID     string `json:"team_id"`
	MaxMembers int    `json:"max_members"`
	Client     Client `json:"client"`
}

// MaxMembersView is the 200 answer. InUse is active members plus spawns still starting, the count spawn and adopt
// compare with the cap.
type MaxMembersView struct {
	TeamID     string `json:"team_id"`
	MaxMembers int    `json:"max_members"`
	InUse      int    `json:"in_use"`
}

// MaxMembersRefusal is the body of the 409 max_below_in_use: an APIError plus the count that refused.
type MaxMembersRefusal struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
	InUse  int    `json:"in_use"`
}
