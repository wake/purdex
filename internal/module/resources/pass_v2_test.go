package resourcesmod

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func (f *passFix) setZone(zone, trend string) {
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: 1, Zone: zone, Trend: trend})
}

// waitReasons reads the column back as a map; nil for ”.
func (f *passFix) waitReasons(id string) map[string]int64 {
	f.t.Helper()
	var raw string
	require.NoError(f.t, f.m.store.db.QueryRow(`SELECT wait_reasons FROM resource_leases WHERE id = ?`, id).Scan(&raw))
	if raw == "" {
		return nil
	}
	out := map[string]int64{}
	require.NoError(f.t, json.Unmarshal([]byte(raw), &out))
	return out
}

func (f *passFix) pass() { f.m.admissionPass(context.Background(), "") }

// Lease mode end to end: with a lease running and the usage rising the second
// request waits; once the trend is falling it is granted. (M: grant on rising
// and the first assertion is red; wait on falling and the last one is.)
func TestPassV2_SecondWaitsForTheTrend(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	require.Equal(t, "held", f.state("a"))

	f.queue("b", 35, 5*time.Minute)
	f.setZone("normal", "rising")
	f.pass()
	assert.Equal(t, "waiting", f.state("b"), "rising with a lease running observes")
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, "held", f.state("b"), "flat in the normal zone is granted")

	f.queue("c", 35, 5*time.Minute)
	f.setZone("warning", "flat")
	f.pass()
	assert.Equal(t, "waiting", f.state("c"), "warning and flat observes")
	f.setZone("warning", "falling")
	f.pass()
	assert.Equal(t, "held", f.state("c"), "warning and falling is granted")
}

// The gap comes first, then the trend: inside the gap nothing is granted
// whatever the zone; after it the table decides.
func TestPassV2_GapThenTrend(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.set.set(resources.Settings{Mode: resources.ModeLease}) // gap 30 s
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	require.Equal(t, "held", f.state("a"))
	f.queue("b", 35, 5*time.Minute)
	f.setZone("normal", "flat")
	f.advance(10 * time.Second)
	f.m.store.RenewLease("b", f.nowMS()+30000)
	f.pass()
	assert.Equal(t, "waiting", f.state("b"), "inside the gap")
	f.advance(25 * time.Second)
	f.m.store.RenewLease("b", f.nowMS()+30000)
	f.setZone("normal", "rising")
	f.pass()
	assert.Equal(t, "waiting", f.state("b"), "past the gap, but rising")
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, "held", f.state("b"))
	assert.Equal(t, map[string]int64{"gap": 25000}, f.waitReasons("b"))
}

// wait_reasons: 20 s of danger, then 10 s of rising, then granted, records
// exactly those. (M: charge the new reason for the interval instead of the
// previous one and the numbers swap.)
func TestPassV2_WaitReasonsAreTheTimeEachReasonHeld(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.heldLease("h", resources.ScopeProcess, 100) // a lease is running
	f.queue("a", 35, 5*time.Minute)
	f.setZone("danger", "flat")
	f.pass() // t0: danger
	f.advance(10 * time.Second)
	f.m.store.RenewLease("a", f.nowMS()+30000)
	f.pass() // t0+10: danger
	f.advance(10 * time.Second)
	f.m.store.RenewLease("a", f.nowMS()+30000)
	f.setZone("normal", "rising")
	f.pass() // t0+20: rising
	f.advance(10 * time.Second)
	f.m.store.RenewLease("a", f.nowMS()+30000)
	f.setZone("normal", "flat")
	f.pass() // t0+30: granted
	require.Equal(t, "held", f.state("a"))
	assert.Equal(t, map[string]int64{"danger": 20000, "rising": 10000}, f.waitReasons("a"))
	_, _, ok := f.m.reasons.current("a")
	assert.False(t, ok, "a granted row's entry is dropped")
}

// Everyone behind the head is "behind", and the head's own reason is what
// holds the queue.
func TestPassV2_BehindIsRecordedForTheRest(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.queue("b", 35, 5*time.Minute)
	f.setZone("danger", "flat")
	f.pass()
	ra, _, _ := f.m.reasons.current("a")
	rb, _, _ := f.m.reasons.current("b")
	assert.Equal(t, resources.ReasonDanger, ra)
	assert.Equal(t, resources.ReasonBehind, rb)
}

