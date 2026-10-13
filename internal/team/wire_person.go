package team

// ---- Person tokens (relay/team rules v2, RT-1a): a command the person typed in a session ----
//
// The mod handles `/relay` and `/team …` as registered commands. Before it makes the daemon call that the command stands for,
// it asks the daemon for a one-use token bound to the session and the command. A request that carries a valid token is the
// person's own: it is granted without a card, whatever the unattended switch. The token is a misuse guard, not a security
// boundary (an agent that types into its own pane can use one too; separating the App's permissions from the agent's is #2774).

const (
	PersonRoute           = "/api/relay/person"
	ErrPersonTokenInvalid = "person_token_invalid" // 409: the token is unknown, spent, expired or another session's or command's
	CapabilityPerson      = "team.person.v1"

	ClientKindPerson = "person"
	PersonLabel      = "使用者（終端機指令）"

	// The commands a token can be minted for.
	PersonCmdRelay = "relay"
	PersonCmdAdopt = "adopt"
	PersonCmdLead  = "lead"
	PersonCmdSpawn = "spawn"

	// Lifetimes: the commands the mod runs itself are short, the ones the model runs after composing them are long.
	PersonTTLShortS = 60
	PersonTTLLongS  = 600
	// PersonMaxLive is how many unspent tokens one session may hold; the oldest is dropped for a fifth.
	PersonMaxLive = 4
)

// PersonClient is decided_by of a request the person made by a typed command.
func PersonClient() Client { return Client{Kind: ClientKindPerson, Label: PersonLabel} }

// PersonTTLS is the lifetime in seconds of a token for command, 0 for an unknown command.
func PersonTTLS(command string) int {
	switch command {
	case PersonCmdRelay, PersonCmdAdopt:
		return PersonTTLShortS
	case PersonCmdLead, PersonCmdSpawn:
		return PersonTTLLongS
	}
	return 0
}

// PersonRequest is POST /api/relay/person.
type PersonRequest struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
}

// PersonResponse answers it.
type PersonResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix ms
}
