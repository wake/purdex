package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// psiMinute is a one-minute record of a Linux host: whole minute i, the production PSI "some avg60" it ended with, MemAvailable in MB.
func psiMinute(i int, some60 float64, availMB int) wire.Facts {
	f := minute(i, wire.ZoneNormal, 0)
	f.Platform = "linux"
	f.Mem.AvailableMB = availMB
	f.Mem.PSI = &wire.PSI{Some60: some60}
	return f
}

func linuxInput(recs ...wire.Facts) Input {
	in := minutesInput(recs[len(recs)-1].At.Add(30*time.Second), recs...)
	return in
}

func TestMservService_PSIAtTenForTwoMinutes(t *testing.T) {
	recs := []wire.Facts{psiMinute(0, 2, 9000), psiMinute(1, 10, 9000), psiMinute(2, 14, 9000)}
	v, err := MservService{}.Eval(linuxInput(recs...))
	if err != nil || !v.Holds || v.Key != "mserv_service" || v.Level != wire.AlertPush || !strings.Contains(v.Detail, "PSI") {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestMservService_PSINeedsTwoWholeMinutesAndTheLatestReading(t *testing.T) {
	for name, c := range map[string]struct {
		recs []wire.Facts
		want bool
	}{
		"one minute only":              {[]wire.Facts{psiMinute(0, 2, 9000), psiMinute(1, 2, 9000), psiMinute(2, 12, 9000)}, false},
		"9.9 is under":                 {[]wire.Facts{psiMinute(0, 9.9, 9000), psiMinute(1, 9.9, 9000), psiMinute(2, 9.9, 9000)}, false},
		"two minutes then it dropped":  {[]wire.Facts{psiMinute(0, 11, 9000), psiMinute(1, 11, 9000), psiMinute(2, 3, 9000)}, false},
		"a hole between the minutes":   {[]wire.Facts{psiMinute(0, 11, 9000), psiMinute(2, 11, 9000), psiMinute(3, 11, 9000)}, true}, // 2 and 3 are consecutive
		"minutes 0 and 2 are not next": {[]wire.Facts{psiMinute(0, 11, 9000), psiMinute(2, 11, 9000)}, false},
	} {
		v, err := MservService{}.Eval(linuxInput(c.recs...))
		if err != nil || v.Holds != c.want {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
	// a minute that lost its samples proves nothing about the two minutes
	recs := []wire.Facts{psiMinute(0, 11, 9000), psiMinute(1, 11, 9000), psiMinute(2, 11, 9000)}
	recs[1].Stats.SampledS = 20
	if v, _ := (MservService{}).Eval(linuxInput(recs...)); v.Holds {
		t.Fatal("a minute with 20 s of samples counted")
	}
	// PSI that could not be read (a missing slice) is unknown, not zero and not high
	recs = []wire.Facts{psiMinute(0, 11, 9000), psiMinute(1, 11, 9000), psiMinute(2, 11, 9000)}
	recs[2].Mem.PSI = nil
	if v, _ := (MservService{}).Eval(linuxInput(recs...)); v.Holds {
		t.Fatal("unreadable PSI counted as high")
	}
}

// Live history ends with the minute still being filled: two whole high minutes before it, and a high latest reading, hold. Without the
// open minute set aside the rule would only hold for a moment at each minute's end.
func TestMservService_TheOpenMinuteIsSetAside(t *testing.T) {
	open := psiMinute(3, 12, 9000)
	open.Stats.SampledS = 15 // 15 s into the minute
	recs := []wire.Facts{psiMinute(0, 3, 9000), psiMinute(1, 11, 9000), psiMinute(2, 13, 9000), open}
	in := minutesInput(open.At.Add(15*time.Second), recs...)
	if v, err := (MservService{}).Eval(in); err != nil || !v.Holds {
		t.Fatalf("two whole high minutes and an open one: %+v %v", v, err)
	}
	// only one whole high minute before the open one: not two minutes yet
	recs = []wire.Facts{psiMinute(0, 3, 9000), psiMinute(1, 3, 9000), psiMinute(2, 13, 9000), open}
	if v, _ := (MservService{}).Eval(minutesInput(open.At.Add(15*time.Second), recs...)); v.Holds {
		t.Fatal("one whole high minute and an open one counted as two minutes")
	}
}

// MemAvailable < 1.5 GB needs no duration.
func TestMservService_MemAvailableUnderOneAndAHalfGB(t *testing.T) {
	for avail, want := range map[int]bool{1535: true, 1536: false, 800: true, 9000: false} {
		v, err := MservService{}.Eval(linuxInput(psiMinute(0, 0, avail)))
		if err != nil || v.Holds != want {
			t.Errorf("%d MB: %+v %v", avail, v, err)
		}
		if want && !strings.Contains(v.Detail, "MemAvailable") {
			t.Errorf("detail = %q", v.Detail)
		}
	}
}

// A deliberate stress test (the expect_pressure hold) suspends only the PSI part: low memory still counts.
func TestMservService_ExpectPressureSuspendsOnlyPSI(t *testing.T) {
	psi := []wire.Facts{psiMinute(0, 30, 9000), psiMinute(1, 30, 9000), psiMinute(2, 30, 9000)}
	in := linuxInput(psi...)
	in.Facts.Exception = []wire.FactExc{{Name: "stress", Flags: []wire.HoldFlag{wire.HoldFlagExpectPressure}}}
	if v, _ := (MservService{}).Eval(in); v.Holds {
		t.Fatal("PSI held with an expect_pressure hold on")
	}
	low := linuxInput(psiMinute(0, 30, 900))
	low.Facts.Exception = []wire.FactExc{{Name: "stress", Flags: []wire.HoldFlag{wire.HoldFlagExpectPressure}}}
	if v, _ := (MservService{}).Eval(low); !v.Holds {
		t.Fatal("low MemAvailable was suspended by an expect_pressure hold")
	}
	other := linuxInput(psi...)
	other.Facts.Exception = []wire.FactExc{{Name: "release", Flags: []wire.HoldFlag{wire.HoldFlagRelease}}}
	if v, _ := (MservService{}).Eval(other); !v.Holds {
		t.Fatal("a release hold suspended the PSI part")
	}
}

// Where PSI and MemAvailable are Linux's, the rule applies only on Linux: no hold, no error elsewhere.
func TestMservService_OnlyOnLinux(t *testing.T) {
	f := psiMinute(0, 50, 100)
	f.Platform = "darwin"
	v, err := MservService{}.Eval(linuxInput(f))
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

// With no minute history the PSI part cannot be judged, but low memory is the latest reading and still is.
func TestMservService_NoHistory(t *testing.T) {
	f := psiMinute(0, 50, 9000)
	in := Input{Now: t0, Facts: f}
	if _, err := (MservService{}).Eval(in); err == nil {
		t.Fatal("no history and nothing low is not a verdict")
	}
	f.Mem.AvailableMB = 500
	if v, err := (MservService{}).Eval(Input{Now: t0, Facts: f}); err != nil || !v.Holds {
		t.Fatalf("low memory needs no history: %+v %v", v, err)
	}
}
