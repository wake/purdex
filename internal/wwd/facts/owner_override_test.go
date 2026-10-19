package facts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// #3561: with a fault hook naming the owner file, the real HID must not decide: a keyboard in use on the test host cannot reach the worker.
func TestOverrideOwnerFaultReplacesRealHID(t *testing.T) {
	hidActive := OwnerSources{IdleSeconds: func() (float64, error) { return 0, nil }} // the real HID says someone is typing
	real := func(s ProcSnapshot, c ProcContext) *wire.OwnerState {
		return BuildOwner(OwnerConfig{}, hidActive, s, c)
	}
	if got := real(ProcSnapshot{}, ProcContext{}); !got.Active {
		t.Fatalf("setup: the real rule should be active, got %+v", got)
	}
	away := &wire.OwnerState{Active: false, Reason: "fault: owner_active_file absent"}
	var hook *wire.OwnerState
	wrapped := OverrideOwner(real, func() *wire.OwnerState { return hook })

	hook = away
	if got := wrapped(ProcSnapshot{}, ProcContext{}); got.Active || got.Reason != away.Reason {
		t.Fatalf("the file says away but the real HID got through: %+v", got)
	}
	hook = nil // no hook in force: the real rule answers as before
	if got := wrapped(ProcSnapshot{}, ProcContext{}); !got.Active || got.Reason != "hid" {
		t.Fatalf("without a hook the real rule must answer: %+v", got)
	}
	if OverrideOwner(nil, func() *wire.OwnerState { return away }) != nil {
		t.Fatal("a host with no owner rule must stay without one")
	}
}
