package session

import "net/http"

// SessionProvider is the interface registered to ServiceRegistry
// for other modules to access session data.
type SessionProvider interface {
	ListSessions() ([]SessionInfo, error)
	GetSession(code string) (*SessionInfo, error)
	UpdateMeta(code string, update MetaUpdate) error
	HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string)

	// TmuxInstance returns the current tmux server identity, or "" when it
	// cannot be determined. Consumed by the agent module, which already holds
	// this provider (internal/module/agent/module.go:33), to stamp the
	// generation onto the provenance envelope.
	TmuxInstance() string

	// Session creation for a module that needs a fresh tmux session of its
	// own (the nex module's take-to-terminal, exec-to-terminal spec §4.1):
	// SessionExists and ValidateCwd are the preflights a caller runs before
	// anything irreversible, CreateSession the same path POST /api/sessions
	// takes (name rule, cwd resolution, critical section, meta). Its error
	// is a *CreateError; see SessionAlive.
	SessionExists(name string) bool
	ValidateCwd(cwd string) error
	CreateSession(name, cwd string) (*SessionInfo, error)
}

// SessionInfo combines live tmux data with cached meta.
type SessionInfo struct {
	// Live from tmux (not stored in DB)
	Code           string `json:"code"`
	TmuxID         string `json:"-"` // internal only
	Name           string `json:"name"`
	Exists         bool   `json:"-"`
	CurrentCommand string `json:"current_command,omitempty"`
	PaneTitle      string `json:"pane_title,omitempty"`
	WindowName     string `json:"window_name,omitempty"`

	// TmuxInstance is the tmux server identity ("<pid>:<start_time>") the
	// session belongs to. It changes on every tmux server restart, which is
	// what lets a client tell a genuinely-live session from a reused session
	// code after a reboot (session codes are a reversible encoding of $N, so
	// $0 mints the same code every boot). Empty means "unknown" — never treat
	// two empties as a match.
	//
	// Deliberately NOT omitempty: the SPA distinguishes "unknown" from
	// "absent field / old daemon", and spec §4.6 requires "" to be
	// transmitted rather than elided.
	TmuxInstance string `json:"tmux_instance"`

	// Meta cache (stored in DB)
	Mode string `json:"mode"`
	Cwd  string `json:"cwd,omitempty"`
}

// MetaUpdate supports partial meta updates. Nil = no change.
type MetaUpdate struct {
	Mode *string
	Cwd  *string
}

// RegistryKey is the service registry key for SessionProvider.
const RegistryKey = "session.provider"
