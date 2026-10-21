package advisor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func diskFacts(at time.Time, mount string, free float64) wire.Facts {
	return wire.Facts{At: at, Disk: []wire.Disk{{Mount: "/", FreeGB: 80}, {Mount: mount, FreeGB: free}}}
}

func inputWith(free float64, hist []wire.Facts) Input {
	return Input{
		Now: t0, Facts: diskFacts(t0, "/Users", free), WorkRoot: "/Users/wake/Library/Application Support/Wwd/work",
		Minutes: func(since time.Time) []wire.Facts {
			var out []wire.Facts
			for _, f := range hist {
				if !f.At.Before(since) {
					out = append(out, f)
				}
			}
			return out
		},
	}
}

// history makes one record a minute ending at t0, the free space going from start down at perHour GB/h.
func history(minutes int, start, perHour float64) []wire.Facts {
	var out []wire.Facts
	for i := minutes; i >= 0; i-- {
		at := t0.Add(-time.Duration(i) * time.Minute)
		out = append(out, diskFacts(at, "/Users", start-perHour*float64(minutes-i)/60))
	}
	return out
}

func TestDiskLow_HoldsBelowTwelveAndNeedsHalfAGBToClear(t *testing.T) {
	r := DiskLow{}
	for name, c := range map[string]struct {
		free float64
		prev bool
		want bool
	}{
		"plenty":                     {40, false, false},
		"11.9 GB is low":             {11.9, false, true},
		"exactly 12 is not low":      {12, false, false},
		"12.2 still low once raised": {12.2, true, true},
		"12.5 clears once raised":    {12.5, true, false},
		"12.2 does not raise afresh": {12.2, false, false},
	} {
		in := inputWith(c.free, nil)
		in.Raised = func(key string) bool { return c.prev }
		v, err := r.Eval(in)
		if err != nil || v.Holds != c.want || v.Key != "disk_low" || v.Level != wire.AlertPush {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
	v, _ := r.Eval(inputWith(11.2, nil))
	if !strings.Contains(v.Detail, "11.2") {
		t.Errorf("detail = %q", v.Detail)
	}
}

// The volume is the one that holds the work root: the longest mount that is a prefix of it, whole path components only.
func TestDiskLow_TheVolumeIsTheOneHoldingTheWorkRoot(t *testing.T) {
	in := inputWith(50, nil)
	in.Facts.Disk = []wire.Disk{{Mount: "/", FreeGB: 5}, {Mount: "/Users", FreeGB: 50}, {Mount: "/Users/wake/Library/Application Support/Wwd/work2", FreeGB: 1}}
	v, err := DiskLow{}.Eval(in)
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
	in.Facts.Disk = []wire.Disk{{Mount: "/", FreeGB: 5}}
	if v, _ := (DiskLow{}).Eval(in); !v.Holds {
		t.Fatal("only / holds the work root, and it is low")
	}
	in.Facts.Disk = nil
	if _, err := (DiskLow{}).Eval(in); err == nil {
		t.Fatal("no disk in the facts is not a verdict")
	}
}

func TestDiskFalling_FreeUnderFourteenIsEnough(t *testing.T) {
	v, err := DiskFalling{}.Eval(inputWith(13.9, nil))
	if err != nil || !v.Holds || v.Level != wire.AlertDigest || v.Key != "disk_falling" {
		t.Fatalf("%+v %v", v, err)
	}
	if v, _ := (DiskFalling{}).Eval(inputWith(14, nil)); v.Holds {
		t.Fatal("14 GB with no history is not falling")
	}
}

// ≥ 2 GB/h (over the last three hours) with < 10 GB projected within 6 h.
func TestDiskFalling_TheSlopeAndTheProjection(t *testing.T) {
	// 20 GB now, 3 GB/h: 20 - 18 = 2 < 10 in 6 h
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(180, 29, 3))); !v.Holds {
		t.Fatalf("3 GB/h from 20 GB should hold: %+v", v)
	}
	// 40 GB now, 3 GB/h: 40 - 18 = 22, plenty
	if v, _ := (DiskFalling{}).Eval(inputWith(40, history(180, 49, 3))); v.Holds {
		t.Fatalf("40 GB falling 3 GB/h is not yet a worry: %+v", v)
	}
	// 20 GB now, 1 GB/h: slope below 2
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(180, 23, 1))); v.Holds {
		t.Fatalf("1 GB/h is below the slope: %+v", v)
	}
	// rising
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(180, 5, -5))); v.Holds {
		t.Fatalf("a rising disk: %+v", v)
	}
}

// The slope needs an hour and a half of minutes behind it; a host that just started has no slope.
func TestDiskFalling_NeedsAnHourAndAHalfOfHistory(t *testing.T) {
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(10, 23, 18))); v.Holds {
		t.Fatalf("10 minutes of history gave a slope: %+v", v)
	}
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(80, 24, 3))); v.Holds {
		t.Fatalf("80 minutes of history gave a slope: %+v", v)
	}
	if v, _ := (DiskFalling{}).Eval(inputWith(20, history(100, 25, 3))); !v.Holds {
		t.Fatalf("100 minutes of history is enough for a slope: %+v", v)
	}
}

// A fall measured on two far-apart points with a hole between them, or on records from after now, is not a slope.
func TestDiskFalling_AHoleOrTheFutureIsNotASlope(t *testing.T) {
	// 3 GB/h from 23 GB would hold, but only the two ends are there
	h := history(180, 29, 3)
	sparse := []wire.Facts{h[0], h[1], h[len(h)-1]}
	if v, _ := (DiskFalling{}).Eval(inputWith(20, sparse)); v.Holds {
		t.Fatalf("three records over three hours gave a slope: %+v", v)
	}
	// records after now are ignored: a clock stepped back leaves later minutes in the store
	future := history(180, 29, 3)
	for i := 1; i <= 30; i++ {
		future = append(future, diskFacts(t0.Add(time.Duration(i)*time.Minute), "/Users", 1))
	}
	flat := inputWith(40, append(history(180, 40, 0), future[len(future)-30:]...))
	if v, _ := (DiskFalling{}).Eval(flat); v.Holds {
		t.Fatalf("future records gave a slope: %+v", v)
	}
}

func TestWorkerCrashLoop_ThreeUncleanStartsInAnHour(t *testing.T) {
	for n, want := range map[int]bool{0: false, 2: false, 3: true, 7: true} {
		in := Input{Now: t0, UncleanStartsSince: func(since time.Time) (int, error) {
			if !since.Equal(t0.Add(-time.Hour)) {
				t.Errorf("window starts at %v", since)
			}
			return n, nil
		}}
		v, err := WorkerCrashLoop{}.Eval(in)
		if err != nil || v.Holds != want || v.Key != "worker_crash_loop" || v.Level != wire.AlertPush {
			t.Errorf("%d starts: %+v %v", n, v, err)
		}
	}
	if _, err := (WorkerCrashLoop{}).Eval(Input{Now: t0}); err == nil {
		t.Fatal("no start log is not a verdict")
	}
	if _, err := (WorkerCrashLoop{}).Eval(Input{Now: t0, UncleanStartsSince: func(time.Time) (int, error) { return 0, errors.New("x") }}); err == nil {
		t.Fatal("an unreadable start log is not a verdict")
	}
}
