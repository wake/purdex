package resourcesmod

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// A fixture with a queued position of B, an ask row of A waiting at its running
// position, and a held row of A: everything a drain meets.
func (f *posFix) drainFixture() (running, held, ask, queued string) {
	f.t.Helper()
	run := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(f.t, resources.StateHeld, run.State)
	f.setZone("danger", "flat")
	a := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallAsk, "bg"))
	require.Equal(f.t, resources.StateWaiting, a.State)
	b := decodeLease(f.t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	require.Equal(f.t, resources.StateQueued, b.State)
	return run.PositionID, run.ID, a.ID, b.ID
}

// Leaving lease ends every live position, drained, with their waiting rows; a
// held row keeps running and its release ends it as today; a poll of an ask row
// answers ended / drained. (M: skip the drain outside lease.)
func TestDrain_LeavingLeaseEndsEveryLivePosition(t *testing.T) {
	f := newPosFix(t)
	runningPos, held, ask, queued := f.drainFixture()
	f.set.set(resources.Settings{Mode: resources.ModeAdvise, GapS: intp(0)})
	f.sweep()

	for _, id := range []string{runningPos, f.row(queued).PositionID} {
		p := f.position(id)
		assert.Equal(t, resources.PosEnded, p.State, id)
		assert.Equal(t, resources.EndDrained, p.EndReason, id)
	}
	assert.Equal(t, resources.StateEnded, f.state(queued))
	assert.Equal(t, resources.EndDrained, f.row(queued).EndReason)
	assert.Equal(t, resources.EndDrained, f.row(ask).EndReason)
	assert.Equal(t, resources.StateHeld, f.state(held), "a held row keeps running (V8)")
	assert.Empty(t, f.livePositions())

	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+ask+"?wait=0", nil))
	assert.Equal(t, resources.StateEnded, got.State)
	assert.Equal(t, resources.EndDrained, got.EndReason)

	rel := f.release(held, false)
	assert.Equal(t, resources.StateEnded, rel.State)
	assert.Equal(t, resources.EndReleased, rel.EndReason, "ends as a plain lease does")
}

func TestDrain_RunsInMeasureAndOff(t *testing.T) {
	for _, mode := range []string{resources.ModeMeasure, resources.ModeOff} {
		t.Run(mode, func(t *testing.T) {
			f := newPosFix(t)
			f.setZone("danger", "flat")
			q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
			f.set.set(resources.Settings{Mode: mode, GapS: intp(0)})
			f.sweep()
			assert.Equal(t, resources.EndDrained, f.position(q.PositionID).EndReason)
			assert.Equal(t, resources.EndDrained, f.row(q.ID).EndReason)
		})
	}
}

// A setting that could not be read is not a mode: nothing is drained, so a
// hostconfig outage does not empty the queue. (M: drain on a failed read.)
func TestDrain_UnreadableSettingDrainsNothing(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.set.fail(errors.New("hostconfig is down"))
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State)
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(0)})
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State, "lease again: nothing to drain")
}

// The notice slot `drained` is armed only when something of the session is pending (Q7).
func TestDrain_NoticeOnlyWhenSomethingIsPending(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	named := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.name(named.ID) // the session was told its turn came
	plain := decodeLease(t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.set.set(resources.Settings{Mode: resources.ModeAdvise, GapS: intp(0)})
	f.sweep()
	assert.Equal(t, "drained", f.position(named.PositionID).NoticeKind)
	assert.Empty(t, f.position(plain.PositionID).NoticeKind)
}

// ---- boot ----

// bootWith opens a module on a database seeded by fn, with the setting given.
func bootWith(t *testing.T, mode string, fn func(s *leaseStore, now int64)) (*Module, *logSink, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	dir := t.TempDir()
	seed(t, filepath.Join(dir, "resources.db"), func(s *leaseStore) { fn(s, clock.now().UnixMilli()) })
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: mode})
	m, logs := initedModule(t, dir, set, idleSampler())
	m.now = clock.now
	require.NoError(t, m.Start(context.Background()))
	return m, logs, clock
}

func seedQueuedPosition(t *testing.T, s *leaseStore, id, session string, now int64) {
	t.Helper()
	_, _, _, err := s.CreateAttached(positionRow{ID: id, SessionID: session, CreatedAt: now},
		leaseRow{ID: "row-" + id, ClientID: "c-" + id, Weight: 35, SessionID: session, HolderPID: 7,
			Scope: resources.ScopeProcess, CreatedAt: now, Mode: resources.CallQueue, Command: "go test", Cwd: "/w"})
	require.NoError(t, err)
}

func TestBoot_DrainsWhenStartingOutsideLease(t *testing.T) {
	m, logs, _ := bootWith(t, resources.ModeAdvise, func(s *leaseStore, now int64) {
		seedQueuedPosition(t, s, "pos_00000001", "s1", now)
	})
	p, ok, err := m.store.PositionByID("pos_00000001")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndDrained, p.EndReason)
	assert.True(t, logsHave(logs, "drained 1 positions"), "logged")
}

func TestBoot_KeepsPositionsInLease(t *testing.T) {
	m, _, _ := bootWith(t, resources.ModeLease, func(s *leaseStore, now int64) {
		seedQueuedPosition(t, s, "pos_00000001", "s1", now)
	})
	p, _, err := m.store.PositionByID("pos_00000001")
	require.NoError(t, err)
	assert.Equal(t, resources.PosQueued, p.State)
}

