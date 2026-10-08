package resources

import "time"

// Lease states, as the lease routes and the store name them.
const (
	StateWaiting = "waiting"
	StateHeld    = "held"
	StateEnded   = "ended"
	// StateNone is what a route answers when there is no row to describe: a
	// request granted in mode off or measure (nothing is recorded), or a
	// release by a client id the daemon never saw.
	StateNone = "none"
)

// Lease scopes: whose process tree a lease measures.
const (
	// ScopeProcess is the tree under the holder pid itself.
	ScopeProcess = "process"
	// ScopeSessionNew is whatever a session's agent process started after the
	// lease was granted (the mod's tool call).
	ScopeSessionNew = "session-new"
)

// Reasons a lease ends (the end_reason column).
const (
	EndReleased   = "released"
	EndCancelled  = "cancelled"
	EndHolderGone = "holder_gone"
	EndExpired    = "expired"
	EndAbandoned  = "abandoned"
	EndVanished   = "vanished"
)

// Limits of the lease routes.
const (
	// MaxWaitS is the longest a request may ask to wait: the mod's own call
	// cap is 10 minutes, so the daemon's deadline stays under it.
	MaxWaitS = 590
	// MaxPollS is the longest one GET poll waits.
	MaxPollS = 25
	// LeaseS is how long a waiting row lives without a poll renewing it.
	LeaseS = 30
	// MaxExplicitWeight is the largest weight a request may name (a weight
	// above Capacity is granted only when nothing else is active).
	MaxExplicitWeight = 200
	// RecentLimit is how many ended leases a snapshot lists.
	RecentLimit = 20
)

// LeaseRequest is the body of POST /api/resources/leases. Exactly one of Kind
// and Weight is set.
type LeaseRequest struct {
	ClientID    string `json:"client_id"`
	Kind        string `json:"kind,omitempty"`
	Weight      int    `json:"weight,omitempty"`
	WaitS       int    `json:"wait_s,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	HolderPID   int    `json:"holder_pid"`
	HolderStart string `json:"holder_start,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
}

// LeaseHost is the part of the host reading a lease answer carries.
type LeaseHost struct {
	Measured int  `json:"measured"`
	Full     bool `json:"full"`
}

// LeaseResponse answers POST, GET and DELETE of a lease.
type LeaseResponse struct {
	// ID is empty when no row was recorded (mode off or measure).
	ID    string `json:"id,omitempty"`
	State string `json:"state"`
	// Granted is true once the lease was granted, whatever became of it
	// since.
	Granted bool `json:"granted"`
	Overrun bool `json:"overrun"`
	// WouldWait is set in mode advise only: the request was granted at once,
	// but in mode lease it would have waited.
	WouldWait *bool `json:"would_wait,omitempty"`
	// Position is the 1-based place in the queue of a waiting request.
	Position int       `json:"position,omitempty"`
	WaitedMS int64     `json:"waited_ms"`
	Host     LeaseHost `json:"host"`
	Mode     string    `json:"mode"`
	// ScopeFallback is true when scope session-new could not resolve the
	// session's process and the lease measures the holder pid instead.
	ScopeFallback bool `json:"scope_fallback,omitempty"`
	// EndReason names how an ended lease ended.
	EndReason string `json:"end_reason,omitempty"`
}

// LeaseView is one held lease in a Snapshot.
type LeaseView struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind,omitempty"`
	Weight    int     `json:"weight"`
	Charge    float64 `json:"charge"`
	Use       float64 `json:"use"`
	SessionID string  `json:"session_id,omitempty"`
	AgeS      int64   `json:"age_s"`
	Overrun   bool    `json:"overrun"`
}

// WaiterView is one waiting request in a Snapshot.
type WaiterView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind,omitempty"`
	Weight      int    `json:"weight"`
	SessionID   string `json:"session_id,omitempty"`
	Position    int    `json:"position"`
	WaitedS     int64  `json:"waited_s"`
	DeadlineInS int64  `json:"deadline_in_s"`
}

// RecentView is one ended lease in a Snapshot, for pdx lease ls.
type RecentView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind,omitempty"`
	Weight    int       `json:"weight"`
	SessionID string    `json:"session_id,omitempty"`
	EndReason string    `json:"end_reason"`
	Overrun   bool      `json:"overrun"`
	WouldWait bool      `json:"would_wait,omitempty"`
	WaitedMS  int64     `json:"waited_ms"`
	EndedAt   time.Time `json:"ended_at"`
}

// Error codes of the lease routes, in APIError.Error.
const (
	ErrBadRequest  = "bad_request"
	ErrUnknownKind = "unknown_kind"
	ErrNoLease     = "no_lease"
	ErrNotReady    = "not_ready" // the daemon is stopping, or resources.db is not open
)

// APIError is the body of a refused lease request: {"error": code, "detail": text}.
type APIError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}
