package fleetmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
)

// The own host's gate summary comes from the local source, whose gate.Status always carries its Admission (readLocal passes
// admissionKnown = true). Pin the premise: the summary's admission is the status' own answer and its reason, on a host that is
// calm (level 1) and whose status has no admission.* settings to rebuild from - a rebuild would say "not held".
// Mutation gate: readLocal passes false (the status' Admission ignored) -> red.
func TestOwnHostAdmissionComesFromTheLocalGateStatus(t *testing.T) {
	r := newMRig(t, silent)
	r.fleet.PeerSSHAlias = ""
	r.start()
	r.setLocal(10, 1)
	r.local.SetSlow(leaf.LocalSlow{At: r.fc.Now(), Gate: &gate.Status{
		Settings:  []gate.Setting{{Key: "slots.heavy", Value: "2"}, {Key: "slots.light", Value: "4"}},
		Admission: gate.AdmissionState{Held: true, Reason: "disk free 3 GB is under 10"},
	}})
	r.tick()

	h := r.host("mlab:abc123")
	require.NotNil(t, h)
	require.NotNil(t, h.Slow)
	require.NotNil(t, h.Slow.Queue, "the own host has a queue summary when the local source has a gate")
	assert.True(t, h.Slow.Queue.AdmissionHeld)
	assert.Equal(t, "disk free 3 GB is under 10", h.Slow.Queue.AdmissionReason)

	// and an Admission that says "not held" stays not held, on a host that the settings alone would hold (level 4)
	r.fc.Advance(5e9)
	r.setLocal(10, 4)
	r.local.SetSlow(leaf.LocalSlow{At: r.fc.Now(), Gate: &gate.Status{
		Settings: []gate.Setting{{Key: "slots.heavy", Value: "2"}, {Key: "admission.enabled", Value: "true"}, {Key: "admission.max_pressure_level", Value: "1"}},
	}})
	r.tick()
	h = r.host("mlab:abc123")
	require.NotNil(t, h.Slow)
	require.NotNil(t, h.Slow.Queue)
	assert.False(t, h.Slow.Queue.AdmissionHeld, "the local status' own answer decides, not a rebuild from the level")
}