// The §2.4a cases a–d left in the file are repaired at boot, before the sampler,
// and the repairs are logged. (M: skip the reconcile call.)
func TestBoot_ReconcileRunsBeforeTheSampler(t *testing.T) {
	m, logs, _ := bootWith(t, resources.ModeLease, func(s *leaseStore, now int64) {
		// (a) running, no held row
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_0000000a", SessionID: "sa", State: resources.PosQueued, CreatedAt: now}))
		setState(t, s, "pos_0000000a", "state = 'running'")
		// (b) idle, with a held row
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_0000000b", SessionID: "sb", State: resources.PosQueued, CreatedAt: now}))
		setState(t, s, "pos_0000000b", "state = 'idle', window_until = 1")
		require.NoError(t, createRow(s, txRow("hb", "sb", "pos_0000000b", resources.CallQueue, "x", "/w", now)))
		holdRow(t, s, "hb")
		// (c) a waiting row of a position that is gone
		require.NoError(t, createRow(s, txRow("wc", "sc", "pos_gone", resources.CallQueue, "x", "/w", now)))
		// (d) queued, no waiting row
		require.NoError(t, s.insertPosition(positionRow{ID: "pos_0000000d", SessionID: "sd", State: resources.PosQueued, CreatedAt: now}))
	})
	pa, _, _ := m.store.PositionByID("pos_0000000a")
	assert.Equal(t, resources.PosIdle, pa.State)
	pb, _, _ := m.store.PositionByID("pos_0000000b")
	assert.Equal(t, resources.PosRunning, pb.State)
	wc, _, _ := m.store.Get("wc")
	assert.Equal(t, resources.StateEnded, wc.State)
	assert.Equal(t, resources.EndAbandoned, wc.EndReason)
	pd, _, _ := m.store.PositionByID("pos_0000000d")
	assert.Equal(t, resources.PosEnded, pd.State)
	for _, want := range []string{"pos_0000000a", "pos_0000000b", "wc", "pos_0000000d"} {
		assert.True(t, logsHave(logs, want), "the repair of %s is logged", want)
	}
}

// A queue row keeps lease_until = 0 across a boot: nobody polls it, and the grace
// would otherwise give it a lease that runs out. (M: drop the mode filter.)
func TestBoot_GraceSkipsQueueRows(t *testing.T) {
	m, _, _ := bootWith(t, resources.ModeLease, func(s *leaseStore, now int64) {
		seedQueuedPosition(t, s, "pos_00000001", "s1", now)
		require.NoError(t, createRow(s, withPos(txRow("ask", "s1", "pos_00000001", resources.CallAsk, "bg", "/w", now), 0)))
		_, err := s.db.Exec(`UPDATE resource_leases SET lease_until = 5 WHERE id = 'ask'`)
		require.NoError(t, err)
	})
	q, _, err := m.store.Get("row-pos_00000001")
	require.NoError(t, err)
	assert.Zero(t, q.LeaseUntil)
	a, _, err := m.store.Get("ask")
	require.NoError(t, err)
	assert.NotZero(t, a.LeaseUntil, "an ask row is polled: it gets the grace")
	assert.Greater(t, a.LeaseUntil, int64(5))
}

// ---- rollback ----

// p2aLeaseCols is the column list the P2a daemon (the last deployed before
// amendment A) reads a row with. An older daemon on a database this daemon has
// migrated must still read every row.
const p2aLeaseCols = `id, client_id, state, kind, weight, session_id, holder_pid, holder_start, scope, tool_use_id,
	created_at, deadline_at, lease_until, COALESCE(granted_at,0), COALESCE(ended_at,0),
	overrun, would_wait, COALESCE(end_reason,''), COALESCE(waited_ms,0),
	COALESCE(peak_use,0), COALESCE(mean_use,0), COALESCE(ewma,0), samples, empty_samples, COALESCE(baseline,''),
	parks, parked_until, parkable`

// (M: rename one column in the migration.)
func TestRollback_TheP2aColumnListStillReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	s, err := openLeaseStore(path)
	require.NoError(t, err)
	require.NoError(t, createRow(s, withPos(txRow("a", "s1", "", "", "x", "/w", 10), 0)))
	require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 11)))
	require.NoError(t, s.Close())

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Query(`SELECT ` + p2aLeaseCols + ` FROM resource_leases`)
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, n)
	// And the table the older daemon never reads does not get in its way: its
	// own state check still holds for every row.
	var bad int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM resource_leases WHERE state NOT IN ('waiting','held','ended')`).Scan(&bad))
	assert.Zero(t, bad)
}

func logsHave(l *logSink, sub string) bool { return l.count(sub) > 0 }

// The mode changes to advise and a pass runs before the sweeper's next tick: the
// positions end drained first, and their rows are not granted as plain ones.
// (M: leave the drain to the sweeper.)
func TestDrain_APassBeforeTheSweeperDoesNotGrantPositionRows(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateQueued, q.State)
	f.set.set(resources.Settings{Mode: resources.ModeAdvise, GapS: intp(0)})
	f.pass() // before any sweeper tick
	r := f.row(q.ID)
	assert.Equal(t, resources.StateEnded, r.State)
	assert.Equal(t, resources.EndDrained, r.EndReason, "not granted")
	assert.Equal(t, resources.EndDrained, f.position(q.PositionID).EndReason)
}

// A pass that read advise, and found the setting back at lease by the time it holds
// the lock, drains nothing. (M: act on the mode read before the lock.)
func TestDrain_APassWithAStaleAdviseDrainsNothing(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.set.set(resources.Settings{Mode: resources.ModeAdvise, GapS: intp(0)})
	f.m.beforeLockHook = func() { // the setting flips back after the pass read it
		f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(0)})
	}
	f.pass()
	f.m.beforeLockHook = nil
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State)
	assert.Equal(t, resources.StateWaiting, f.state(q.ID))
}
