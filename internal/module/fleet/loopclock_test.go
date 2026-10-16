package fleetmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
)

// The tests step the measuring by hand (tick()). The one-second loop therefore waits on a clock of its own that nothing
// advances: moving the readings' clock must not wake it, or its tick would run at an unpredictable moment next to the test's
// own and take the "slow changed" mark first (#2990: TestPushPerTickSlowOnlyWhenChanged failed once under load).
// Mutation gate: the loop's timer on the readings' clock (Deps.LoopClock ignored, or loopClock = clock) -> red; the loop never
// ticks -> red.
func TestLoopClock_TheReadingsClockDoesNotWakeTheLoop(t *testing.T) {
	r := newMRig(t, silent)
	r.fleet.PeerSSHAlias = ""
	r.start()
	sub := r.subscribe(core.FeatureFleetV1)

	eventually(t, "the loop armed its timer", func() bool { return r.loop.armed() == 1 })
	assert.Equal(t, 0, r.fc.created(), "the loop's timer is on the readings' clock: Advance would wake it")

	r.setLocal(30, 1)
	r.fc.Advance(5 * time.Second)
	r.fc.Advance(time.Second)
	assert.Equal(t, 1, r.loop.armed(), "the loop did not move")
	_, ok := pushOf(t, sub)
	assert.False(t, ok, "the loop ticked on its own after the readings' clock moved")

	r.tick() // the test's own tick is the only one
	_, ok = pushOf(t, sub)
	assert.True(t, ok)
}

// The loop itself still works: its own clock makes it tick, once per tickEvery, and each tick pushes.
func TestLoopClock_TheLoopsOwnClockTicksIt(t *testing.T) {
	r := newMRig(t, silent)
	r.fleet.PeerSSHAlias = ""
	r.start()
	sub := r.subscribe(core.FeatureFleetV1)
	eventually(t, "the loop armed its timer", func() bool { return r.loop.armed() == 1 })

	r.setLocal(30, 1)
	r.loop.Advance(tickEvery)
	var p PushPayload
	eventually(t, "the loop pushed", func() bool {
		var ok bool
		p, ok = pushOf(t, sub)
		return ok
	})
	require.Len(t, p.Hosts, 1)
	eventually(t, "the loop armed the next timer", func() bool { return r.loop.armed() == 1 })
}
