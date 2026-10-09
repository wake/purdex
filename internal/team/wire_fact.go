// internal/team/wire_fact.go
package team

import "encoding/json"

// Cross-host team facts (M → L), spec docs/specs/2026-10-09-cross-host-team-spec-plan.md §6.3.
// POST /api/peers/team/facts carries one TeamFact: something that happened on the member host. The lead host's
// daemon applies it; the member host queues it in its facts outbox in the transaction of the change that caused it.

// Fact kinds.
const FactEnded = "ended" // the membership ended on the member host

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
}

// TeamFactAnswer is the 200 body: the receiver's host id (the sender checks it is who it addressed) and the fact's
// outcome — {"state":"applied"} or {"state":"ignored"} (the row had already moved on, or its team had ended).
type TeamFactAnswer struct {
	ID      string          `json:"id"`
	HostID  string          `json:"host_id"`
	Outcome json.RawMessage `json:"outcome"`
}

// Outcome states of a fact.
const (
	FactApplied = "applied"
	FactIgnored = "ignored"
)
