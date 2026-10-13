package resourcesmod

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

type shadowRow struct {
	WaitMS    int64
	Open      int
	WouldWait int
	Reasons   string
}

func (f *passFix) shadowOf(id string) shadowRow {
	f.t.Helper()
	var r shadowRow
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT v2_wait_ms, v2_wait_open, would_wait, wait_reasons FROM resource_leases WHERE id = ?`, id).
		Scan(&r.WaitMS, &r.Open, &r.WouldWait, &r.Reasons))
	return r
}

func newShadowFix(t *testing.T) *passFix {
	f := newPassFix(t, resources.ModeAdvise)
	f.set.set(resources.Settings{Mode: resources.ModeAdvise}) // the default gap, 30 s
	return f
}

// renew keeps the queue's clients alive across a long advance.
func (f *passFix) renew(ids ...string) {
	for _, id := range ids {
		f.m.store.RenewLease(id, f.nowMS()+30000)
	}
}

// A burst of three requests in advise: all granted at once, for real. The
// shadow grants them 30 s apart (the gap): its waits are about 0, 30 and 60 s
// after the first, and would_wait says which would have waited in the pass of
// their own request. (M: drop the shadow's own gap and the three waits equal.)
func TestShadow_BurstGrantedAtOnceShadowSpacedByTheGap(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute) // created one second before the pass
	f.queue("b", 35, 5*time.Minute)
	f.queue("c", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	for _, id := range []string{"a", "b", "c"} {
		require.Equal(t, "held", f.state(id), "advise grants at once")
	}
	assert.Equal(t, shadowRow{WaitMS: 1000, WouldWait: 0}, f.shadowOf("a"))
	assert.Equal(t, int64(-1), f.shadowOf("b").WaitMS, "b is still queued in the shadow")
	assert.Equal(t, 1, f.shadowOf("b").WouldWait)
	assert.Equal(t, 1, f.shadowOf("c").WouldWait)

	f.advance(29 * time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(-1), f.shadowOf("b").WaitMS, "inside the gap")
	f.advance(time.Second)
	f.m.admissionPass(context.Background(), "")
	b := f.shadowOf("b")
	assert.Equal(t, int64(31000), b.WaitMS)
	assert.Equal(t, 0, b.Open)
	assert.Equal(t, int64(-1), f.shadowOf("c").WaitMS)

	f.advance(30 * time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(61000), f.shadowOf("c").WaitMS)
}

// The shadow records why it held a row: the time per reason, written when the
// shadow grants it.
func TestShadow_WaitReasonsAreTheShadows(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "") // a granted by the shadow; b is behind it
	f.advance(30 * time.Second)
	f.m.admissionPass(context.Background(), "")
	b := f.shadowOf("b")
	require.Equal(t, int64(31000), b.WaitMS)
	reasons := parseReasons(t, b.Reasons)
	assert.Equal(t, int64(30000), reasons["behind"]+reasons["gap"], "the 30 s are the shadow's reasons: %v", reasons)
}

// A row that ends before the shadow would have granted it records how long it
// had waited, flagged as a lower bound.
func TestShadow_EndedBeforeTheShadowGrantIsALowerBound(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	f.advance(10 * time.Second)
	_, err := f.m.store.End("b", resources.EndReleased, f.nowMS())
	require.NoError(t, err)
	f.renew("a", "b")
	f.m.admissionPass(context.Background(), "")
	b := f.shadowOf("b")
	assert.Equal(t, shadowRow{WaitMS: 11000, Open: 1, WouldWait: 1, Reasons: b.Reasons}, b)
	assert.NotEmpty(t, b.Reasons)

	// Written once: later passes leave it alone.
	f.advance(60 * time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(11000), f.shadowOf("b").WaitMS)
}

// The shadow is in memory: after a restart the rows that were pending in it
// record nothing.
func TestShadow_RestartDropsIt(t *testing.T) {
	f := newShadowFix(t)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	f.m.dropShadow() // what a restart does to memory
	f.advance(120 * time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(-1), f.shadowOf("b").WaitMS)
	assert.Equal(t, 0, f.shadowOf("b").Open)
}

// Lease mode has no shadow, and a switch away from advise drops it.
func TestShadow_OnlyInAdvise(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(-1), f.shadowOf("a").WaitMS)

	g := newShadowFix(t)
	g.queue("x", 35, 5*time.Minute)
	g.queue("y", 35, 5*time.Minute)
	g.m.admissionPass(context.Background(), "")
	g.set.set(resources.Settings{Mode: resources.ModeLease})
	g.m.admissionPass(context.Background(), "")
	assert.Zero(t, g.m.shadowLen.Load(), "a switch to lease drops the shadow")
}

// The shadow fails open on an unknown zone like the real rule, and holds in
// danger.
func TestShadow_FollowsTheZone(t *testing.T) {
	f := newShadowFix(t)
	f.setZone("danger", "flat")
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	require.Equal(t, "held", f.state("a"))
	assert.Equal(t, int64(-1), f.shadowOf("a").WaitMS, "danger holds the shadow")
	assert.Equal(t, 1, f.shadowOf("a").WouldWait)
	f.advance(20 * time.Second)
	f.setZone("normal", "flat")
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, int64(21000), f.shadowOf("a").WaitMS)
}
