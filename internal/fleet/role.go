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

const (
	RolePrimary = "primary"
	RoleStandby = "standby"
	RoleOff     = "off"
)
