// Package convmodel is the conversation model of the interface language
// (spec 2026-10-08 §8.1): the Go types, their JSON wire form, validation and
// the transcript capabilities.
//
// It is pure: standard library only, no I/O, no clock. The Claude Code
// transcript normalizer (convmodel/ccnorm) and, later, the mod and execution
// sources all produce these types.
//
// Wire rules: snake_case, times as integer milliseconds since the epoch,
// optional fields omitted when empty. Evolution is additive only: new item
// types, fields and enum values may appear, so decoding never fails on an
// unknown item type or enum value; Validate and Marshal constrain only what
// the daemon itself produces.
package convmodel

// Caps applied by producers and checked by Validate.
const (
	MaxText        = 64 << 10 // user text, agent markdown, thinking text (head)
	MaxInputString = 4 << 10  // one string value of a step input
	MaxInput       = 16 << 10 // a whole step input
	MaxInputDepth  = 32       // container levels of a step input (the input object is level 1)
	MaxOutput      = 16 << 10 // step output text
	MaxDiffLines   = 400      // hunk lines of one diff, in total
)

// Outcome is how a turn ended.
type Outcome string

// Turn outcomes.
const (
	OutcomeDone        Outcome = "done"
	OutcomeInterrupted Outcome = "interrupted"
	OutcomeFailed      Outcome = "failed"
	OutcomeRunning     Outcome = "running"
)

// Conversation is one conversation: its identity, capabilities and turns.
type Conversation struct {
	Key          Key           `json:"key"`
	Backend      string        `json:"backend,omitempty"`
	Provider     string        `json:"provider"`
	Title        string        `json:"title"`
	Status       string        `json:"status,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
	Usage        *Usage        `json:"usage,omitempty"`
	Turns        []Turn        `json:"turns"`
}

// Key identifies a conversation. It is three fields on purpose: the
// conversation-entity line owns any joined string form.
type Key struct {
	HostID    string `json:"host_id"`
	Provider  string `json:"provider"`
	SessionID string `json:"session_id"`
}

// Usage is what is known about the model and resource use.
type Usage struct {
	Model      string      `json:"model,omitempty"`
	Effort     string      `json:"effort,omitempty"`
	Context    *Context    `json:"context,omitempty"`
	RateLimits []RateLimit `json:"rate_limits,omitempty"`
	CostUSD    *float64    `json:"cost_usd,omitempty"`
	At         *int64      `json:"at,omitempty"`
}

// Context is the context window use.
type Context struct {
	Tokens  *int64   `json:"tokens,omitempty"`
	Window  *int64   `json:"window,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
}

// RateLimit is one account quota reading.
type RateLimit struct {
	Kind        string  `json:"kind"`
	PercentUsed float64 `json:"percent_used"`
	ResetsAt    *int64  `json:"resets_at,omitempty"`
}

// Turn is one prompt-to-completion round. Its ID is the opening row's id and
// Index its 0-based ordinal in the conversation.
type Turn struct {
	ID        string     `json:"id"`
	Index     int        `json:"index"`
	StartedAt int64      `json:"started_at"`
	EndedAt   *int64     `json:"ended_at,omitempty"`
	Outcome   Outcome    `json:"outcome"`
	Error     *TurnError `json:"error,omitempty"`
	// DurationMS is the turn time Claude Code records in its turn_duration row
	// (the last valid one when several); absent when no row has a non-negative number.
	DurationMS *int64 `json:"duration_ms,omitempty"`
	Items      []Item `json:"items"`
	// OmittedItems is how many of the turn's oldest items a size-capped window
	// left out (U1-6, spec §8.2); 0 and absent for a whole turn.
	OmittedItems int `json:"omitted_items,omitempty"`

	// Offset is the byte offset of the row that opened the turn. It is for
	// Go callers (cursors) and never on the wire.
	Offset int64 `json:"-"`
}

// TurnError describes the API error that failed a turn.
type TurnError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}
