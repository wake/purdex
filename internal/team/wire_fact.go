// internal/team/wire_fact.go
package team

// Cross-host team facts (M → L), spec docs/specs/2026-10-09-cross-host-team-spec-plan.md §6.3.
// POST /api/peers/team/facts carries one TeamFact: something that happened on the member host. The lead host's
// daemon applies it; the member host queues it in its facts outbox in the transaction of the change that caused it.

// Fact kinds.
const (
	FactEnded       = "ended"        // the membership ended on the member host
	FactRegistered  = "registered"   // a forwarded spawn's session registered: it is a member now (spec §6.3)
	FactSpawnFailed = "spawn_failed" // a forwarded spawn ended without a member; Reason says why (a SpawnReason*)
)

// Reasons of an `ended` fact.
const (
	FactReasonSessionGone = "session_gone" // the member's session is no longer live (the sweeper saw it)
	FactReasonLocalEnd    = "local_end"    // the operator on the member host ended it
)

// TeamFact is the request body. ToHostID is the lead host's own host id (the receiver answers 409 wrong_host on
// another); MK is the member key of the membership the fact is about.
type TeamFact struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	ToHostID string `json:"to_host_id"`
	TeamID   string `json:"team_id"`
	MK       string `json:"mk"`
	Reason   string `json:"reason,omitempty"`
	// registered: the new member as the member host registered it
	MemberSession string `json:"member_session_id,omitempty"`
	Ref           string `json:"ref,omitempty"`
	PID           int    `json:"pid,omitempty"`
	ProcStart     string `json:"proc_start,omitempty"`
	Pane          string `json:"pane,omitempty"`
	Title         string `json:"title,omitempty"`
}
