// internal/team/wire_fact.go
package team

import "encoding/json"

// Cross-host team facts (M → L), spec docs/specs/2026-10-09-cross-host-team-spec-plan.md §6.3.
// POST /api/peers/team/facts carries one TeamFact: something that happened on the member host. The lead host's
// daemon applies it; the member host queues it in its facts outbox in the transaction of the change that caused it.

// Fact kinds.
const (
	FactEnded       = "ended"        // the membership ended on the member host
	FactRegistered  = "registered"   // a forwarded spawn's session registered: it is a member now (spec §6.3)
	FactSpawnFailed = "spawn_failed" // a forwarded spawn ended without a member; Reason says why (a SpawnReason*)
	FactRelayFailed = "relay_failed" // a relay the lead sent ended failed or cancelled on the member host (op_id, state, reason)
	FactRelayAsk    = "relay_ask"    // a remote member asks its lead to relay it (member relay spec D6)
	FactMoved       = "moved"        // the member's session moved to a new session id on the member host (a person's /relay; member relay spec D4)
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
	// relay_failed: failed | cancelled (the op is OpID below)
	State string `json:"state,omitempty"`
	// registered: the new member as the member host registered it
	MemberSession string `json:"member_session_id,omitempty"`
	Ref           string `json:"ref,omitempty"`
	PID           int    `json:"pid,omitempty"`
	ProcStart     string `json:"proc_start,omitempty"`
	Pane          string `json:"pane,omitempty"`
	Title         string `json:"title,omitempty"`
	// moved: the member's new session and ref (PID / ProcStart / Pane / Title above are its current ones), the relay op that
	// moved it when the lead started that relay ("" for a person's own /relay), and whether a person typed it.
	OpID       string `json:"op_id,omitempty"`
	NewSession string `json:"new_session_id,omitempty"`
	NewRef     string `json:"new_ref,omitempty"`
	Manual     bool   `json:"manual,omitempty"`
	// relay_ask: a remote member's 70% ask (the member host's ask id; no clocks cross hosts, so the window is a duration)
	AskID      string `json:"ask_id,omitempty"`
	UsedPct    int    `json:"used_pct,omitempty"`
	Window     int    `json:"window,omitempty"`
	ExpiresInS int    `json:"expires_in_s,omitempty"`
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
