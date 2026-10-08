package resourcesmod

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// minuteFix is a useFix whose clock starts on a minute boundary.
type minuteFix struct{ *useFix }

func newMinuteFix(t *testing.T) *minuteFix {
	f := &minuteFix{newUseFix(t)}
	f.clock.ms.Store(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).UnixMilli())
	f.m.interval = resources.SampleInterval
	return f
}

// tick feeds one good tick to the timeline and moves the clock one interval.
func (f *minuteFix) tick(h resources.HostUse) {
	f.m.noteMinute(h, f.clock.now())
	f.advance(f.m.interval)
}

func (f *minuteFix) minutes() []minuteRow {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT at, load1, ncpu, mem, measured, full, full_ticks, full_starts, full_longest_s,
		held, heavy_held, sum_charge, waiting, unleased FROM host_minutes ORDER BY at`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []minuteRow
	for rows.Next() {
		var r minuteRow
		var full int
		if err := rows.Scan(&r.At, &r.Load1, &r.NCPU, &r.Mem, &r.Measured, &full, &r.FullTicks, &r.FullStarts, &r.FullLongestS,
			&r.Held, &r.HeavyHeld, &r.SumCharge, &r.Waiting, &r.Unleased); err != nil {
			f.t.Fatal(err)
		}
		r.Full = full != 0
		out = append(out, r)
	}
	return out
}

func (f *minuteFix) ticks(n int, h resources.HostUse) {
	for i := 0; i < n; i++ {
		f.tick(h)
	}
}

// A row is written when the next minute's first tick arrives, and holds the
// minute's peaks and the last ncpu.
func TestMinutes_RowPerMinuteWithPeaks(t *testing.T) {
	f := newMinuteFix(t)
	f.tick(resources.HostUse{Load1: 3, Mem: 40, Measured: 40, NCPU: 8})
	f.tick(resources.HostUse{Load1: 7, Mem: 55, Measured: 70, NCPU: 10})
	f.tick(resources.HostUse{Load1: 2, Mem: 30, Measured: 30, NCPU: 10})
	if got := f.minutes(); len(got) != 0 {
		t.Fatalf("the open minute was written early: %+v", got)
	}
	f.ticks(10, resources.HostUse{Load1: 1, NCPU: 10}) // 13 ticks in all: crosses into minute 2
	got := f.minutes()
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	r := got[0]
	if r.At != time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).UnixMilli() || r.Load1 != 7 || r.Mem != 55 || r.Measured != 70 || r.NCPU != 10 || r.Full {
		t.Errorf("row = %+v", r)
	}
}

// full_ticks counts the ticks the flag was on, full_starts the off→on
// transitions, full_longest_s the longest run: here 3 ticks (15 s), a gap,
// then 1 tick.
func TestMinutes_FullTicksStartsAndLongest(t *testing.T) {
	f := newMinuteFix(t)
	on, off := resources.HostUse{Full: true, NCPU: 10}, resources.HostUse{NCPU: 10}
	f.tick(off)
	f.ticks(3, on)
	f.tick(off)
	f.tick(on)
	f.ticks(6, off) // 12 ticks: the minute is complete
	f.tick(off)     // first tick of the next minute flushes
	r := f.minutes()[0]
	if !r.Full || r.FullTicks != 4 || r.FullStarts != 2 || r.FullLongestS != 15 {
		t.Errorf("row = %+v, want full_ticks 4, starts 2, longest 15", r)
	}
}

// A run that goes on across the minute boundary: the second minute has no new
// start, its ticks count, and the longest is the whole run so far.
func TestMinutes_FullRunAcrossAMinute(t *testing.T) {
	f := newMinuteFix(t)
	on := resources.HostUse{Full: true, NCPU: 10}
	f.ticks(8, resources.HostUse{NCPU: 10}) // 40 s idle
	f.ticks(8, on)                          // 40 s full: 20 s in minute 1, 20 s in minute 2
	f.ticks(6, resources.HostUse{NCPU: 10})
	rows := f.minutes()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	m1 := rows[0]
	if m1.FullStarts != 1 || m1.FullTicks != 4 || m1.FullLongestS != 20 {
		t.Errorf("minute 1 = %+v", m1)
	}
	f.ticks(12, resources.HostUse{NCPU: 10})
	m2 := f.minutes()[1]
	if m2.FullStarts != 0 || m2.FullTicks != 4 || m2.FullLongestS != 40 {
		t.Errorf("minute 2 = %+v (the run began in minute 1 and lasted 40 s)", m2)
	}
}

// held, heavy_held, Σ charge, waiting and unleased: two held leases (35 and
// 15) and one waiting.
func TestMinutes_LeaseFigures(t *testing.T) {
	f := newMinuteFix(t)
	f.heldLease("big", resources.ScopeProcess, 100)
	f.heldLease("small", resources.ScopeProcess, 101)
	if _, err := f.m.store.db.Exec(`UPDATE resource_leases SET weight = 35 WHERE id = 'big'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`UPDATE resource_leases SET weight = 15 WHERE id = 'small'`); err != nil {
		t.Fatal(err)
	}
	f.waiting("w", f.nowMS()+30000)
	f.m.setLeaseUse(map[string]resources.LeaseUsage{"big": {CPU: 20, Use: 20}, "small": {CPU: 5, Use: 5}})
	f.tick(resources.HostUse{Measured: 60, NCPU: 10}) // both in warmup → charge 35 + 15 = 50; unleased 60 − 25 = 35
	f.ticks(12, resources.HostUse{NCPU: 10})
	r := f.minutes()[0]
	if r.Held != 2 || r.HeavyHeld != 1 || r.Waiting != 1 || r.SumCharge != 50 || r.Unleased != 35 {
		t.Errorf("row = %+v", r)
	}
	// With the heavy threshold lowered to 10 both count.
	f.set.set(resources.Settings{Mode: resources.ModeLease, HeavyMinWeight: ptr(10)})
	f.ticks(12, resources.HostUse{NCPU: 10})
	if r := f.minutes()[1]; r.HeavyHeld != 2 {
		t.Errorf("heavy_held with heavy_min_weight 10 = %d, want 2", r.HeavyHeld)
	}
}

