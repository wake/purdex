package team

// What the mod socket asks the team module (TI-5a, team-interface spec §4.10), without the modevents package importing
// the team module.

// ModReadKey is the service-registry key of the ModReader.
const ModReadKey = "team.mod-read"

// ModRead is the answer for one session: its role, and for a lead the number of members whose state is active (on any
// host) and the team's short label.
type ModRead struct {
	Role      string // lead | member | none (a remote member is a member)
	Members   int
	TeamLabel string
}

// ModReader answers for the CURRENT session id only (a relay changes the lead's session id, and the team's with it). A
// store error is an error, never "none".
type ModReader interface {
	ModTeamRead(sessionID string) (ModRead, error)
}
