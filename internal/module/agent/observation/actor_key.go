package observation

import "fmt"

// ActorKey is a composite key that uniquely identifies an actor within a
// session and generation. It is suitable for use as a Go map key.
//
// Format: <sessionID>/<generation>/<actorID>
type ActorKey struct {
	SessionID  string `json:"session_id"`
	Generation int64  `json:"generation"`
	ActorID    string `json:"actor_id"`
}

// String implements fmt.Stringer. Safe on zero value.
func (k ActorKey) String() string {
	return fmt.Sprintf("%s/%d/%s", k.SessionID, k.Generation, k.ActorID)
}
