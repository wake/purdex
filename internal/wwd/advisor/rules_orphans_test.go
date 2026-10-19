package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func orphanInput(r *OrphanReport) Input {
	in := Input{Now: t0}
	if r != nil {
		in.Orphans = func() *OrphanReport { return r }
	}
	return in
}

func devOrphan(pid int, kind string, age time.Duration) OrphanProc {
	return OrphanProc{PID: pid, Name: "node", Kind: kind, Age: age}
}

func TestOrphans_NothingToReportDoesNotHold(t *testing.T) {
	v, err := Orphans{}.Eval(orphanInput(&OrphanReport{At: t0}))
	if err != nil || v.Holds || v.Key != "orphans" || v.Level != wire.AlertDigest {
		t.Fatalf("%+v %v", v, err)
	}
}

// ppid-1 processes under dev paths, older than 2 h: listed in the digest, with their kinds counted.
func TestOrphans_DevOrphansAreADigest(t *testing.T) {
	r := &OrphanReport{At: t0, Procs: []OrphanProc{
		devOrphan(10, "worktree", 3*time.Hour), devOrphan(11, "worktree", 5*time.Hour), devOrphan(12, "playwright", 26*time.Hour),
	}}
	v, err := Orphans{}.Eval(orphanInput(r))
	if err != nil || !v.Holds || v.Level != wire.AlertDigest {
		t.Fatalf("%+v %v", v, err)
	}
	if !strings.Contains(v.Detail, "3") || !strings.Contains(v.Detail, "worktree") || !strings.Contains(v.Detail, "playwright") {
		t.Errorf("detail = %q", v.Detail)
	}
	if len(v.Detail) > wire.MaxAlertDetailBytes {
		t.Errorf("detail is %d bytes", len(v.Detail))
	}
}

// A fork loop becomes a push only once it has held for 10 minutes; before that it is only listed.
func TestOrphans_AForkLoopPushesAfterTenMinutes(t *testing.T) {
	loop := func(since time.Duration) *OrphanReport {
		return &OrphanReport{At: t0, Loops: []ForkLoop{{PID: 77, Name: "sh", Rate: 43, Since: t0.Add(-since)}}}
	}
	if v, _ := (Orphans{}).Eval(orphanInput(loop(9*time.Minute + 59*time.Second))); !v.Holds || v.Level != wire.AlertDigest {
		t.Fatalf("9m59s: %+v", v)
	}
	v, err := Orphans{}.Eval(orphanInput(loop(10 * time.Minute)))
	if err != nil || !v.Holds || v.Level != wire.AlertPush {
		t.Fatalf("10m: %+v %v", v, err)
	}
	if !strings.Contains(v.Detail, "43") {
		t.Errorf("the rate is in the detail: %q", v.Detail)
	}
}

// One loop is enough to push, whatever else is listed; and the detail counts them.
func TestOrphans_ManyLoopsAndOrphans(t *testing.T) {
	r := &OrphanReport{At: t0, Procs: []OrphanProc{devOrphan(1, "scratch", 4*time.Hour)}}
	for i := 0; i < 12; i++ {
		r.Loops = append(r.Loops, ForkLoop{PID: 100 + i, Name: "sh", Rate: 40 + float64(i), Since: t0.Add(-25 * time.Minute)})
	}
	v, _ := Orphans{}.Eval(orphanInput(r))
	if !v.Holds || v.Level != wire.AlertPush || !strings.Contains(v.Detail, "12") || !strings.Contains(v.Detail, "51") {
		t.Fatalf("%+v", v)
	}
	if len(v.Detail) > wire.MaxAlertDetailBytes {
		t.Errorf("detail is %d bytes", len(v.Detail))
	}
}

// A scan nobody renews any more (the sampler stopped) is unjudged: its fork loop would otherwise be pushed on the clock alone.
func TestOrphans_AStaleScanIsUnjudged(t *testing.T) {
	r := &OrphanReport{At: t0.Add(-3 * time.Minute), Loops: []ForkLoop{{PID: 77, Name: "sh", Rate: 43, Since: t0.Add(-time.Hour)}}}
	if _, err := (Orphans{}).Eval(orphanInput(r)); err == nil {
		t.Fatal("a 3-minute-old scan was judged")
	}
	r.At = t0.Add(-time.Minute)
	if v, err := (Orphans{}).Eval(orphanInput(r)); err != nil || !v.Holds || v.Level != wire.AlertPush {
		t.Fatalf("a 1-minute-old scan is current: %+v %v", v, err)
	}
}

// Nothing scanned yet (or a replay, which has no process table) is unjudged: not "no orphans", which would clear the alert.
func TestOrphans_NoScanIsUnjudged(t *testing.T) {
	if _, err := (Orphans{}).Eval(orphanInput(nil)); err == nil {
		t.Fatal("no scan was taken as an empty one")
	}
}