// A cancel and an abandon flush the interval that was open.
func TestPassV2_CancelAndAbandonFlushTheLastInterval(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "", 60))
	require.Equal(t, resources.StateWaiting, a.State)
	f.advance(7 * time.Second)
	rec := f.do(http.MethodDelete, "/api/resources/leases/"+a.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, map[string]int64{"danger": 7000}, f.waitReasons(a.ID), "cancelled")
	_, _, ok := f.m.reasons.current(a.ID)
	assert.False(t, ok)

	b := decodeLease(t, f.post(cidB, "", 60))
	f.advance(9 * time.Second)
	f.m.store.db.Exec(`UPDATE resource_leases SET lease_until = ? WHERE id = ?`, f.nowMS()-1, b.ID)
	f.m.sweepOnce(context.Background())
	require.Equal(t, "ended", f.state(b.ID))
	assert.Equal(t, resources.EndAbandoned, f.row(b.ID).EndReason)
	assert.Equal(t, map[string]int64{"danger": 9000}, f.waitReasons(b.ID), "abandoned")
}

// A grant records the zone, trend, running and gap it decided on.
func TestPassV2_GrantRecordsTheDecision(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.heldLease("h", resources.ScopeProcess, 100)
	f.setZone("warning", "falling")
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	require.Equal(t, "held", f.state("a"))
	var zone, trend string
	var running, gap int
	require.NoError(t, f.m.store.db.QueryRow(`SELECT dec_zone, dec_trend, dec_running, dec_gap_ms FROM resource_leases WHERE id = 'a'`).
		Scan(&zone, &trend, &running, &gap))
	assert.Equal(t, []any{"warning", "falling", 1, 0}, []any{zone, trend, running, gap})
}

// A cut head (a session-new waiter whose baseline is not worked out) stops the
// queue; nobody behind it is granted. (This is the old cut, now on the head.)
func TestPassV2_CutHeadStopsTheQueue(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	a := baseRow("s", "c-s")
	a.Scope, a.HolderPID = resources.ScopeSessionNew, 500
	a.CreatedAt, a.DeadlineAt, a.LeaseUntil = f.nowMS()-2000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(f.t, f.m.store, a)
	f.queue("b", 35, 5*time.Minute)
	f.m.passOnce(f.m.settings(), "", map[string]string{}) // no baseline for s
	assert.Equal(t, "waiting", f.state("s"))
	assert.Equal(t, "waiting", f.state("b"))
	r, _, _ := f.m.reasons.current("s")
	assert.Equal(t, resources.ReasonCut, r)
}

// The reasons state is read by the GET and the event goroutines while the pass
// writes it; run under -race.
func TestPassV2_ReasonsAreRaceFree(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.post(cidA, "", 60))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				f.m.handleGet(rec, httptest.NewRequest(http.MethodGet, "/api/resources", nil))
				_ = f.m.reasons.snapshot()
			}
		}()
	}
	for i := 0; i < 50; i++ {
		f.pass()
		f.m.requestEvent()
	}
	close(stop)
	wg.Wait()
	assert.Equal(t, "waiting", f.state(a.ID))
}

// An old database (before the v2 columns) opens, migrates, and keeps its rows.
func TestStore_MigratesAPreV2Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(`
		CREATE TABLE resource_leases (
			id TEXT PRIMARY KEY, client_id TEXT NOT NULL UNIQUE,
			state TEXT NOT NULL CHECK (state IN ('waiting','held','ended')),
			kind TEXT NOT NULL DEFAULT '', weight INTEGER NOT NULL, session_id TEXT NOT NULL DEFAULT '',
			holder_pid INTEGER NOT NULL, holder_start TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL CHECK (scope IN ('process','session-new')), tool_use_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, deadline_at INTEGER NOT NULL, lease_until INTEGER NOT NULL,
			granted_at INTEGER, ended_at INTEGER, overrun INTEGER NOT NULL DEFAULT 0, would_wait INTEGER NOT NULL DEFAULT 0,
			end_reason TEXT, waited_ms INTEGER, peak_use REAL, mean_use REAL, ewma REAL,
			samples INTEGER NOT NULL DEFAULT 0, empty_samples INTEGER NOT NULL DEFAULT 0, baseline TEXT,
			dec_recorded INTEGER NOT NULL DEFAULT 0, dec_load1 REAL NOT NULL DEFAULT 0, dec_ncpu INTEGER NOT NULL DEFAULT 0,
			dec_mem REAL NOT NULL DEFAULT 0, dec_measured INTEGER NOT NULL DEFAULT 0, dec_full INTEGER NOT NULL DEFAULT 0,
			dec_sum_charge REAL NOT NULL DEFAULT 0, dec_unleased REAL NOT NULL DEFAULT 0, dec_weight INTEGER NOT NULL DEFAULT 0,
			dec_path TEXT NOT NULL DEFAULT '', would_wait_r2 INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO resource_leases (id, client_id, state, weight, holder_pid, scope, created_at, deadline_at, lease_until, granted_at, overrun, dec_path)
		VALUES ('old', 'c-old', 'held', 35, 7, 'process', 1000, 2000, 3000, 1500, 1, 'overrun');`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := openLeaseStore(path)
	require.NoError(t, err)
	defer s.Close()
	have, err := columnSet(s.db, "resource_leases")
	require.NoError(t, err)
	for _, c := range []string{"dec_zone", "dec_trend", "dec_running", "dec_gap_ms", "parks", "wait_reasons"} {
		assert.True(t, have[c], c)
	}
	r, ok, err := s.Get("old")
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, r.Overrun, "the old row keeps its overrun")
	last, err := s.MaxGrantedAt()
	require.NoError(t, err)
	assert.Equal(t, int64(1500), last)
	// A second open is a no-op.
	require.NoError(t, s.Close())
	s2, err := openLeaseStore(path)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
}

