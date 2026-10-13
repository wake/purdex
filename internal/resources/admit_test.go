package resources

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func lease(id string, w int, age time.Duration, measured float64, samples int) Lease {
	return Lease{ID: id, Weight: w, GrantedAt: t0.Add(-age), Measured: measured, Samples: samples}
}

func TestCharge_WarmupFloorEWMA(t *testing.T) {
	s := DefaultSettings()
	warm := s.Warmup()
	for name, c := range map[string]struct {
		l    Lease
		want float64
	}{
		"inside warmup: the weight":        {lease("a", 35, warm-time.Second, 5, 4), 35},
		"after warmup: the average":        {lease("a", 35, warm+time.Second, 20, 4), 20},
		"the floor holds a low average up": {lease("a", 35, warm+time.Second, 2, 4), 35 * s.Floor()},
		"nothing measured yet: the weight": {lease("a", 35, warm+time.Hour, 0, 0), 35},
	} {
		if got := Charge(c.l, t0, s); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// RecordOf keeps the figures the report compares: Σ charge, the unexplained
// part of the host's load, and the pre-R9 verdict.
func TestRecordOf_Table(t *testing.T) {
	s := DefaultSettings()
	long := time.Hour
	for name, c := range map[string]struct {
		host    HostUse
		leases  []Lease
		use     map[string]LeaseUsage
		weight  int
		wantSum float64
		wantUn  float64
		wantR2  bool
	}{
		"empty pool, idle host":    {HostUse{Measured: 5}, nil, nil, 35, 0, 5, false},
		"busy baseline holds R2":   {HostUse{Measured: 80}, nil, nil, 35, 0, 80, true}, // 80 + 35 > 100
		"sum of the charges":       {HostUse{}, []Lease{lease("a", 35, long, 35, 5), lease("b", 35, long, 35, 5)}, nil, 35, 70, 0, true},
		"fits exactly":             {HostUse{}, []Lease{lease("a", 35, long, 35, 5)}, nil, 65, 35, 0, false},
		"oversize with nothing":    {HostUse{}, nil, nil, 150, 0, 0, false},
		"oversize with a lease":    {HostUse{}, []Lease{lease("a", 10, long, 1, 5)}, nil, 150, 5, 0, true}, // floor 50 % of 10
		"a lease in warmup counts": {HostUse{}, []Lease{lease("a", 60, time.Second, 1, 5)}, nil, 45, 60, 0, true},
	} {
		got := RecordOf(c.host, c.leases, c.use, c.weight, t0, s)
		want := Record{SumCharge: c.wantSum, Unleased: c.wantUn, WouldWaitR2: c.wantR2}
		if got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

// The P0 night as a fixture: a busy baseline of 80 with one test-full held
// (measured 20): the pre-R9 rule would have made a second wait.
func TestRecordOf_P0Night(t *testing.T) {
	r := RecordOf(HostUse{Measured: 80}, []Lease{lease("a", 35, time.Hour, 20, 5)},
		map[string]LeaseUsage{"a": {CPU: 20, Use: 20}}, 35, t0, DefaultSettings())
	if !r.WouldWaitR2 || r.Unleased != 60 || r.SumCharge != 20 {
		t.Fatalf("record = %+v", r)
	}
}

func TestRecordOf_UnleasedNeverNegative(t *testing.T) {
	r := RecordOf(HostUse{Measured: 10}, []Lease{lease("a", 35, time.Hour, 30, 5)},
		map[string]LeaseUsage{"a": {CPU: 30, Use: 30}}, 10, t0, DefaultSettings())
	if r.Unleased != 0 {
		t.Errorf("unleased = %v", r.Unleased)
	}
}

// Two leases that stress different resources do not add up by their Use:
// 30 % CPU and 30 % memory leave host.Measured 40 − max(30, 30) unexplained.
func TestRecordOf_UnleasedAddsCPUAndMemSeparately(t *testing.T) {
	leases := []Lease{lease("a", 35, time.Hour, 30, 5), lease("b", 35, time.Hour, 30, 5)}
	use := map[string]LeaseUsage{"a": {CPU: 30, Use: 30}, "b": {Mem: 30, Use: 30}}
	r := RecordOf(HostUse{Measured: 40}, leases, use, 10, t0, DefaultSettings())
	if r.Unleased != 10 { // not 40 − 60 -> 0
		t.Fatalf("record = %+v", r)
	}
}

// A total that is mathematically exactly 100 fits, whatever the float sum says.
func TestRecordOf_ExactCapacityFits(t *testing.T) {
	long := time.Hour
	// Charges 0.2 + 64.4 + 15.4 = 80, plus 20 is 100 (summed in float it is 100.00000000000001).
	leases := []Lease{lease("a", 1, long, 0.2, 5), lease("b", 100, long, 64.4, 5), lease("c", 30, long, 15.4, 5)}
	s := DefaultSettings()
	s.FloorPct = intp(0)
	if r := RecordOf(HostUse{}, leases, nil, 20, t0, s); r.WouldWaitR2 {
		t.Fatalf("record = %+v", r)
	}
}
