package agent

// Status represents the normalized agent status.
type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
	StatusError   Status = "error"
	StatusClear   Status = "clear"
)

// DeriveResult is the output of AgentProvider.DeriveStatus.
type DeriveResult struct {
	Status Status
	Valid  bool           // false = event should be ignored
	Model  string         // extracted model name (if any)
	Detail map[string]any // event-specific data for frontend notifications
	// Reason explains why Valid=false. Empty means truly unknown event name
	// ("event_not_in_catalog"). Non-empty signals a known event whose payload
	// cannot be mapped to a Status (e.g. "compact_ignored" for the cc
	// SessionStart compact subtype, or "notification_unknown_type" for an
	// unrecognized notification_type subtype). Handler uses this to keep
	// trace observability for known-but-unmappable events instead of folding
	// them into the generic catalog miss bucket. Ignored when Valid=true.
	Reason string
}

// DetailStrings copies only the non-empty string-valued keys present in raw
// into a new map, so an absent key stays absent rather than serializing as
// null in the WS payload's detail object.
func DetailStrings(raw map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := raw[k].(string); ok && v != "" {
			out[k] = v
		}
	}
	return out
}

// NormalizedEvent is broadcast to WS subscribers.
type NormalizedEvent struct {
	AgentType    string         `json:"agent_type"`
	Status       string         `json:"status"`
	Model        string         `json:"model,omitempty"`
	Subagents    []SubagentRef  `json:"subagents"`
	RawEventName string         `json:"raw_event_name"`
	BroadcastTs  int64          `json:"broadcast_ts"`
	Detail       map[string]any `json:"detail,omitempty"`
	// Background is the corner symbol (lights v2, spec §7): "workflow",
	// "monitor", "schedule" or "". Always on the wire: "" clears it.
	Background string `json:"background"`
	// Source says what decided Status: "mod" (a live mod stream) or "hook".
	Source string `json:"source"`
	// Epoch and Seq order the `hook` frames of one daemon process (plan
	// U1-2b-2): Epoch is the daemon's boot id (suffixed "-n" after the n-th
	// counter rotation), Seq counts every hook frame the daemon has
	// broadcast, from 1, across all sessions. A client that sees a Seq other
	// than the last plus one has lost a frame. Always on the wire (never
	// omitted; a frame that did not come from the emit slot, such as a
	// subscribe-time replay, carries "" and 0): the nex.* lesson is that an
	// omitempty counter hides the first frame.
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}
