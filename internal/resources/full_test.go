package resources

import "testing"

func hostFor(load1 float64, ncpu int, mem float64, pressure int) HostUse {
	return HostUse{Load1: load1, NCPU: ncpu, Mem: mem, Pressure: pressure}
}

// P0 acceptance (2026-10-09): load1 sat between 9.5 and 10.5 on a 10-core
// host for ten minutes, so a stateless "load1 >= ncpu" flag flapped. Entering
// follows R5; leaving needs a 10 % margin.
func TestFullLatch_EntersAtR5AndLeavesWithMargin(t *testing.T) {
	steps := []struct {
		name string
		h    HostUse
		want bool
	}{
		{"idle", hostFor(3, 10, 40, 1), false},
		{"just under the line", hostFor(9.99, 10, 40, 1), false},
		{"load1 reaches ncpu", hostFor(10, 10, 40, 1), true},
		{"wobbles under the line, still full", hostFor(9.5, 10, 40, 1), true},
		{"exactly 0.9 x ncpu, still full", hostFor(9, 10, 40, 1), true},
		{"below 0.9 x ncpu, clear", hostFor(8.99, 10, 40, 1), false},
		{"wobbles back to 9.5, stays clear", hostFor(9.5, 10, 40, 1), false},
		{"memory 90 enters", hostFor(1, 10, 90, 1), true},
		{"memory 85 is still full", hostFor(1, 10, 85, 1), true},
		{"memory 80.9 clears", hostFor(1, 10, 80.9, 1), false},
		{"pressure warn enters", hostFor(1, 10, 40, 2), true},
		{"pressure back to normal clears", hostFor(1, 10, 40, 1), false},
	}
	var l FullLatch
	for _, s := range steps {
		if got := l.Update(s.h); got != s.want {
			t.Fatalf("%s: Update = %v, want %v", s.name, got, s.want)
		}
	}
}

func TestFullLatch_ZeroValueIsClearAndNeedsNCPU(t *testing.T) {
	var l FullLatch
	if l.Update(HostUse{}) {
		t.Fatal("an empty reading is not full")
	}
	if l.Update(hostFor(100, 0, 0, 0)) {
		t.Fatal("a reading without a cpu count cannot be judged: not full")
	}
}
