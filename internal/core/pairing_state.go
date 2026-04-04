package core

import "sync/atomic"

// StateValue represents the daemon's pairing state.
type StateValue int32

const (
	StateNormal  StateValue = 0
	StatePairing StateValue = 1
	StatePending StateValue = 2
)

func (s StateValue) String() string {
	switch s {
	case StatePairing:
		return "pairing"
	case StatePending:
		return "pending"
	default:
		return "normal"
	}
}

// PairingState provides thread-safe access to the daemon's pairing mode.
// Zero value is StateNormal. Must not be copied after first use.
type PairingState struct {
	v atomic.Int32
}

func (ps *PairingState) Get() StateValue {
	return StateValue(ps.v.Load())
}

func (ps *PairingState) Set(s StateValue) {
	ps.v.Store(int32(s))
}
