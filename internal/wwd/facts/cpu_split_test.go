package facts

import (
	"math"
	"testing"
)

func near(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }

func TestParseLoadavgThirdColumn(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg("0.07 0.10 0.05 1/200 1234\n")
	if err != nil || l1 != 0.07 || l5 != 0.10 || l15 != 0.05 {
		t.Fatalf("%v %v %v %v", l1, l5, l15, err)
	}
	if _, _, _, err := ParseLoadavg("0.07 0.10\n"); err == nil {
		t.Error("two columns are too few")
	}
}

// user nice system idle iowait irq softirq steal guest guest_nice
const statSample = "cpu  100 10 50 800 40 4 6 20 5 0\n" +
	"cpu0 50 5 25 400 20 2 3 10 0 0\n" +
	"cpu1 50 5 25 400 20 2 3 10 0 0\n" +
	"intr 1\n"

func TestParseCPUStatDetail(t *testing.T) {
	d, err := ParseCPUStatDetail(statSample)
	if err != nil {
		t.Fatal(err)
	}
	// user = user+nice, system = system+irq+softirq, idle = idle+iowait, total = the first eight columns (steal counts, guest does not)
	want := RawCore{User: 110, System: 60, Idle: 840, Total: 1030}
	if d.All != want {
		t.Errorf("all %+v want %+v", d.All, want)
	}
	if len(d.Cores) != 2 || d.Cores[1] != (RawCore{ID: 1, User: 55, System: 30, Idle: 420, Total: 515}) {
		t.Errorf("cores %+v", d.Cores)
	}
}

func TestParseCPUStatDetailSortsCoresByNumber(t *testing.T) {
	// an offline core is missing from the file; the others keep their order by number, not by position
	d, err := ParseCPUStatDetail("cpu 3 0 0 3 0 0 0 0\ncpu10 1 0 0 1 0 0 0 0\ncpu2 2 0 0 2 0 0 0 0\n")
	if err != nil || len(d.Cores) != 2 || d.Cores[0].User != 2 || d.Cores[1].User != 1 {
		t.Fatalf("%+v %v", d.Cores, err)
	}
	if _, err := ParseCPUStatDetail("cpu 1 0 0 1\ncpux 1 0 0 1\n"); err != nil {
		t.Errorf("a line that is not cpu<N> is not a core, and not an error: %v", err)
	}
}

func rawSplit(user, sys, idle uint64, cores ...RawCore) RawCPU {
	return RawCPU{NCPU: len(cores), Busy: user + sys, Total: user + sys + idle, User: user, System: sys, HaveSplit: true, Cores: cores}
}

func TestBuildCPUSplit(t *testing.T) {
	prev := rawSplit(100, 50, 850, RawCore{User: 10, System: 5, Idle: 85, Total: 100}, RawCore{User: 0, System: 0, Idle: 100, Total: 100})
	cur := rawSplit(300, 150, 1550, RawCore{User: 40, System: 15, Idle: 145, Total: 200}, RawCore{User: 0, System: 20, Idle: 180, Total: 200})
	got := BuildCPU(&prev, cur)
	// delta total 1000: user 200, system 100
	if !near(got.UserPct, 20) || !near(got.SystemPct, 10) {
		t.Fatalf("user %v system %v", got.UserPct, got.SystemPct)
	}
	if len(got.Cores) != 2 || !near(got.Cores[0].UserPct, 30) || !near(got.Cores[0].SystemPct, 10) || !near(got.Cores[0].IdlePct, 60) ||
		!near(got.Cores[1].SystemPct, 20) || !near(got.Cores[1].IdlePct, 80) {
		t.Fatalf("cores %+v", got.Cores)
	}
}

func TestBuildCPUSplitIsNullWithoutABaseline(t *testing.T) {
	cur := rawSplit(300, 150, 1550, RawCore{User: 40, System: 15, Idle: 145, Total: 200})
	got := BuildCPU(nil, cur) // first sample
	if got.UserPct != nil || got.SystemPct != nil {
		t.Errorf("first sample: %v %v", got.UserPct, got.SystemPct)
	}
	if len(got.Cores) != 1 || got.Cores[0].UserPct != nil || got.Cores[0].SystemPct != nil || got.Cores[0].IdlePct != nil {
		t.Errorf("first sample cores: %+v", got.Cores)
	}
	same := BuildCPU(&cur, cur) // no time passed
	if same.UserPct != nil || same.Cores[0].UserPct != nil {
		t.Errorf("zero delta: %v %+v", same.UserPct, same.Cores)
	}
}

