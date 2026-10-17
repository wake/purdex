package team

// What the assist module asks the team module (assist-dispatch plan contract 1), without importing it.

// AssistTeamsKey is the service-registry key of the AssistTeams.
const AssistTeamsKey = "team.assist-teams"

// AssistTeams lists the live teams (an allowed entry is stored by team id) and finds the sessions a ref a relay replaced
// went on to be.
type AssistTeams interface {
	ListLiveTeams() ([]Team, error)
	// RefSuccessors are the session ids whose relay chain holds ref (oldest first); the caller picks the live one.
	RefSuccessors(ref string) ([]string, error)
}
