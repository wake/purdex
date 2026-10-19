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

// minuteFullS is the sampled seconds a one-minute record needs to stand for the whole minute: a sample is 5 s, so a minute that lost one
// sample still counts. The minute being filled, and a minute the sampler failed in, have fewer, and prove nothing about the seconds they
// did not see.
const minuteFullS = 55

// minuteFull says whether a one-minute record covers (nearly) the whole minute. A record without stats cannot say, so it does not.
func minuteFull(f wire.Facts) bool { return f.Stats != nil && f.Stats.SampledS >= minuteFullS }

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
	recs := in.Minutes(in.Now.Add(-(pressureMinutes + 3) * time.Minute))
	if len(recs) == 0 {
		return v, nil
	}
	last := recs[len(recs)-1]
	if in.Now.Sub(last.At) > 90*time.Second {
		return v, nil // the newest minute is not now's
	}
	if in.Raised != nil && in.Raised(v.Key) && pressureStands(recs, in.Now) {
		v.Holds, v.Detail = true, "記憶體壓力持續在 warning 以上"
		return v, nil
	}
	// to start, five whole minutes are needed: the minute still being filled (or one that lost samples) is left out, so the alert cannot
	// fire before the pressure has lasted five minutes, nor across seconds nobody sampled
	if !minuteFull(last) {
		recs = recs[:len(recs)-1]
	}
	// a minute's swap reading is the swap at its end, so five minutes of growth is the reading at the end of the fifth minus the one at the
	// end of the minute before the window: six readings, five intervals
	if len(recs) < pressureMinutes+1 {
		return v, nil
	}
	base := recs[len(recs)-pressureMinutes-1]
	win := recs[len(recs)-pressureMinutes:]
	for i, f := range win {
		prev := base
		if i > 0 {
			prev = win[i-1]
		}
		if !minuteFull(f) || !minuteHigh(f) || f.At.Sub(prev.At) != time.Minute {
			return v, nil
		}
	}
	from, to := base.Mem.SwapUsedMB, win[len(win)-1].Mem.SwapUsedMB
	if from == nil || to == nil || *to-*from < pressureSwapMB {
		return v, nil
	}
	v.Holds, v.Detail = true, fmt.Sprintf("記憶體壓力在 warning 以上已 %d 分鐘，swap 增加 %d MB", pressureMinutes, *to-*from)
	return v, nil
}

// pressureStands says whether a raised "pressure held" is still true: the newest whole minute (within 150 s: it ended less than a minute
// and a half ago) was spent at warning or worse, and the minute being filled has not shown a normal second. A minute that lost its samples
// proves nothing, so it cannot keep the alert up: with no recent whole minute the answer is no.
func pressureStands(recs []wire.Facts, now time.Time) bool {
	for i := len(recs) - 1; i >= 0; i-- {
		if !minuteFull(recs[i]) {
			continue
		}
		if now.Sub(recs[i].At) > 150*time.Second || !minuteHigh(recs[i]) {
			return false
		}
		newest := recs[len(recs)-1]
		return i == len(recs)-1 || newest.Stats == nil || newest.Stats.NormalS == 0
	}
	return false
}

// The thresholds of "thrash" (spec §13.3): the compressor over 4 GiB and decompressing over 80 000 pages/s, for 60 s.
const (
	thrashCompressorMB = 4096
	thrashRate         = 80000
	thrashSpan         = 60 * time.Second
	// thrashSpanSlack is how short of thrashSpan the covered span of the samples may be (a sample is 5 s apart, the clock jitters).
	thrashSpanSlack = 5 * time.Second
	// thrashMaxGap is the most two neighbouring samples may be apart: one missed sample (2 × 5 s) is fine, more is a hole.
	thrashMaxGap = 2 * wire.SampleIntervalS * time.Second
)

// Thrash is "thrash" (digest): macOS, compressor > 4 GiB and decompress_rate > 80 000/s for ≥ 60 s, judged on the last 60 s of 5 s samples
// (Input.Recent). It cannot be judged from the minute rollups, which keep each field's own peak (the two peaks need not share a sample) and
// say nothing of how long: without the 5 s samples (a replay) it is unjudged, never an estimate. decompress_rate is a proto 2 field, written
// only while the writer gate is open (spec §13.1); on macOS a sample without it means the gate is shut, which is unjudged too (the alert is
// left as it was), and on Linux, where it is never read, the rule does not apply.
type Thrash struct{}

func (Thrash) Kind() wire.AlertKind { return wire.AlertThrash }

func thrashing(m wire.Mem) bool {
	return m.CompressorMB != nil && *m.CompressorMB > thrashCompressorMB && m.DecompressRate != nil && *m.DecompressRate > thrashRate
}

func (Thrash) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: "thrash", Level: wire.AlertDigest}
	if in.Recent == nil {
		return Verdict{}, errors.New("thrash needs the 5 s samples; the minute rollups cannot show a sustained minute")
	}
	recs := in.Recent(in.Now.Add(-thrashSpan))
	if len(recs) == 0 || recs[len(recs)-1].At.Sub(recs[0].At) < thrashSpan-thrashSpanSlack {
		return v, nil
	}
	for i, f := range recs {
		if in.Facts.Platform == "darwin" && f.Mem.DecompressRate == nil {
			return Verdict{}, errors.New("decompress_rate is not recorded (the proto 2 writer gate is shut)")
		}
		// every sample, and no hole between them: two high samples a minute apart (the daemon paused, the clock jumped) are not a minute
		if !thrashing(f.Mem) || (i > 0 && f.At.Sub(recs[i-1].At) > thrashMaxGap) {
			return v, nil
		}
	}
	v.Holds = true
	v.Detail = fmt.Sprintf("compressor %d MB，每秒解壓 %.0f 頁，已 %d 秒", *recs[len(recs)-1].Mem.CompressorMB, *recs[len(recs)-1].Mem.DecompressRate, int(thrashSpan.Seconds()))
	return v, nil
}

// suppressWindow is how far back "swap did not grow" looks for a known resident's noise suppression.
const suppressWindow = 10 * time.Minute

// suppressSlack is how much younger than suppressWindow the oldest reading may be and still stand for the whole window (minutes are 1 min
// apart and the newest is still being filled).
const suppressSlack = 90 * time.Second

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
			if f.At.After(in.Now.Add(-suppressWindow + suppressSlack)) {
				return false // the history does not reach back far enough (a fresh start, a hole): nothing shows the swap did not grow
			}
			return *m.SwapUsedMB <= *f.Mem.SwapUsedMB // the oldest reading in the window
		}
	}
	return false
}
