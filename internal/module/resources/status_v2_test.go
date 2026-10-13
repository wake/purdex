package resourcesmod

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func (f *passFix) snapshot() resources.Snapshot {
	snap := f.m.current()
	f.m.addLeases(&snap)
	return snap
}

// The queue in the snapshot carries each waiter's current reason and since
// when; the gap's end is there while it runs. (M: read the reasons from nowhere
// and the reason is empty.)
func TestStatus_WaiterReasonAndGap(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.set.set(resources.Settings{Mode: resources.ModeLease}) // gap 30 s
	f.setZone("normal", "flat")
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	require.Equal(t, "held", f.state("a"))
	grantedAt := f.nowMS()

	f.queue("b", 35, 5*time.Minute)
	f.queue("c", 35, 5*time.Minute)
	f.pass()
	snap := f.snapshot()
	require.Len(t, snap.Waiters, 2)
	assert.Equal(t, resources.WaiterView{ID: "b", Kind: "test-full", Weight: 35, SessionID: "sid-1", Position: 1, WaitedS: 1, DeadlineInS: 300,
		Reason: "gap", ReasonSince: f.nowMS()}, snap.Waiters[0])
	assert.Equal(t, "behind", snap.Waiters[1].Reason)
	assert.Equal(t, grantedAt+30000, snap.GapUntil)

	f.advance(31 * time.Second)
	assert.Zero(t, f.snapshot().GapUntil, "the gap is over")
}

// Parked, cut and the zone reasons reach the snapshot as they are named.
func TestStatus_ReasonNames(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	assert.Equal(t, "danger", f.snapshot().Waiters[0].Reason)
	f.setZone("warning", "flat")
	f.heldLease("h", resources.ScopeProcess, 100)
	f.pass()
	assert.Equal(t, "warning_flat", f.snapshot().Waiters[0].Reason)
}

// Without a pass a waiter has no reason yet, and the field is left out of the JSON.
func TestStatus_NoReasonYetIsOmitted(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	snap := f.snapshot()
	require.Len(t, snap.Waiters, 1)
	assert.Empty(t, snap.Waiters[0].Reason)
	b, _ := json.Marshal(snap.Waiters[0])
	assert.NotContains(t, string(b), "reason")
	b, _ = json.Marshal(snap)
	assert.NotContains(t, string(b), "gap_until")
}

// The event is the snapshot, so it carries them too.
func TestStatus_EventCarriesReasonAndGap(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	ev, err := f.m.eventNow()
	require.NoError(t, err)
	var snap resources.Snapshot
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &snap))
	require.Len(t, snap.Waiters, 1)
	assert.Equal(t, "danger", snap.Waiters[0].Reason)
}

// The gap shown in advise is the shadow's own.
func TestStatus_AdviseShowsTheShadowGap(t *testing.T) {
	f := newPassFix(t, resources.ModeAdvise)
	f.set.set(resources.Settings{Mode: resources.ModeAdvise})
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	advise := resources.Settings{Mode: resources.ModeAdvise}.Effective()
	f.m.lastSettings.Store(&advise) // what the last tick read
	f.m.lastGrantAtMS.Store(1) // the real mirror is somewhere else entirely
	assert.Equal(t, f.nowMS()+30000, f.snapshot().GapUntil)
}
