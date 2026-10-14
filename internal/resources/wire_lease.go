package resources

import "time"

// Lease states, as the lease routes and the store name them.
const (
	StateWaiting = "waiting"
	StateHeld    = "held"
	StateEnded   = "ended"
	// StateQueued is what a poll or a POST answers for a waiting row that is
	// parked: it keeps its place in the queue but this call stops waiting (the
	// row's own state is still waiting).
	StateQueued = "queued"
	// StateReplaced is what a poll answers when a newer call took the row over
	// (re-attach): this client must not run its command.
	StateReplaced = "replaced"
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
	// Amendment A: how a row or a position ends.
	EndCancelledBySession = "cancelled_by_session"
	EndNoAnswer           = "no_answer"
	EndIdleTimeout        = "idle_timeout"
	EndDone               = "done"
	EndReleasedAfterLimit = "released_after_limit"
	// EndDrained: the mode left lease and the queue was emptied.
	EndDrained = "drained"
)

// EventType is the WS host event that carries the snapshot (without recent)
// while something is held or waiting, and once more when the last lease ends.
const EventType = "resources.changed"

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
	ClientID  string `json:"client_id"`
	Kind      string `json:"kind,omitempty"`
	Weight    int    `json:"weight,omitempty"`
	WaitS     int    `json:"wait_s,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	HolderPID int    `json:"holder_pid"`
	// HolderStart is when the holder process started, in the registry's form:
	// UTC, "Mon Jan _2 15:04:05 2006". The CLI turns the ps text it reads
	// (the machine's local clock) into this before it sends it; the daemon does not
	// guess a zone for a text without one.
	HolderStart string `json:"holder_start,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	// Parkable is accepted and ignored: parking is gone (amendment A), but a
	// client that still sends it must not be refused (POST rejects unknown
	// fields).
	Parkable bool `json:"parkable,omitempty"`
	// Mode is how the caller wants a busy host answered (CallQueue, CallAsk,
	// CallWait; empty reads as wait; CallAuto lets the daemon choose). Command and Cwd are shown in the queue;
	// both are cut by the daemon and env is never sent.
	Mode    string `json:"mode,omitempty"`
	Command string `json:"command,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
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
	Position int `json:"position,omitempty"`
	// ParkedUntil is, for a queued answer, when the park ends (unix ms): the
	// place is kept until then.
	ParkedUntil int64     `json:"parked_until,omitempty"`
	WaitedMS    int64     `json:"waited_ms"`
	Host        LeaseHost `json:"host"`
	Mode        string    `json:"mode"`
	// ScopeFallback is true when scope session-new could not resolve the
	// session's process and the lease measures the holder pid instead.
	ScopeFallback bool `json:"scope_fallback,omitempty"`
	// EndReason names how an ended lease ended.
	EndReason string `json:"end_reason,omitempty"`
	// Warning is set on a held build / test lease while the volume that holds the Go build cache is under its hard floor
	// even after a trim: the lease was granted all the same (#2470).
	Warning string `json:"warning,omitempty"`
	// PositionID is the position the row belongs to (set on every answer about
	// such a row, held answers included).
	PositionID string `json:"position_id,omitempty"`
	// QueuedReason says why a queue-mode request did not run (QueuedBehindQueue,
	// QueuedOwnRunning).
	QueuedReason string `json:"queued_reason,omitempty"`
	// Notice is the final notice a release answer carries (amendment A §3.3).
	Notice string `json:"notice,omitempty"`
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
	// Reason is why the last admission pass held the request back (gap,
	// danger, warning_flat, rising, behind, cut, parked) and ReasonSince when
	// that reason began, unix ms; both empty before the first pass.
	Reason      string `json:"reason,omitempty"`
	ReasonSince int64  `json:"reason_since,omitempty"`
	// ParkedUntil is when a parked request's place stops being kept (unix ms);
	// 0 when it is not parked.
	ParkedUntil int64 `json:"parked_until,omitempty"`
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
	// ErrClientIDReused: the client id names a lease that was made for a
	// different request. A replay must repeat the request it replays.
	ErrClientIDReused = "client_id_reused"
	// Amendment A: the routes that act on the caller's own session.
	ErrNotYourSession = "not_your_session" // 403
	ErrNoSession      = "no_session"       // 403: the caller is not under an agent
	ErrNotYourTurn    = "not_your_turn"    // 409: confirm on a queued position
	ErrRunning        = "running"          // 409: cancel of a running command
	ErrNoPosition     = "no_position"      // 404
)

// PositionView is one position in a Snapshot (amendment A §5), with its
// requests under it.
type PositionView struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Ref       string `json:"ref,omitempty"`
	Address   string `json:"address,omitempty"`
	State     string `json:"state"`
	// Place is the 1-based queue place of a queued position.
	Place          int    `json:"place,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	WindowUntil    int64  `json:"window_until,omitempty"`
	FirstStartedAt int64  `json:"first_started_at,omitempty"`
	Commands       int    `json:"commands"`
	SpanS          int64  `json:"span_s,omitempty"`
	Warned         bool   `json:"warned,omitempty"`
	Delivered      bool   `json:"delivered,omitempty"`
	NoticeResult   string `json:"notice_result,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ReasonSince    int64  `json:"reason_since,omitempty"`
	// Requests are the commands of the position.
	Requests []PositionRequestView `json:"requests,omitempty"`
}

// PositionRequestView is one command of a position: state is held, queued,
// waiting or asked.
type PositionRequestView struct {
	ID      string `json:"id"`
	Mode    string `json:"mode,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Command string `json:"command,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	State   string `json:"state"`
	AgeS    int64  `json:"age_s"`
}

// APIError is the body of a refused lease request: {"error": code, "detail": text}.
type APIError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// CallerRequest is the body of the routes a session uses on its own requests and
// position (confirm, cancel, done): holder_pid names the caller, from which the
// daemon finds the agent and so the session.
type CallerRequest struct {
	HolderPID int `json:"holder_pid"`
}

// PositionResponse answers cancel and done on a position.
type PositionResponse struct {
	ID        string `json:"id,omitempty"`
	State     string `json:"state"`
	EndReason string `json:"end_reason,omitempty"`
	// DonePending: the position runs and ends with its last command.
	DonePending bool `json:"done_pending,omitempty"`
}
