package resourcesmod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

const (
	cidD = "44444444-4444-4444-8444-444444444444"
	cidE = "55555555-5555-4555-8555-555555555555"
)

// postAs posts a process-scope request of a session (explicit session_id, so
// the pid walk is not involved) with its own holder pid.
func (f *routeFix) postAs(cid, session, kind string, weight, pid int) *httptest.ResponseRecorder {
	req := resources.LeaseRequest{ClientID: cid, Kind: kind, Weight: weight, SessionID: session, HolderPID: pid, HolderStart: "Thu Oct  9 00:00:00 2026"}
	return f.do(http.MethodPost, "/api/resources/leases", req)
}

// parked makes the row of a request wait in the danger zone, pushes its deadline
// into the past and lets a pass park it.
func (f *routeFix) parked(cid, session, kind string, weight int) resources.LeaseResponse {
	f.t.Helper()
	got := decodeLease(f.t, f.postAs(cid, session, kind, weight, 4242))
	require.Equal(f.t, resources.StateWaiting, got.State)
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, got.ID)
	require.NoError(f.t, err)
	f.m.admissionPass(context.Background(), "")
	require.NotZero(f.t, f.row(got.ID).ParkedUntil, "the row is parked")
	return got
}

func (f *routeFix) rowCount() int {
	var n int
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT COUNT(*) FROM resource_leases`).Scan(&n))
	return n
}

// A new call of the same session, kind and weight takes the parked row over: the
// same id and the same place in the queue, a fresh deadline and lease, no longer
// parked; the old client is told it was replaced. (M: insert a new row instead
// and the id and place change.)
func TestReattach_AdoptsTheParkedRow(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	first := f.parked(cidA, "sid-1", "test-full", 0)
	f.advance(time.Second)
	other := decodeLease(t, f.postAs(cidB, "sid-2", "test-full", 0, 77)) // behind it
	require.Equal(t, 2, other.Position)
	created := f.row(first.ID).CreatedAt
	f.advance(3 * time.Minute)

	rec := f.postAs(cidD, "sid-1", "test-full", 0, 9999)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, first.ID, got.ID)
	assert.Equal(t, resources.StateWaiting, got.State, "no longer parked")
	assert.Equal(t, 1, got.Position, "the place is kept")
	r := f.row(first.ID)
	assert.Equal(t, cidD, r.ClientID)
	assert.Equal(t, 9999, r.HolderPID)
	assert.Equal(t, created, r.CreatedAt)
	assert.Zero(t, r.ParkedUntil)
	assert.Equal(t, f.nowMS()+540_000, r.DeadlineAt, "the hold counts from now")
	assert.Equal(t, f.nowMS()+30_000, r.LeaseUntil)
	assert.Equal(t, 2, f.rowCount(), "no new row")

	// The old client's poll is told it was replaced; the new one's is not.
	old := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+first.ID+"?client_id="+cidA, nil))
	assert.Equal(t, resources.StateReplaced, old.State)
	now := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+first.ID+"?client_id="+cidD, nil))
	assert.Equal(t, resources.StateWaiting, now.State)
}

// A waiting row that is not parked is never adopted: two parallel calls of one
// session are two requests. (M: adopt unparked rows and the second POST returns
// the first's row.)
func TestReattach_NeverAdoptsAWaitingRow(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(cidA, "sid-1", "test-full", 0, 4242))
	f.advance(time.Second)
	b := decodeLease(t, f.postAs(cidB, "sid-1", "test-full", 0, 4243))
	assert.NotEqual(t, a.ID, b.ID)
	assert.Equal(t, 2, f.rowCount())
	assert.Equal(t, 2, b.Position)
}

// Another kind, another weight, another session, or no session: not adopted.
func TestReattach_OnlyTheSameSessionKindAndWeight(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	parked := f.parked(cidA, "sid-1", "test-full", 0)
	for i, c := range []struct {
		session, kind string
		weight        int
	}{
		{"sid-1", "build", 0},
		{"sid-1", "", 40},
		{"sid-2", "test-full", 0},
	} {
		cid := []string{cidB, cidC, cidD}[i]
		got := decodeLease(t, f.postAs(cid, c.session, c.kind, c.weight, 4242))
		assert.NotEqual(t, parked.ID, got.ID, "%+v", c)
	}
	// A request with no session at all (and none to find) is never matched.
	rec := f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cidE, Kind: "test-full", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"})
	assert.NotEqual(t, parked.ID, decodeLease(t, rec).ID)
	assert.NotZero(t, f.row(parked.ID).ParkedUntil, "still parked")

	// Nor does a parked row that has no session of its own match a call with none.
	r := baseRow("nosess", "c-nosess")
	r.SessionID = ""
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-5000, f.nowMS()-1000, f.nowMS()+600_000
	mustCreate(t, f.m.store, r)
	_, err := f.m.store.Park("nosess", r.DeadlineAt, f.nowMS()+600_000)
	require.NoError(t, err)
	rec = f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: "66666666-6666-4666-8666-666666666666", Kind: "test-full", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"})
	assert.NotEqual(t, "nosess", decodeLease(t, rec).ID)
}

// An ended row is never adopted, nor is a held one.
func TestReattach_NeverAdoptsEndedOrHeldRows(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	p := f.parked(cidA, "sid-1", "test-full", 0)
	_, err := f.m.store.End(p.ID, resources.EndCancelled, f.nowMS())
	require.NoError(t, err)
	got := decodeLease(t, f.postAs(cidB, "sid-1", "test-full", 0, 4242))
	assert.NotEqual(t, p.ID, got.ID)
	assert.Equal(t, "ended", f.state(p.ID))
}

// Two parked rows of the same kind: the oldest is taken first, the next call
// takes the other. (Seeded directly: a POST of that session would itself adopt
// the first one.)
func TestReattach_OldestParkedFirst(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	seed := func(id, cid string, ageS int64) {
		r := baseRow(id, cid)
		r.SessionID = "sid-1"
		r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-ageS*1000, f.nowMS()-1000, f.nowMS()+600_000
		mustCreate(t, f.m.store, r)
		_, err := f.m.store.Park(id, r.DeadlineAt, f.nowMS()+600_000)
		require.NoError(t, err)
	}
	seed("second", "c-second", 60)
	seed("first", "c-first", 120)
	g1 := decodeLease(t, f.postAs(cidD, "sid-1", "test-full", 0, 1))
	assert.Equal(t, "first", g1.ID)
	g2 := decodeLease(t, f.postAs(cidE, "sid-1", "test-full", 0, 2))
	assert.Equal(t, "second", g2.ID)
	assert.Equal(t, 2, f.rowCount())
}

// A replay of a call's own client id still replays, even when the session has a
// parked row of someone else.
func TestReattach_ReplayStillReplays(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	f.parked(cidA, "sid-1", "test-full", 0)
	f.setZone("normal", "flat")
	g := decodeLease(t, f.postAs(cidB, "sid-9", "test-full", 0, 4242)) // another session, granted
	again := f.postAs(cidB, "sid-9", "test-full", 0, 4242)
	assert.Equal(t, http.StatusOK, again.Code)
	assert.Equal(t, g.ID, decodeLease(t, again).ID)
}

// The adopted row is judged by the pass at once: with room it is granted.
func TestReattach_PassRunsAfterAdoption(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	p := f.parked(cidA, "sid-1", "test-full", 0)
	f.setZone("normal", "flat")
	got := decodeLease(t, f.postAs(cidD, "sid-1", "test-full", 0, 1))
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, resources.StateHeld, got.State)
}

// Two callers racing for one parked row: exactly one gets it, the other gets a
// row of its own. (Run under -race.)
func TestReattach_ConcurrentAdoptersExactlyOneWins(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	p := f.parked(cidA, "sid-1", "test-full", 0)
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i, cid := range []string{cidD, cidE} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i] = decodeLease(t, f.postAs(cid, "sid-1", "test-full", 0, 100+i)).ID
		}()
	}
	wg.Wait()
	wins := 0
	for _, id := range ids {
		if id == p.ID {
			wins++
		}
	}
	assert.Equal(t, 1, wins, "ids %v", ids)
	assert.NotEqual(t, ids[0], ids[1])
	assert.Equal(t, 2, f.rowCount())
}

// A session-new request is re-attached too, with its agent process re-resolved.
func TestReattach_SessionNewScope(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	f.m.roots = &fakeRoots{roots: []resources.Root{{SessionID: "sid-1", PID: 500, ProcStart: attrStartText}}}
	post := func(cid string) resources.LeaseResponse {
		return decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
			ClientID: cid, Kind: "test-full", HolderPID: 4242, SessionID: "sid-1", Scope: resources.ScopeSessionNew}))
	}
	a := post(cidA)
	require.Equal(t, resources.StateWaiting, a.State)
	_, _ = f.m.store.db.Exec(`UPDATE resource_leases SET deadline_at = ? WHERE id = ?`, f.nowMS()-1, a.ID)
	f.m.admissionPass(context.Background(), "")
	require.NotZero(t, f.row(a.ID).ParkedUntil)
	b := post(cidB)
	assert.Equal(t, a.ID, b.ID)
	r := f.row(a.ID)
	assert.Equal(t, resources.ScopeSessionNew, r.Scope)
	assert.Equal(t, 500, r.HolderPID)
}
