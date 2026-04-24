package agent

// SubagentRef identifies a subagent attached to a frame. Fields:
//   - ID / Type / StartedAt: display identity
//   - SourcePID / SourceStartTime: process identity for proxy refs (zero for
//     native SubagentStart refs)
//   - IsProxy: true when the ref was synthesized from a cross-agent-type hook
//     (see spec §1.4)
type SubagentRef struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	StartedAt       int64  `json:"started_at"`
	SourcePID       int    `json:"source_pid"`
	SourceStartTime string `json:"source_start_time"`
	IsProxy         bool   `json:"is_proxy,omitempty"`
}
