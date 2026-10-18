package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func rec(at time.Time, load, avail float64, pressure int, zone wire.Zone) wire.Facts {
	return wire.Facts{
		V: wire.Proto, At: at,
		CPU:     wire.CPU{NCPU: 10, Load1: load, IdlePct: 100 - load*10},
		Mem:     wire.Mem{AvailableMB: int(avail), Pressure: pressure, PageOutRate: fp(float64(pressure))},
		Derived: wire.Derived{Zone: zone},
	}
}

func fp(v float64) *float64 { return &v }

func TestRingKeepsOneHour(t *testing.T) {
	r := NewRing(720)
	for i := 0; i < 800; i++ {
		r.Add(rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal))
	}
	all := r.Since(time.Time{})
	if len(all) != 720 {
		t.Fatalf("len %d want 720", len(all))
	}
	if !all[0].At.Equal(t0.Add(80 * 5 * time.Second)) {
		t.Fatalf("oldest %v", all[0].At)
	}
	// Since filters (inclusive) and keeps order
	cut := t0.Add(790 * 5 * time.Second)
	got := r.Since(cut)
	if len(got) != 10 || !got[0].At.Equal(cut) {
		t.Fatalf("since: %d %v", len(got), got[0].At)
	}
	if last, ok := r.Last(); !ok || !last.At.Equal(t0.Add(799*5*time.Second)) {
		t.Fatalf("last %v %v", last.At, ok)
	}
	if _, ok := NewRing(3).Last(); ok {
		t.Fatal("empty ring has no last")
	}
}

func TestRollupMinute(t *testing.T) {
	min := []wire.Facts{
		rec(t0, 1, 900, 1, wire.ZoneNormal),
		rec(t0.Add(5*time.Second), 3, 400, 2, wire.ZoneWarning),
		rec(t0.Add(10*time.Second), 2, 700, 1, wire.ZoneNormal),
	}
	got := Rollup(min)
	if !got.At.Equal(t0) {
		t.Errorf("at %v: a rollup is stamped with the start of its minute", got.At)
	}
	if got.CPU.Load1 != 2 { // mean
		t.Errorf("load1 %v want 2", got.CPU.Load1)
	}
	if got.Mem.AvailableMB != 400 { // worst = min
		t.Errorf("available %d want 400", got.Mem.AvailableMB)
	}
	if got.Mem.Pressure != 2 { // worst = max
		t.Errorf("pressure %d want 2", got.Mem.Pressure)
	}
	if got.Mem.PageOutRate == nil || *got.Mem.PageOutRate != 2 { // max
		t.Errorf("page_out_rate %v", got.Mem.PageOutRate)
	}
	if got.Derived.Zone != wire.ZoneWarning { // worst zone of the minute
		t.Errorf("zone %v", got.Derived.Zone)
	}
}

func TestRollupBucketsByMinute(t *testing.T) {
	var recs []wire.Facts
	for i := 0; i < 36; i++ { // 3 minutes of 5 s records starting on a minute edge
		recs = append(recs, rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal))
	}
	got := RollupAll(recs)
	if len(got) != 3 {
		t.Fatalf("%d rollups want 3", len(got))
	}
	for i, g := range got {
		if !g.At.Equal(t0.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("rollup %d at %v", i, g.At)
		}
	}
}

func TestRollupZoneOrderingAndUnknown(t *testing.T) {
	if got := Rollup([]wire.Facts{rec(t0, 1, 1, 1, wire.ZoneNormal), rec(t0, 1, 1, 4, wire.ZoneDanger), rec(t0, 1, 1, 1, wire.ZoneWarning)}); got.Derived.Zone != wire.ZoneDanger {
		t.Errorf("danger lost: %v", got.Derived.Zone)
	}
	// unknown never hides a real zone
	if got := Rollup([]wire.Facts{rec(t0, 1, 1, 1, wire.ZoneUnknown), rec(t0, 1, 1, 1, wire.ZoneWarning)}); got.Derived.Zone != wire.ZoneWarning {
		t.Errorf("unknown masked warning: %v", got.Derived.Zone)
	}
	if got := Rollup(nil); !got.At.IsZero() {
		t.Errorf("empty rollup should be zero value")
	}
}
