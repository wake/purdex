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
}

// NormalizedEvent is broadcast to WS subscribers.
type NormalizedEvent struct {
	AgentType    string         `json:"agent_type"`
	Status       string         `json:"status"`
	Model        string         `json:"model,omitempty"`
	Subagents    []string       `json:"subagents"`
	RawEventName string         `json:"raw_event_name"`
	BroadcastTs  int64          `json:"broadcast_ts"`
	Detail       map[string]any `json:"detail,omitempty"`
}
