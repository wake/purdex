package team

// Per-session readers of the team module, for consumers that cannot import it (the session workbook).

// LineageRootKey is the service-registry key of the LineageRootResolver.
const LineageRootKey = "team.lineage-root"

// LineageRootResolver answers the root of one session's relay chain: the session with no predecessor. A session
// absent from lineage is its own root.
type LineageRootResolver interface {
	RootSessionOf(sessionID string) (string, error)
}

// SeatReaderKey is the service-registry key of the SeatReader.
const SeatReaderKey = "team.seat-reader"

// Seat roles on the wire.
const (
	SeatLead         = "lead"
	SeatMember       = "member"        // active member of a live team led on this host
	SeatMemberRemote = "member_remote" // active member of a team led on another host
	SeatNone         = "none"
)

// Seat is where a session sits in a team right now. TeamID is the team's id on the lead's host (for a remote member,
// the one it joined); empty for SeatNone. For SeatMemberRemote only, LeadHostID is the lead's host (its daemon host id) and
// LeadSessionID the lead's session on that host, as recorded when the member joined: where the team's journal can be asked.
type Seat struct {
	TeamID        string
	Role          string
	LeadHostID    string
	LeadSessionID string
}

// SeatReader answers a session's seat from the store as it is now (an ended member is none). A store error is an
// error, never none.
type SeatReader interface {
	SeatOf(sessionID string) (Seat, error)
}

// PredecessorReaderKey is the service-registry key of the PredecessorReader.
const PredecessorReaderKey = "team.predecessor"

// PredecessorReader answers which session a relay's new session replaced (the session_lineage row), and when
// (unix ms). ok is false for a session no relay produced. The conversation module reads it to scroll back across a relay.
type PredecessorReader interface {
	PredecessorOf(sessionID string) (prev string, at int64, ok bool, err error)
}
