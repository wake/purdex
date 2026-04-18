package agent

import "encoding/json"

// AgentProvider is the core interface that all agent providers must implement.
type AgentProvider interface {
	Type() string
	DisplayName() string
	IconHint() string
	Claim(ctx ClaimContext) bool
	DeriveStatus(eventName string, rawEvent json.RawMessage) DeriveResult
	IsAlive(tmuxTarget string) bool
}

// ClaimContext provides information for agent detection.
type ClaimContext struct {
	HookEvent   *HookEvent
	ProcessName string // pane_current_command value (e.g. "claude", "codex")
	TmuxTarget  string // tmux target for detailed detection (e.g. "mySession:")
}

// HookEvent is the raw hook event received from pdx hook CLI.
type HookEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// --- Optional capabilities ---

// HookInstaller can install/remove/check hook configurations for a specific agent.
type HookInstaller interface {
	InstallHooks(pdxPath string) error
	RemoveHooks(pdxPath string) error
	CheckHooks() (HookStatus, error)
}

// HookStatus reports the installation state of hooks for an agent.
type HookStatus struct {
	Installed bool                     `json:"installed"`
	Events    map[string]HookEventInfo `json:"events"`
	Issues    []string                 `json:"issues"`
}

// HookEventInfo describes the state of a single hook event.
type HookEventInfo struct {
	Installed bool   `json:"installed"`
	Command   string `json:"command"`
}

// StatuslineInstaller manages CC's statusLine.command in ~/.claude/settings.json.
type StatuslineInstaller interface {
	CheckStatusline() (StatuslineState, error)
	InstallStatuslinePdx(pdxPath string) error
	InstallStatuslineWrap(pdxPath, inner string) error
	RemoveStatusline() error
}

// StatuslineState describes the current state of an agent's statusLine config.
type StatuslineState struct {
	Mode         string `json:"mode"` // "none" | "pdx" | "wrapped" | "unmanaged"
	Installed    bool   `json:"installed"`
	Inner        string `json:"innerCommand,omitempty"`
	RawCommand   string `json:"rawCommand,omitempty"`
	SettingsPath string `json:"settingsPath"`
}

// HistoryProvider can retrieve conversation history for a session.
type HistoryProvider interface {
	GetHistory(cwd string, sessionID string) ([]map[string]any, error)
}

// StreamCapable marks a provider that supports stream mode handoff.
// Reserved for future implementation.
type StreamCapable interface {
	ExtractState(tmuxTarget string) (SessionState, error)
	ExitInteractive(tmuxTarget string) error
	RelayArgs(state SessionState) []string
	ResumeCommand(state SessionState) string
}

// SessionState holds agent session state for stream handoff.
type SessionState struct {
	SessionID string
	Cwd       string
}
