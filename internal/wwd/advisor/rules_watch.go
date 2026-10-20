package advisor

import (
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The watcher's three pressure rules (spec §13.3, WWD switch S3-2). They are told to the watcher, so they are judged on this host's own
// 5 s samples (Input.Recent, the sampler's LocalRecent) and never on the zone: a host's zone can sit at warning for page-outs alone (air19,
// with a Windows VM paging about 92/s) while mem.pressure is 1.

// SampleRule is a rule that is also judged at every sample, not only at the evaluator's 30 s pass (pressure danger: an alert raised within 5 s
// of the sample that shows it).
type SampleRule interface {
	Rule
	EverySample() bool
}

// pressureState reads one 5 s sample. read is false for a sample with no usable memory reading: the memory source failed (TotalMB 0, see
// Sampler.LocalRecent), macOS gave no level (0), or Linux gave no PSI. Such a sample is a hole: not calm, not pressure. pressure is
// macOS mem.pressure ≥ 2, or the PSI at the warning line (without the available-memory and swap conditions of the zone); danger is level 4,
// or the PSI at the danger line.
func pressureState(f wire.Facts) (read, pressure, danger bool) {
	if f.Mem.TotalMB <= 0 {
		return false, false, false
	}
	if f.Platform == "linux" {
		if f.Mem.PSI == nil {
			return false, false, false
		}
		return true, facts.PSIWarn(*f.Mem.PSI), facts.PSIDanger(*f.Mem.PSI)
	}
	if f.Mem.Pressure <= 0 {
		return false, false, false
	}
	return true, f.Mem.Pressure >= 2, f.Mem.Pressure >= 4
}

const (
	// watchSpan is how long every sample must be a pressure sample; a sample is 5 s apart, so a minute of them is 12 samples whose first and
	// last are 55 s apart (watchSlack), and watchMaxGap is the most two neighbours may be apart (one missed sample is fine, more is a hole).
	watchSpan   = 60 * time.Second
	watchSlack  = wire.SampleIntervalS * time.Second
	watchMaxGap = 2 * wire.SampleIntervalS * time.Second
	// watchEntries entries into pressure within watchEntryWindow also raise it, whatever the length of each.
	watchEntries     = 2
	watchEntryWindow = 30 * time.Minute
	// watchClear is how long without a pressure sample before a raised pressure_watch clears.
	watchClear = 10 * time.Minute

	// dangerLook is how far back an unraised pressure_danger looks for a level-4 sample: one evaluator pass, so a sample is never missed
	// between two passes. dangerClear is how long without one before a raised alert clears.
	dangerLook  = 30 * time.Second
	dangerClear = 2 * time.Minute
)

// PressureWatch is "pressure watch" (level watch): every sample of the last 60 s is a pressure sample, or the host went from calm into
// pressure at least twice in the last 30 minutes. No swap condition. Once raised it clears after 10 minutes with no pressure sample.
type PressureWatch struct{}

func (PressureWatch) Kind() wire.AlertKind { return wire.AlertPressureWatch }

func (PressureWatch) Eval(in Input) (Verdict, error) {
	if in.Recent == nil {
		return Verdict{}, errors.New("pressure watch needs the 5 s samples")
	}
	v := Verdict{Key: "pressure_watch", Level: wire.AlertWatch}
	recs := in.Recent(in.Now.Add(-watchEntryWindow))
	if in.Raised != nil && in.Raised(v.Key) {
		anyRead := false
		for _, f := range recs {
			if f.At.Before(in.Now.Add(-watchClear)) {
				continue
			}
			read, pressure, _ := pressureState(f)
			anyRead = anyRead || read
			if pressure {
				v.Holds, v.Detail = true, "記憶體壓力仍在（最近 10 分鐘內有壓力樣本）"
				return v, nil
			}
		}
		if !anyRead {
			return Verdict{}, errors.New("nothing was read in the last 10 minutes: pressure cannot be shown to be over")
		}
		return v, nil
	}
	if minuteOfPressure(recs, in.Now) {
		v.Holds, v.Detail = true, fmt.Sprintf("記憶體壓力已持續 %d 秒", int(watchSpan.Seconds()))
		return v, nil
	}
	if n := entriesInto(recs); n >= watchEntries {
		v.Holds, v.Detail = true, fmt.Sprintf("30 分鐘內 %d 次進入記憶體壓力", n)
	}
	return v, nil
}

// minuteOfPressure says whether the samples of the last watchSpan are all pressure samples with no hole between them, the newest being now's.
func minuteOfPressure(recs []wire.Facts, now time.Time) bool {
	var win []wire.Facts
	for _, f := range recs {
		if !f.At.Before(now.Add(-watchSpan)) {
			win = append(win, f)
		}
	}
	if len(win) == 0 || now.Sub(win[len(win)-1].At) > watchMaxGap || win[len(win)-1].At.Sub(win[0].At) < watchSpan-watchSlack {
		return false
	}
	for i, f := range win {
		if _, pressure, _ := pressureState(f); !pressure || (i > 0 && f.At.Sub(win[i-1].At) > watchMaxGap) {
			return false
		}
	}
	return true
}

// entriesInto counts the times a pressure sample directly follows a calm one (neither a hole, no gap between them).
func entriesInto(recs []wire.Facts) int {
	n := 0
	for i := 1; i < len(recs); i++ {
		prevRead, prevPressure, _ := pressureState(recs[i-1])
		read, pressure, _ := pressureState(recs[i])
		if prevRead && read && !prevPressure && pressure && recs[i].At.Sub(recs[i-1].At) <= watchMaxGap {
			n++
		}
	}
	return n
}

// PressureDanger is "pressure danger" (push, and the watcher): macOS mem.pressure 4 in any sample, or Linux PSI at the danger line (full10 ≥ 10
// or some60 ≥ 25). It is judged at every sample (SampleRule). Once raised it clears after 2 minutes with no such sample.
type PressureDanger struct{}

func (PressureDanger) Kind() wire.AlertKind { return wire.AlertPressureDanger }

func (PressureDanger) EverySample() bool { return true }

func (PressureDanger) Eval(in Input) (Verdict, error) {
	if in.Recent == nil {
		return Verdict{}, errors.New("pressure danger needs the 5 s samples")
	}
	v := Verdict{Key: "pressure_danger", Level: wire.AlertPush}
	raised := in.Raised != nil && in.Raised(v.Key)
	look := dangerLook
	if raised {
		look = dangerClear
	}
	anyRead := false
	var worst *wire.Facts
	recs := in.Recent(in.Now.Add(-look))
	for i := range recs {
		read, _, danger := pressureState(recs[i])
		anyRead = anyRead || read
		if danger {
			worst = &recs[i]
		}
	}
	if worst != nil {
		v.Holds, v.Detail = true, dangerDetail(*worst)
		return v, nil
	}
	if raised && !anyRead {
		return Verdict{}, errors.New("nothing was read in the last 2 minutes: the danger cannot be shown to be over")
	}
	return v, nil
}

func dangerDetail(f wire.Facts) string {
	if f.Platform == "linux" && f.Mem.PSI != nil {
		return fmt.Sprintf("記憶體壓力危急（PSI full10 %.1f，some60 %.1f）", f.Mem.PSI.Full10, f.Mem.PSI.Some60)
	}
	return fmt.Sprintf("記憶體壓力危急（等級 %d）", f.Mem.Pressure)
}

const (
	// swapWindow is the span the swap growth is measured over, on whole-minute records (the last reading minus the first); the first may be
	// swapJitter older than the window and the history must cover swapMinSpan of it. The newest record must be now's (swapFresh).
	swapWindow  = 30 * time.Minute
	swapJitter  = 30 * time.Second
	swapMinSpan = 28 * time.Minute // the newest whole minute ended up to 90 s ago and the oldest may be 30 s older than the window
	swapFresh   = 90 * time.Second
	// swapBaseMB is the growth that raises it up to swapBaseRAMMB of RAM; above that it grows in proportion (24 GB: 750 MB).
	swapBaseMB    = 500
	swapBaseRAMMB = 16384
)

// SwapThresholdMB is the growth over 30 minutes that raises swap_growth on a host with totalMB of RAM: 500 MB up to 16 GB, in proportion above
// it. override > 0 (the host's [advisor] swap_growth_mb) replaces it.
func SwapThresholdMB(totalMB, override int) int {
	switch {
	case override > 0:
		return override
	case totalMB <= swapBaseRAMMB:
		return swapBaseMB
	}
	return totalMB * swapBaseMB / swapBaseRAMMB
}

// SwapGrowth is "swap growth" (level watch): the swap in use grew by at least the threshold over the last 30 minutes (whole-minute records,
// the newest reading minus the oldest in the window). Once raised it clears when the growth falls under half the threshold.
type SwapGrowth struct {
	// ThresholdMB is [advisor] swap_growth_mb; 0: by RAM (SwapThresholdMB).
	ThresholdMB int
}

func (SwapGrowth) Kind() wire.AlertKind { return wire.AlertSwapGrowth }

func (r SwapGrowth) Eval(in Input) (Verdict, error) {
	if in.Minutes == nil {
		return Verdict{}, errors.New("swap growth needs the minute history")
	}
	v := Verdict{Key: "swap_growth", Level: wire.AlertWatch}
	raised := in.Raised != nil && in.Raised(v.Key)
	var first, last *wire.Facts
	recs := in.Minutes(in.Now.Add(-swapWindow - swapJitter))
	for i := range recs {
		// whole minutes only (the history includes the minute being filled, and a record without stats cannot say what it saw)
		if recs[i].At.After(in.Now) || recs[i].Mem.SwapUsedMB == nil || !minuteFull(recs[i]) {
			continue
		}
		if first == nil {
			first = &recs[i]
		}
		last = &recs[i]
	}
	if first == nil || in.Now.Sub(last.At) > swapFresh || last.At.Sub(first.At) < swapMinSpan {
		// the history does not show 30 minutes of swap (a fresh start, a hole, a stalled sampler): nothing is raised, and a raised alert is
		// left as it was rather than cleared on no evidence
		if raised {
			return Verdict{}, errors.New("the minute history does not cover the last 30 minutes of swap")
		}
		return v, nil
	}
	total := last.Mem.TotalMB
	if total <= 0 {
		total = in.Facts.Mem.TotalMB
	}
	limit := SwapThresholdMB(total, r.ThresholdMB)
	if raised {
		limit /= 2
	}
	grew := *last.Mem.SwapUsedMB - *first.Mem.SwapUsedMB
	if grew >= limit {
		v.Holds, v.Detail = true, fmt.Sprintf("swap 在 30 分鐘內增加 %d MB（門檻 %d MB）", grew, SwapThresholdMB(total, r.ThresholdMB))
	}
	return v, nil
}