func TestBuildCPUSplitCounterWentBackwards(t *testing.T) {
	prev := rawSplit(500, 50, 450, RawCore{User: 10, System: 5, Idle: 85, Total: 100}, RawCore{User: 10, System: 5, Idle: 85, Total: 100})
	cur := rawSplit(100, 60, 1000, RawCore{User: 5, System: 5, Idle: 190, Total: 200}, RawCore{User: 20, System: 10, Idle: 170, Total: 200})
	got := BuildCPU(&prev, cur)
	if got.UserPct != nil { // whole-machine user went down: no figure for either share
		t.Errorf("user went backwards: %v", got.UserPct)
	}
	if got.Cores[0].UserPct != nil || got.Cores[0].IdlePct != nil { // core 0's user went down, only that core loses its figures
		t.Errorf("core 0: %+v", got.Cores[0])
	}
	if !near(got.Cores[1].UserPct, 10) || !near(got.Cores[1].IdlePct, 85) {
		t.Errorf("core 1 is fine: %+v", got.Cores[1])
	}
}

func TestBuildCPUSplitCoreCountChanged(t *testing.T) {
	prev := rawSplit(100, 50, 850, RawCore{Total: 100}, RawCore{Total: 100})
	cur := rawSplit(200, 60, 1740, RawCore{User: 1, Idle: 99, Total: 200}, RawCore{Total: 200}, RawCore{Total: 200})
	got := BuildCPU(&prev, cur)
	if len(got.Cores) != 3 {
		t.Fatalf("every core is listed: %+v", got.Cores)
	}
	for i, c := range got.Cores {
		if c.UserPct != nil || c.SystemPct != nil || c.IdlePct != nil {
			t.Errorf("core %d must be null when the core count changed: %+v", i, c)
		}
	}
	if !near(got.UserPct, 10) { // the whole-machine share does not depend on the core count
		t.Errorf("user %v", got.UserPct)
	}
}

// cpu1 goes offline and cpu2 comes online: the count is the same, but index 1 is no longer the same core.
func TestBuildCPUSplitOnlineCoresChanged(t *testing.T) {
	prev := rawSplit(100, 50, 850, RawCore{ID: 0, Total: 100}, RawCore{ID: 1, Total: 100})
	cur := rawSplit(200, 100, 1700, RawCore{ID: 0, User: 10, Idle: 190, Total: 200}, RawCore{ID: 2, User: 10, Idle: 190, Total: 200})
	got := BuildCPU(&prev, cur)
	if !near(got.Cores[0].UserPct, 10) {
		t.Errorf("cpu0 is the same core and is measured: %+v", got.Cores[0])
	}
	if c := got.Cores[1]; c.UserPct != nil || c.SystemPct != nil || c.IdlePct != nil {
		t.Errorf("index 1 changed from cpu1 to cpu2: must be null, got %+v", c)
	}
}

func TestBuildCPUKindsAndLoad15(t *testing.T) {
	l15 := 1.25
	cur := RawCPU{NCPU: 2, Total: 10, Load15: &l15, HaveSplit: true, Cores: []RawCore{{}, {}}, Kinds: []string{"e", "p"}}
	got := BuildCPU(nil, cur)
	if !near(got.Load15, 1.25) || got.Load15 == &l15 {
		t.Errorf("load15 %v", got.Load15)
	}
	if got.Cores[0].Kind == nil || *got.Cores[0].Kind != "e" || *got.Cores[1].Kind != "p" {
		t.Errorf("kinds %+v", got.Cores)
	}
	cur.Kinds = []string{"e"} // the count does not match: no kind is trusted
	if got = BuildCPU(nil, cur); got.Cores[0].Kind != nil || got.Cores[1].Kind != nil {
		t.Errorf("mismatched kinds must be null: %+v", got.Cores)
	}
	if got = BuildCPU(nil, RawCPU{NCPU: 1, Total: 10}); got.Load15 != nil || got.Cores != nil || got.UserPct != nil {
		t.Errorf("a source that gives none of it leaves it null: %+v", got)
	}
}
