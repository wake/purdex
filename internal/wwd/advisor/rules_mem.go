package advisor

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// pressureMinutes is how long the zone must have stayed at warning or worse, and pressureSwapMB how much the swap in use must have grown
// over it (spec §13.3 "pressure held").
const (
	pressureMinutes = 5
	pressureSwapMB  = 1024
)

func zoneHigh(z wire.Zone) bool { return z == wire.ZoneWarning || z == wire.ZoneDanger }

// minuteHigh says whether a one-minute record spent the whole minute at warning or worse: the per-minute stats count the seconds in each
// zone, so a minute with any normal second is not. A record without stats (written before they existed) has only its worst zone to go on.
func minuteHigh(f wire.Facts) bool {
	if st := f.Stats; st != nil && st.SampledS > 0 {
		return st.NormalS == 0 && st.WarningS+st.DangerS > 0
	}
	return zoneHigh(f.Derived.Zone)
}

// PressureHeld is "pressure held": zone ≥ warning for ≥ 5 min with the swap in use up ≥ 1 GB over those minutes (push). Once raised it
// stands while the zone is still at warning or worse, even if the swap stopped growing, and clears when the zone is normal again.
type PressureHeld struct{}

func (PressureHeld) Kind() wire.AlertKind { return wire.AlertPressureHeld }

func (PressureHeld) Eval(in Input) (Verdict, error) {
	if in.Minutes == nil {
		return Verdict{}, errors.New("no minute history is available")
	}
	v := Verdict{Key: "pressure_held", Level: wire.AlertPush}
	recs := in.Minutes(in.Now.Add(-(pressureMinutes + 1) * time.Minute))
	if len(recs) == 0 {
		return v, nil
	}
	last := recs[len(recs)-1]
	if in.Now.Sub(last.At) > 90*time.Second {
		return v, nil // the newest minute is not now's
	}
	if in.Raised != nil && in.Raised(v.Key) && minuteHigh(last) {
		v.Holds, v.Detail = true, "記憶體壓力持續在 warning 以上"
		return v, nil
	}
	if len(recs) < pressureMinutes {
		return v, nil
	}
	win := recs[len(recs)-pressureMinutes:]
	for i, f := range win {
		if !minuteHigh(f) || (i > 0 && f.At.Sub(win[i-1].At) != time.Minute) {
			return v, nil
		}
	}
	from, to := win[0].Mem.SwapUsedMB, win[len(win)-1].Mem.SwapUsedMB
	if from == nil || to == nil || *to-*from < pressureSwapMB {
		return v, nil
	}
	v.Holds, v.Detail = true, fmt.Sprintf("記憶體壓力在 warning 以上已 %d 分鐘，swap 增加 %d MB", pressureMinutes, *to-*from)
	return v, nil
}

// The thresholds of "thrash" (spec §13.3): the compressor over 4 GiB and decompressing over 80 000 pages/s, for 60 s.
const (
	thrashCompressorMB = 4096
	thrashRate         = 80000
	thrashSpan         = 60 * time.Second
	// thrashSpanSlack is how short of thrashSpan the covered span of the samples may be (a sample is 5 s apart, the clock jitters).
	thrashSpanSlack = 5 * time.Second
)

// Thrash is "thrash" (digest): macOS, compressor > 4 GiB and decompress_rate > 80 000/s for ≥ 60 s. It judges the last 60 s of 5 s samples
// when it has them (Input.Recent); a replay from the minute rollups has only each minute's peak rate, and judges the newest minute by it,
// an upper bound. Where decompress_rate is not read (Linux, a record from before the proto 2 writers) the rule does not apply.
type Thrash struct{}

func (Thrash) Kind() wire.AlertKind { return wire.AlertThrash }

func thrashing(m wire.Mem) bool {
	return m.CompressorMB != nil && *m.CompressorMB > thrashCompressorMB && m.DecompressRate != nil && *m.DecompressRate > thrashRate
}

