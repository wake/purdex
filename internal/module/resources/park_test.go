package resourcesmod

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func (f *routeFix) parkedUntil(id string) int64 { return f.row(id).ParkedUntil }

func (f *routeFix) parks(id string) int {
	f.t.Helper()
	var n int
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT parks FROM resource_leases WHERE id = ?`, id).Scan(&n))
	return n
}

// The store parks a row once: the CAS wants the row still waiting, not parked
// yet, and with the deadline the pass read.
func TestStore_ParkCAS(t *testing.T) {
	s := newTestStore(t)
	r := baseRow("a", "c-a")
	r.DeadlineAt = 5000
	mustCreate(t, s, r)
	ok, err := s.Park("a", 4999, 90000) // another deadline: the row was re-attached in between
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = s.Park("a", 5000, 90000)
	require.NoError(t, err)
	assert.True(t, ok)
	got, _, _ := s.Get("a")
	assert.Equal(t, int64(90000), got.ParkedUntil)
	assert.Equal(t, int64(90000), got.LeaseUntil, "lease_until becomes parked_until")
	assert.Equal(t, 1, got.Parks)
	ok, _ = s.Park("a", 5000, 99000)
	assert.False(t, ok, "parked once, not twice")
	ok, _ = s.Park("nope", 5000, 99000)
	assert.False(t, ok)
	_, _ = s.End("a", resources.EndCancelled, 6000)
	r2 := baseRow("b", "c-b")
	r2.DeadlineAt = 5000
	mustCreate(t, s, r2)
	_, _ = s.End("b", resources.EndCancelled, 6000)
	ok, _ = s.Park("b", 5000, 90000)
	assert.False(t, ok, "an ended row is not parked")
}

// A pass that finds a row past its deadline parks it before AdmitV2 runs, so
// the row is never granted by that pass even when the host is calm and nothing
// is running. (M: park after AdmitV2 and the row is granted.)
func TestPark_PassParksBeforeDeciding(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.queue("a", 35, -time.Second) // its deadline has just passed
	f.m.admissionPass(context.Background(), "a")
	r := f.row("a")
	assert.Equal(t, "waiting", r.State, "never granted by the pass that parked it")
	assert.Equal(t, f.nowMS()+900_000, r.ParkedUntil)
	assert.Equal(t, r.ParkedUntil, r.LeaseUntil)
	assert.Equal(t, 1, f.parks("a"))
	reason, _, _ := f.m.reasons.current("a")
	assert.Equal(t, resources.ReasonParked, reason)
}

// Parked once: later passes leave it alone.
func TestPark_ParkedOnce(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.queue("a", 35, -time.Second)
	f.m.admissionPass(context.Background(), "")
	until := f.parkedUntil("a")
	f.advance(20 * time.Second)
	f.m.admissionPass(context.Background(), "")
	f.m.passIfWaiting(context.Background())
	assert.Equal(t, until, f.parkedUntil("a"))
	assert.Equal(t, 1, f.parks("a"))
}

// The park length is the park_s setting.
func TestPark_UsesTheSetting(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(0), ParkS: intp(120)})
	f.queue("a", 35, -time.Second)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, f.nowMS()+120_000, f.parkedUntil("a"))
}

// The row behind a parked head is granted: the parked one keeps its place and
// is skipped.
func TestPark_RowBehindAParkedHeadIsGranted(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.queue("a", 35, -time.Second)
	f.m.admissionPass(context.Background(), "")
	require.Equal(t, "waiting", f.state("a"))
	f.queue("b", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "")
	assert.Equal(t, "held", f.state("b"))
	assert.Equal(t, "waiting", f.state("a"))
	assert.NotZero(t, f.parkedUntil("a"))
}

// A parked row that nobody re-attaches to is abandoned when the park ends.
func TestPark_ExpiresToAbandoned(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.queue("a", 35, -time.Second)
	f.m.admissionPass(context.Background(), "")
	until := f.parkedUntil("a")
	f.clock.ms.Store(until - 1)
	f.m.sweepOnce(context.Background())
	assert.Equal(t, "waiting", f.state("a"))
	f.clock.ms.Store(until)
	f.m.sweepOnce(context.Background())
	assert.Equal(t, "ended", f.state("a"))
	assert.Equal(t, resources.EndAbandoned, f.row("a").EndReason)
}

// Advise never parks: a row cut for a baseline stays waiting past its deadline,
// untouched.
func TestPark_AdviseNeverParks(t *testing.T) {
	f := newRouteFix(t, resources.ModeAdvise)
	r := baseRow("s", "c-s")
	r.Scope, r.HolderPID = resources.ScopeSessionNew, 500
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-2000, f.nowMS()-1000, f.nowMS()+30000
	mustCreate(t, f.m.store, r)
	f.m.passOnce(f.m.settings(), "", map[string]string{}) // no baseline: it stays waiting
	assert.Equal(t, "waiting", f.state("s"))
	assert.Zero(t, f.parkedUntil("s"))
	assert.Zero(t, f.parks("s"))
}

// The long poll of a parked row answers at once as queued, with its place and
// when the park ends, and renews nothing. (M: renew it and lease_until moves.)
func TestPark_PollAnswersQueuedWithoutRenewing(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat") // the row would wait whatever happens
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	require.Equal(t, resources.StateWaiting, a.State)
	f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	f.m.admissionPass(context.Background(), "")
	until := f.parkedUntil(a.ID)
	require.NotZero(t, until)

	f.advance(5 * time.Second)
	start := time.Now()
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?wait=20", nil))
	assert.Less(t, time.Since(start), 2*time.Second, "answered at once, did not wait")
	assert.Equal(t, resources.StateQueued, got.State)
	assert.Equal(t, 1, got.Position)
	assert.Equal(t, until, got.ParkedUntil)
	assert.Equal(t, until, f.row(a.ID).LeaseUntil, "the poll renewed nothing")
}

// A poll that is waiting when the row gets parked is woken by it.
func TestPark_PendingPollIsWoken(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	done := make(chan resources.LeaseResponse, 1)
	go func() { done <- decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+a.ID+"?wait=20", nil)) }()
	time.Sleep(50 * time.Millisecond)
	f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	f.m.admissionPass(context.Background(), "")
	select {
	case got := <-done:
		assert.Equal(t, resources.StateQueued, got.State)
	case <-time.After(3 * time.Second):
		t.Fatal("the poll was not woken by the park")
	}
}

// The queue in the snapshot shows the park.
func TestPark_SnapshotShowsParkedUntil(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.queue("a", 35, -time.Second)
	f.m.admissionPass(context.Background(), "")
	snap := f.snapshot()
	require.Len(t, snap.Waiters, 1)
	assert.Equal(t, f.parkedUntil("a"), snap.Waiters[0].ParkedUntil)
	assert.Equal(t, "parked", snap.Waiters[0].Reason)
}
