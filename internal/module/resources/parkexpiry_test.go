package resourcesmod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// A park that has ended keeps its place no more: a call that arrives after
// parked_until but before the sweeper has run makes a new row instead of
// bringing the old one back (codex R2). (M: drop the time check and the old row
// is adopted.)
func TestParkExpiry_AnEndedParkIsNotAdopted(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	p := f.parked(cidA, "sid-1", "test-full", 0)
	until := f.parkedUntil(p.ID)

	f.clock.ms.Store(until - 1) // one millisecond left: still adoptable
	f.m.store.RenewLease(p.ID, until)
	got := decodeLease(t, f.postAs(cidB, "sid-1", "test-full", 0, 1))
	assert.Equal(t, p.ID, got.ID, "inside the park")

	g := newRouteFix(t, resources.ModeLease)
	g.setZone("danger", "flat")
	q := g.parked(cidA, "sid-1", "test-full", 0)
	g.clock.ms.Store(g.parkedUntil(q.ID)) // the park has just ended, the sweeper has not run
	fresh := decodeLease(t, g.postAs(cidB, "sid-1", "test-full", 0, 1))
	assert.NotEqual(t, q.ID, fresh.ID, "a new row, not the old place")
	assert.Equal(t, 2, g.rowCount())
	g.m.sweepOnce(context.Background())
	assert.Equal(t, "ended", g.state(q.ID))
	assert.Equal(t, resources.EndAbandoned, g.row(q.ID).EndReason)
}

// The boot grace does not stretch a park: the row is abandoned exactly when its
// park ends, whatever happened to the daemon meanwhile (codex R2). (M: extend
// parked rows too and the row survives the park.)
func TestParkExpiry_BootGraceLeavesAParkAlone(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	p := f.parked(cidA, "sid-1", "test-full", 0)
	until := f.parkedUntil(p.ID)
	w := decodeLease(t, f.postAs(cidB, "sid-2", "test-full", 0, 2)) // an ordinary waiter
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET lease_until = ? WHERE id = ?`, f.nowMS()+1000, w.ID)
	require.NoError(t, err)

	f.clock.ms.Store(until - 5_000) // the daemon restarts 5 s before the park ends
	f.m.boot()
	assert.Equal(t, until, f.row(p.ID).LeaseUntil, "the park keeps its own end")
	assert.Equal(t, f.nowMS()+bootGrace.Milliseconds(), f.row(w.ID).LeaseUntil, "an ordinary waiter gets the grace")

	f.clock.ms.Store(until)
	f.m.sweepOnce(context.Background())
	assert.Equal(t, resources.EndAbandoned, f.row(p.ID).EndReason)
}