func (Thrash) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: "thrash", Level: wire.AlertDigest}
	if in.Recent == nil && in.Minutes == nil {
		return Verdict{}, errors.New("no history is available")
	}
	if in.Recent != nil {
		recs := in.Recent(in.Now.Add(-thrashSpan))
		if len(recs) == 0 || recs[len(recs)-1].At.Sub(recs[0].At) < thrashSpan-thrashSpanSlack {
			return v, nil
		}
		for _, f := range recs {
			if !thrashing(f.Mem) {
				return v, nil
			}
		}
		v.Holds = true
		v.Detail = fmt.Sprintf("compressor %d MB，每秒解壓 %.0f 頁，已 %d 秒", *recs[len(recs)-1].Mem.CompressorMB, *recs[len(recs)-1].Mem.DecompressRate, int(thrashSpan.Seconds()))
		return v, nil
	}
	recs := in.Minutes(in.Now.Add(-2 * time.Minute))
	if len(recs) == 0 {
		return v, nil
	}
	last := recs[len(recs)-1]
	if in.Now.Sub(last.At) <= 90*time.Second && thrashing(last.Mem) {
		v.Holds = true
		v.Detail = fmt.Sprintf("compressor %d MB，每秒解壓最高 %.0f 頁", *last.Mem.CompressorMB, *last.Mem.DecompressRate)
	}
	return v, nil
}

// suppressWindow is how far back "swap did not grow" looks for a known resident's noise suppression.
const suppressWindow = 10 * time.Minute

// ResidentGrowth is "resident growth" (digest): a single system or resident process, by its own footprint (never a sum over a group, the
// simulator's processes excluded), ≥ max(4 GB, 25 % of RAM). A process named in Known is not reported while the host has ≥ 50 % memory
// available and the swap in use did not grow over suppressWindow; when either fails, or the swap trend cannot be read, it stands.
type ResidentGrowth struct {
	// Known is the names of the residents whose large footprint is expected (wwd's [advisor] known_residents); empty: none.
	Known []string
}

func (ResidentGrowth) Kind() wire.AlertKind { return wire.AlertResidentGrowth }

// simulatorProc says whether a process belongs to the iOS simulator, by its name: the facts carry no parent, so its app processes under
// test (arbitrary names) are not recognised.
func simulatorProc(name string) bool {
	return strings.HasPrefix(name, "SimLaunchHost") || name == "launchd_sim" || name == "Simulator" || strings.Contains(name, "CoreSimulator")
}

func (r ResidentGrowth) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: "resident_growth", Level: wire.AlertDigest}
	m := in.Facts.Mem
	limit := max(4096, m.TotalMB/4)
	var big []wire.Proc
	for _, p := range in.Facts.Procs {
		if p.Owner == nil || (p.Owner.Kind != wire.OwnerSystem && p.Owner.Kind != wire.OwnerResident) || simulatorProc(p.Name) || p.MemMB < limit {
			continue
		}
		big = append(big, p)
	}
	slices.SortStableFunc(big, func(a, b wire.Proc) int { return b.MemMB - a.MemMB })
	for _, p := range big {
		if slices.Contains(r.Known, p.Name) && r.suppressed(in) {
			continue
		}
		v.Holds, v.Detail = true, fmt.Sprintf("%s 佔 %d MB", p.Name, p.MemMB)
		return v, nil
	}
	return v, nil
}

// suppressed says whether the host is quiet enough for a known resident's size to pass: ≥ 50 % of the memory available, and the swap in
// use not up since suppressWindow ago. Anything it cannot tell is "not quiet".
func (ResidentGrowth) suppressed(in Input) bool {
	m := in.Facts.Mem
	if m.TotalMB <= 0 || m.AvailableMB*2 < m.TotalMB || m.SwapUsedMB == nil || in.Minutes == nil {
		return false
	}
	for _, f := range in.Minutes(in.Now.Add(-suppressWindow)) {
		if f.Mem.SwapUsedMB != nil {
			return *m.SwapUsedMB <= *f.Mem.SwapUsedMB // the oldest reading in the window
		}
	}
	return false
}