func ptr(n int) *int { return &n }

// A failing write loses that minute, is logged once per run of failures, and
// the next minute is written; a nil store adds nothing and does not panic.
func TestMinutes_WriteFailureLoggedOnceAndRecovers(t *testing.T) {
	f := newMinuteFix(t)
	if _, err := f.m.store.db.Exec(`CREATE TRIGGER minutes_fail BEFORE INSERT ON host_minutes
		BEGIN SELECT RAISE(ABORT, 'disk'); END`); err != nil {
		t.Fatal(err)
	}
	f.ticks(36, resources.HostUse{NCPU: 10}) // three minutes pass, flushes of the first two fail
	if n := f.logs.count("host timeline"); n != 1 {
		t.Fatalf("failure log lines = %d, want 1", n)
	}
	if _, err := f.m.store.db.Exec(`DROP TRIGGER minutes_fail`); err != nil {
		t.Fatal(err)
	}
	f.ticks(12, resources.HostUse{NCPU: 10})
	rows := f.minutes()
	if len(rows) != 1 || rows[0].At != time.Date(2026, 10, 9, 12, 2, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("rows = %+v, want only minute 12:02 (the failed ones are lost, not retried)", rows)
	}
	if f.logs.count("recovered") < 1 {
		t.Error("recovery not logged")
	}
	m := newTestModule(idleSampler(), nil)
	m.noteMinute(resources.HostUse{Full: true}, time.Now()) // store == nil
}

// Ended rows and minute rows go at the same retention (14 days): boot prunes both.
func TestMinutes_PrunedWithTheRetention(t *testing.T) {
	f := newMinuteFix(t)
	now := f.clock.now()
	old := now.Add(-retention - time.Hour).Truncate(time.Minute).UnixMilli()
	fresh := now.Add(-retention + time.Hour).Truncate(time.Minute).UnixMilli()
	for _, at := range []int64{old, fresh} {
		if err := f.m.store.InsertMinute(minuteRow{At: at, NCPU: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if retention != 14*24*time.Hour {
		t.Fatalf("retention = %v", retention)
	}
	f.m.boot()
	rows := f.minutes()
	if len(rows) != 1 || rows[0].At != fresh {
		t.Fatalf("rows after boot prune = %+v", rows)
	}
}

// The tick feeds the timeline.
func TestMinutes_TickFeedsTheTimeline(t *testing.T) {
	f := newMinuteFix(t)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), nil, nil
	}}
	for i := 0; i < 13; i++ {
		f.m.tick(context.Background())
		f.advance(f.m.interval)
	}
	if rows := f.minutes(); len(rows) != 1 || rows[0].NCPU != 10 {
		t.Fatalf("rows = %+v", rows)
	}
}

// The alpha.610 database (its schema, dumped read-only from mlab, with rows
// of its kind) opens, gains the D-8 columns and the timeline table, keeps its
// rows, and reads every old row as "not recorded".
func TestMigration_FromTheAlpha610Schema(t *testing.T) {
	schema, err := os.ReadFile("testdata/resources-alpha610.schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "resources.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO resource_leases (id, client_id, state, kind, weight, holder_pid, scope, created_at, deadline_at, lease_until, granted_at, ended_at, overrun, would_wait, end_reason, waited_ms)
		VALUES ('old1','c1','ended','test-full',35,10,'process',1,2,3,4,5,1,1,'released',1234),
		       ('old2','c2','held','build',35,11,'process',1,2,3,4,NULL,0,0,NULL,NULL)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	for pass := 0; pass < 2; pass++ { // the second open is a no-op
		s, err := openLeaseStore(path)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		have, err := columnSet(s.db, "resource_leases")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range decisionColumns {
			if !have[c.name] {
				t.Errorf("pass %d: column %s missing", pass, c.name)
			}
		}
		var recorded, path_, wouldR2 = 0, "", 0
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*), SUM(dec_recorded), MIN(dec_path), SUM(would_wait_r2) FROM resource_leases`).Scan(&n, &recorded, &path_, &wouldR2); err != nil {
			t.Fatal(err)
		}
		if n != 2 || recorded != 0 || path_ != "" || wouldR2 != 0 {
			t.Errorf("pass %d: rows %d recorded %d path %q r2 %d", pass, n, recorded, path_, wouldR2)
		}
		r, ok, err := s.Get("old1")
		if err != nil || !ok || r.WaitedMS != 1234 || !r.Overrun || r.EndReason != "released" {
			t.Errorf("pass %d: old row = %+v %v %v", pass, r, ok, err)
		}
		if err := s.InsertMinute(minuteRow{At: 60000, NCPU: 4}); err != nil {
			t.Errorf("pass %d: %v", pass, err)
		}
		s.Close()
	}
}