// A held row counts for the gap even when nothing is in memory (the pass
// started before boot read the table, or a row was written by another path).
func TestPassV2_GapFromAHeldRowWithNothingInMemory(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.set.set(resources.Settings{Mode: resources.ModeLease}) // gap 30 s
	f.heldLease("h", resources.ScopeProcess, 100)
	f.advance(10 * time.Second)
	f.m.lastGrantMS = 0
	f.queue("a", 35, 5*time.Minute)
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, "waiting", f.state("a"))
	r, _, _ := f.m.reasons.current("a")
	assert.Equal(t, resources.ReasonGap, r)
}

// Mode advise keeps FIFO at a cut waiter too: nobody behind a session-new
// waiter without a baseline is granted.
func TestPassV2_AdviseStopsAtACutWaiter(t *testing.T) {
	f := newPassFix(t, resources.ModeAdvise)
	a := baseRow("s", "c-s")
	a.Scope, a.HolderPID = resources.ScopeSessionNew, 500
	a.CreatedAt, a.DeadlineAt, a.LeaseUntil = f.nowMS()-2000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(f.t, f.m.store, a)
	f.queue("b", 35, 5*time.Minute)
	f.m.passOnce(f.m.settings(), "", map[string]string{})
	assert.Equal(t, "waiting", f.state("s"))
	assert.Equal(t, "waiting", f.state("b"))
}

// After one failed sampler tick the snapshot is still published (two failures
// are tolerated), but admission no longer decides on it: the zone reads unknown
// and the head is granted (D-5), until a good tick brings a fresh reading
// (codex R2). (M: drop the flag and the first grant is refused.)
func TestPassV2_AFailedTickMakesTheZoneUnknownForAdmission(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setZone("danger", "rising")
	f.m.sampleFailing.Store(true)
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	require.Equal(t, "held", f.state("a"))
	assert.Equal(t, resources.PathUnknownZone, f.dec("a").path)

	f.m.sampleFailing.Store(false)
	f.queue("b", 35, 5*time.Minute)
	f.pass()
	assert.Equal(t, "waiting", f.state("b"), "a fresh danger reading holds again")
}

// A clock that stepped back keeps the gap running instead of shortening it:
// the remaining gap is counted from the newest grant, so nothing is granted
// until the clock has caught up and the gap has passed (codex R1).
func TestPassV2_ClockSteppedBackKeepsTheGap(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.m.lastGrantMS = f.nowMS() + 60000 // the newest grant is a minute "in the future"
	f.queue("a", 35, 5*time.Minute)
	f.pass()
	assert.Equal(t, "waiting", f.state("a"))
	assert.Equal(t, f.nowMS()+60000, f.m.lastGrantMS, "a pass that grants nothing leaves it")
}

// Boot reads the last grant from the table; an empty table says none.
func TestBoot_ReadsTheLastGrant(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.m.boot()
	assert.Zero(t, f.m.lastGrantMS)
	f.heldLease("h", resources.ScopeProcess, 100)
	f.m.boot()
	assert.Equal(t, f.nowMS(), f.m.lastGrantMS)
}
