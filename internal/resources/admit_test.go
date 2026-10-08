package resources

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func lease(id string, w int, age time.Duration, measured float64, samples int) Lease {
	return Lease{ID: id, Weight: w, GrantedAt: t0.Add(-age), Measured: measured, Samples: samples}
}

func waiter(id string, w int) Waiter {
	return Waiter{ID: id, Weight: w, EnqueuedAt: t0.Add(-time.Second)}
}

func ids(gs []Grant) string {
	s := ""
	for i, g := range gs {
		if i > 0 {
			s += ","
		}
		s += g.ID
	}
	return s
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

// Plan P1-1b: the table of the R9 rule. host gives measured / full; leases are
// held for an hour (past warmup) unless a row says so.
func TestAdmit_Table(t *testing.T) {
	s := DefaultSettings()
	long := time.Hour
	for name, c := range map[string]struct {
		host    HostUse
		leases  []Lease
		use     map[string]LeaseUsage
		waiters []Waiter
		want    string
	}{
		"empty pool, idle host grants": {HostUse{Measured: 5}, nil, nil, []Waiter{waiter("w1", 35)}, "w1"},
		// The P0 night: nothing leased and the host at 80 measured; under R9
		// that does not hold a test-full back (it did under the old rule).
		"busy host baseline is not a term": {HostUse{Measured: 80}, nil, nil, []Waiter{waiter("w1", 35)}, "w1"},
		"fits but the host is full waits":  {HostUse{Measured: 80, Full: true}, nil, nil, []Waiter{waiter("w1", 35)}, ""},
		"two test-full on a 35+35 pool": {HostUse{}, []Lease{lease("a", 35, long, 35, 5)}, nil,
			[]Waiter{waiter("w1", 35), waiter("w2", 35)}, "w1"}, // 35 + 35 = 70, then 70 + 35 > 100
		"over 100 waits": {HostUse{}, []Lease{lease("a", 35, long, 35, 5), lease("b", 35, long, 35, 5)}, nil,
			[]Waiter{waiter("w1", 35)}, ""},
		"a light later waiter passes a heavy one that does not fit": {HostUse{}, []Lease{lease("a", 35, long, 60, 5)}, nil,
			[]Waiter{waiter("heavy", 45), waiter("light", 10)}, "light"},
		"fifo when both fit":            {HostUse{}, nil, nil, []Waiter{waiter("w1", 35), waiter("w2", 35)}, "w1,w2"},
		"oversize with nothing active":  {HostUse{}, nil, nil, []Waiter{waiter("big", 150)}, "big"},
		"oversize with a lease waits":   {HostUse{}, []Lease{lease("a", 10, long, 1, 5)}, nil, []Waiter{waiter("big", 150)}, ""},
		"oversize on a full host waits": {HostUse{Full: true}, nil, nil, []Waiter{waiter("big", 150)}, ""},
		"two oversize on an empty pool: only the first": {HostUse{}, nil, nil,
			[]Waiter{waiter("big1", 150), waiter("big2", 150)}, "big1"},
		"a low charge with a lease active: oversize still waits": {HostUse{}, []Lease{lease("a", 10, long, 0.1, 5)}, nil,
			[]Waiter{waiter("big", 150)}, ""},
		"a lease in warmup counts its weight": {HostUse{}, []Lease{lease("a", 60, time.Second, 1, 5)}, nil,
			[]Waiter{waiter("w1", 45)}, ""},
	} {
		got := ids(Admit(c.host, c.leases, c.use, c.waiters, t0, s))
		if got != c.want {
			t.Errorf("%s: granted %q, want %q", name, got, c.want)
		}
	}
}

// A deadline grants whatever the state; the grant counts as active for the
// rest of the pass, so an oversize waiter behind it waits.
func TestAdmit_OverrunAndOversizeAfterIt(t *testing.T) {
	s := DefaultSettings()
	late := Waiter{ID: "late", Weight: 35, EnqueuedAt: t0.Add(-6 * time.Minute), Deadline: t0.Add(-time.Second)}
	gs := Admit(HostUse{Measured: 99, Full: true}, nil, nil, []Waiter{late, waiter("big", 150)}, t0, s)
	if ids(gs) != "late" || !gs[0].Overrun || gs[0].Path != PathOverrun || gs[0].WaitedMS != 360000 {
		t.Fatalf("grants = %+v", gs)
	}
	// Exactly at the deadline counts.
	at := Waiter{ID: "at", Weight: 35, EnqueuedAt: t0.Add(-time.Minute), Deadline: t0}
	if gs := Admit(HostUse{Full: true}, nil, nil, []Waiter{at}, t0, s); ids(gs) != "at" {
		t.Errorf("a waiter at its deadline: %+v", gs)
	}
}

func TestAdmit_DecisionRecordsTheState(t *testing.T) {
	s := DefaultSettings()
	host := HostUse{Measured: 69, Load1: 6.9, NCPU: 10, Mem: 40}
	leases := []Lease{lease("a", 35, time.Hour, 20, 5)}
	use := map[string]LeaseUsage{"a": {CPU: 25, Use: 25}}
	fresh := Waiter{ID: "w1", Weight: 35, EnqueuedAt: t0, Fresh: true}
	old := Waiter{ID: "w2", Weight: 10, EnqueuedAt: t0.Add(-4 * time.Second)}
	gs := Admit(host, leases, use, []Waiter{fresh, old}, t0, s)
	if len(gs) != 2 {
		t.Fatalf("grants = %+v", gs)
	}
	d := gs[0].Decision
	// sum 20 (the average), unleased 69 − 25 = 44; R9: 20 + 35 ≤ 100 grants,
	// the pre-R9 formula 20 + 44 + 35 = 99 ≤ 100 would have too.
	want := Decision{Path: PathImmediate, Load1: 6.9, NCPU: 10, Mem: 40, Measured: 69, Weight: 35, SumCharge: 20, Unleased: 44, WouldWaitR2: false}
	if d != want {
		t.Errorf("decision 1 = %+v\nwant %+v", d, want)
	}
	// The second sees the first's weight in the sum: 20 + 35 = 55; the old formula 55 + 44 + 10 = 109 > 100.
	d2 := gs[1].Decision
	if d2.Path != PathWaited || d2.WaitedMS != 4000 || d2.SumCharge != 55 || !d2.WouldWaitR2 {
		t.Errorf("decision 2 = %+v", d2)
	}
}

// The P0 night as a fixture: a busy baseline of 80 with one test-full held
// (measured 20): R9 grants a second at once while the pre-R9 rule would have
// made it wait.
func TestAdmit_WouldWaitR2WhileR9Grants(t *testing.T) {
	s := DefaultSettings()
	gs := Admit(HostUse{Measured: 80}, []Lease{lease("a", 35, time.Hour, 20, 5)}, map[string]LeaseUsage{"a": {CPU: 20, Use: 20}},
		[]Waiter{{ID: "w", Weight: 35, EnqueuedAt: t0, Fresh: true}}, t0, s)
	if len(gs) != 1 || !gs[0].WouldWaitR2 || gs[0].Unleased != 60 {
		t.Fatalf("grants = %+v", gs)
	}
}

func TestAdmit_UnleasedNeverNegativeAndUnavailableHost(t *testing.T) {
	s := DefaultSettings()
	// leaseUse above the host figure: unleased is 0, not negative.
	gs := Admit(HostUse{Measured: 10}, []Lease{lease("a", 35, time.Hour, 30, 5)}, map[string]LeaseUsage{"a": {CPU: 30, Use: 30}},
		[]Waiter{waiter("w", 10)}, t0, s)
	if len(gs) != 1 || gs[0].Unleased != 0 {
		t.Errorf("unleased = %+v", gs)
	}
	// An unavailable sample is an all-zero HostUse: leases still queue against each other.
	if gs := Admit(HostUse{}, []Lease{lease("a", 60, time.Hour, 60, 5)}, nil, []Waiter{waiter("w", 45)}, t0, s); len(gs) != 0 {
		t.Errorf("an unavailable host must not let 60 + 45 through: %+v", gs)
	}
}

// Two leases that stress different resources do not add up by their Use:
// 30 % CPU and 30 % memory leave host.Measured 30 unexplained by neither.
func TestAdmit_UnleasedAddsCPUAndMemSeparately(t *testing.T) {
	s := DefaultSettings()
	leases := []Lease{lease("a", 35, time.Hour, 30, 5), lease("b", 35, time.Hour, 30, 5)}
	use := map[string]LeaseUsage{"a": {CPU: 30, Use: 30}, "b": {Mem: 30, Use: 30}}
	gs := Admit(HostUse{Measured: 40}, leases, use, []Waiter{waiter("w", 10)}, t0, s)
	if len(gs) != 1 || gs[0].Unleased != 10 { // 40 − max(30, 30), not 40 − 60 -> 0
		t.Fatalf("grants = %+v", gs)
	}
}

// A total that is mathematically exactly 100 fits, whatever the float sum says,
// for R9 and for the pre-R9 comparison alike.
func TestAdmit_ExactCapacityFits(t *testing.T) {
	s := DefaultSettings()
	long := time.Hour
	// Charges 0.2 + 64.4 + 15.4 = 80, plus 20 is 100 (summed in float it is 100.00000000000001).
	leases := []Lease{lease("a", 1, long, 0.2, 5), lease("b", 100, long, 64.4, 5), lease("c", 30, long, 15.4, 5)}
	s.FloorPct = intp(0)
	gs := Admit(HostUse{}, leases, nil, []Waiter{waiter("w", 20)}, t0, s)
	if len(gs) != 1 || gs[0].WouldWaitR2 {
		t.Fatalf("grants = %+v", gs)
	}
}

// A waiter that fits when its deadline arrives is granted by the ordinary
// rule: not an overrun (the count means "let through although it did not fit").
func TestAdmit_FitAtDeadlineIsNotAnOverrun(t *testing.T) {
	s := DefaultSettings()
	w := Waiter{ID: "w", Weight: 35, EnqueuedAt: t0.Add(-time.Minute), Deadline: t0}
	gs := Admit(HostUse{}, nil, nil, []Waiter{w}, t0, s)
	if len(gs) != 1 || gs[0].Overrun || gs[0].Path != PathWaited {
		t.Fatalf("grants = %+v", gs)
	}
}
