package fleet

// RoleView is this daemon's fleet role as the measuring side reads it. The fleet module's role engine implements it
// (F-1b2); the sessions, the aggregator and the API read it (F-1c2).
type RoleView interface {
	State() RoleState
	// OnChange registers f, called after every change of the state, in order, never concurrently with itself.
	OnChange(f func(RoleState))
}

// RoleState is one view of the role.
type RoleState struct {
	Role    string `json:"role"` // RolePrimary, RoleStandby or RoleOff
	Term    int64  `json:"term"`
	Primary string `json:"primary,omitempty"` // the primary's alias; "" when unknown
	Self    string `json:"self"`              // this daemon's alias
	Reason  string `json:"reason,omitempty"`  // why RoleOff
}

// PeerPush is what the other fleet daemon last said about its phone push, as this daemon's last call to it saw it. A standby reads it to
// know whether the primary can send a phone push at all (wwd alerts, WWD-8a-1c).
type PeerPush struct {
	Known    bool  // a call to the other daemon has completed since this one started
	Answered bool  // the last call was answered (false: unreachable or refused)
	Ready    *bool // the push_ready the answer carried; nil when it carried none (an older daemon) or there was no answer
}

// PeerPushView is the optional second face of a RoleView: the role engine implements it, a reader that needs it asks with a type
// assertion.
type PeerPushView interface {
	PeerPush() PeerPush
}

const (
	RolePrimary = "primary"
	RoleStandby = "standby"
	RoleOff     = "off"
)
