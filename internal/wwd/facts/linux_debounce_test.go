package facts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

var (
	calmPSI = &wire.PSI{}
	warmPSI = &wire.PSI{Some10: 50} // level 2
	hotPSI  = &wire.PSI{Full10: 50} // level 4
)

func TestLinuxPressureFirstHotReadingWaitsLikeAnyRise(t *testing.T) {
	var p LinuxPressure
	lv, _ := feed(&p, 16384, 12800, 0, hotPSI, 4, lt0)
	// unknown (0) until it has held 10 s, then 4
	want := []int{0, 0, 4, 4}
	for i := range want {
		if lv[i] != want[i] {
			t.Fatalf("first reading is hot: %v, want %v", lv, want)
		}
	}
	// a first reading that recovers before 10 s never publishes the level
	var q LinuxPressure
	a, at := feed(&q, 16384, 12800, 0, hotPSI, 1, lt0)
	b, _ := feed(&q, 16384, 12800, 0, calmPSI, 2, at)
	if a[0] != 0 || b[0] != 1 || b[1] != 1 {
		t.Errorf("a blip at start: %v %v", a, b)
	}
}

func TestLinuxPressureEachNewLevelHasItsOwnClock(t *testing.T) {
	var p LinuxPressure
	_, at := feed(&p, 16384, 12800, 0, calmPSI, 1, lt0)
	// warning for 5 s, then danger: danger has held 5 s, not 10
	w, at := feed(&p, 16384, 12800, 0, warmPSI, 1, at)
	d, at := feed(&p, 16384, 12800, 0, hotPSI, 2, at)
	if w[0] != 1 || d[0] != 1 || d[1] != 1 {
		t.Errorf("danger borrowed the warning clock: %v %v", w, d)
	}
	d2, at := feed(&p, 16384, 12800, 0, hotPSI, 1, at)
	if d2[0] != 4 {
		t.Errorf("danger held 10 s, still %v", d2)
	}
	// falling: 4 -> 2 for 25 s, then 1: calm has not held 30 s
	var q LinuxPressure
	_, at = feed(&q, 16384, 12800, 0, hotPSI, 3, lt0)
	f1, at := feed(&q, 16384, 12800, 0, warmPSI, 5, at)
	f2, _ := feed(&q, 16384, 12800, 0, calmPSI, 4, at)
	if f1[4] != 4 {
		t.Errorf("warning for 25 s is not yet a fall to 2: %v", f1)
	}
	if f2[0] != 4 || f2[2] != 4 {
		t.Errorf("calm borrowed the warning clock: %v", f2)
	}
}
