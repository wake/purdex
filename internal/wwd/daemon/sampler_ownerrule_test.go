package daemon

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The effective owner rule is the only basis for "the owner is active": a reading that says active does not empty the headroom
// on a host whose rule is empty (config has an owner source, PUT /v1/worker cleared the rule), but the block still reports it.
func TestHeadroomUsesOwnerReadingOnlyWithARule(t *testing.T) {
	for _, tc := range []struct {
		rule string
		want wire.Slots
	}{
		{"", wire.Slots{Heavy: 1, Light: 2}},
		{"hid<5m", wire.Slots{}},
	} {
		level := 1
		owner := &wire.OwnerState{Active: true, Reason: "hid"}
		s := NewSampler(SamplerOptions{
			HostID: "h1", Alias: "air26", OSVersion: "26.6.2", Sources: fakeSources(&level, owner),
			Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true, OwnerRule: tc.rule,
		})
		s.Tick(s0)
		f, _ := s.Latest()
		if f.Derived.Headroom != tc.want || f.Owner == nil || !f.Owner.Active {
			t.Fatalf("rule %q: headroom %+v owner %+v", tc.rule, f.Derived.Headroom, f.Owner)
		}
	}
}
