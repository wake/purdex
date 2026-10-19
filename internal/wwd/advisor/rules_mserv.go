package advisor

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The thresholds of "mserv service" (spec §13.3): the production slices' PSI "some avg60" at 10 or more for two minutes, and MemAvailable
// under 1.5 GiB.
const (
	mservPSI        = 10.0
	mservPSIMinutes = 2
	mservMemAvailMB = 1536
)

// MservService is "mserv service" (push), judged on the host it runs on and only on Linux, where the PSI and MemAvailable are read (mserv is
// the Linux host; elsewhere the rule does not apply). It holds when the production PSI some avg60 was ≥ 10 over the last two whole minutes
// and still is, or when MemAvailable is under 1.5 GiB (no duration: the latest reading). A deliberate stress test (a hold with the
// expect_pressure flag) suspends only the PSI part.
//
// The third condition of the spec row, "mserv unreachable ≥ 2 min", is not this rule's: a host cannot report its own absence. It is noticed
// by the other hosts' wwds and told by the deliverer (the silence rules, 8a-3).
type MservService struct{}

func (MservService) Kind() wire.AlertKind { return wire.AlertMservService }

func (MservService) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: "mserv_service", Level: wire.AlertPush}
	f := in.Facts
	if f.Platform != "linux" {
		return v, nil
	}
	if f.Mem.TotalMB > 0 && f.Mem.AvailableMB < mservMemAvailMB {
		v.Holds, v.Detail = true, fmt.Sprintf("MemAvailable 只剩 %d MB", f.Mem.AvailableMB)
		return v, nil
	}
	if suspendsPSI(f.Exception) {
		return v, nil
	}
	if in.Minutes == nil {
		return Verdict{}, errors.New("no minute history is available for the PSI part")
	}
	if f.Mem.PSI == nil || f.Mem.PSI.Some60 < mservPSI {
		return v, nil // unreadable PSI is unknown, not high; and the latest reading must still be high
	}
	recs := in.Minutes(in.Now.Add(-(mservPSIMinutes + 2) * time.Minute))
	if len(recs) < mservPSIMinutes {
		return v, nil
	}
	win := recs[len(recs)-mservPSIMinutes:]
	for i, r := range win {
		if !minuteFull(r) || r.Mem.PSI == nil || r.Mem.PSI.Some60 < mservPSI || (i > 0 && r.At.Sub(win[i-1].At) != time.Minute) {
			return v, nil
		}
	}
	v.Holds, v.Detail = true, fmt.Sprintf("PSI some avg60 在 %.0f 以上已 %d 分鐘（現在 %.1f）", mservPSI, mservPSIMinutes, f.Mem.PSI.Some60)
	return v, nil
}

// suspendsPSI says whether a hold with the expect_pressure flag is on.
func suspendsPSI(excs []wire.FactExc) bool {
	for _, e := range excs {
		if slices.Contains(e.Flags, wire.HoldFlagExpectPressure) {
			return true
		}
	}
	return false
}
