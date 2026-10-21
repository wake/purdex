package advisor

import (
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The thresholds of spec §13.3, in GB of free space.
const (
	diskLowGB     = 12.0
	diskFallingGB = 14.0
	// diskClearGB is how much above diskLowGB the free space must be before a raised "disk low" clears. The spec gives only the raise
	// threshold; without a margin a disk hovering at 12.0 would raise and clear every pass, and a cleared key may be pushed again at once.
	diskClearGB = 0.5

	fallingGBPerHour = 2.0
	fallingFloorGB   = 10.0
	fallingHorizon   = 6 * time.Hour
	// A raised "disk falling" clears later than it raised (#3926): it stays while the fall is still at least fallingClearGBPerHour and the
	// volume is still under fallingClearFloorGB six hours ahead, so a fall hovering around fallingGBPerHour does not clear and raise every pass.
	fallingClearGBPerHour = 1.5
	fallingClearFloorGB   = 12.0
	// slopeWindow is the span the fall is measured over, and slopeMinSpan the least the history must cover for a slope to count. Three hours
	// reach over several trims of a cache that is cleaned on a timer (the Go build cache gives back about 4.9 GB each time and grows back at 2 to
	// 3 GB/h), whose sawtooth the last hour's two ends read as a fall when the hour ends at the bottom of a tooth.
	slopeWindow  = 3 * time.Hour
	slopeMinSpan = 90 * time.Minute
	// slopeMinSamples is the fewest minutes the slope may stand on: three hours hold about 180, and one with a hole in it (the mount missing for
	// a while, the daemon down) would otherwise extrapolate far-apart points six hours ahead.
	slopeMinSamples = 60
)

func volume(in Input) (wire.Disk, error) {
	d, ok := derived.WorkVolume(in.Facts.Disk, in.WorkRoot) // the same volume the worker's disk floor is measured on
	if !ok {
		return wire.Disk{}, fmt.Errorf("no disk in the facts holds the work root %q", in.WorkRoot)
	}
	return d, nil
}

// DiskLow is "disk low": free < 12 GB on the host's work volume (push).
type DiskLow struct{}

func (DiskLow) Kind() wire.AlertKind { return wire.AlertDiskLow }

func (DiskLow) Eval(in Input) (Verdict, error) {
	d, err := volume(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: "disk_low", Level: wire.AlertPush}
	limit := diskLowGB
	if in.Raised != nil && in.Raised(v.Key) {
		limit += diskClearGB
	}
	if d.FreeGB < limit {
		v.Holds, v.Detail = true, fmt.Sprintf("%s 剩 %.1f GB", d.Mount, d.FreeGB)
	}
	return v, nil
}

// DiskFalling is "disk falling": free < 14 GB, or falling ≥ 2 GB/h with < 10 GB projected within 6 h (digest). The fall is the least-squares
// slope of the last three hours of minutes (#3926). Once raised, it stays while the fall is ≥ 1.5 GB/h and the projection is < 12 GB.
type DiskFalling struct{}

func (DiskFalling) Kind() wire.AlertKind { return wire.AlertDiskFalling }

func (DiskFalling) Eval(in Input) (Verdict, error) {
	d, err := volume(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: "disk_falling", Level: wire.AlertDigest}
	if d.FreeGB < diskFallingGB {
		v.Holds, v.Detail = true, fmt.Sprintf("%s 剩 %.1f GB", d.Mount, d.FreeGB)
		return v, nil
	}
	rate, ok := fallRate(in, d.Mount)
	if !ok {
		return v, nil // not enough history is no judgement, raised or not
	}
	minRate, floor := fallingGBPerHour, fallingFloorGB
	if in.Raised != nil && in.Raised(v.Key) {
		minRate, floor = fallingClearGBPerHour, fallingClearFloorGB
	}
	if rate >= minRate && d.FreeGB-rate*fallingHorizon.Hours() < floor {
		v.Holds, v.Detail = true, fmt.Sprintf("%s 剩 %.1f GB，每小時少 %.1f GB（3 小時趨勢）", d.Mount, d.FreeGB, rate)
	}
	return v, nil
}

// fallRate is the GB per hour the volume at mount lost over the last slopeWindow of minute records: minus the slope of the least-squares line
// through them, on the clock (hours), not on the record number, so a gap does not bend it. ok is false when there is no history, it covers less than
// slopeMinSpan, it holds fewer than slopeMinSamples records, or the records are all at one moment.
func fallRate(in Input, mount string) (float64, bool) {
	if in.Minutes == nil {
		return 0, false
	}
	var first, last wire.Facts
	var have bool
	type point struct{ x, y float64 } // hours since the oldest record, free GB
	var pts []point
	for _, f := range in.Minutes(in.Now.Add(-slopeWindow)) {
		// only the past, in order: a record after now (the clock stepped back) or out of order is not part of the fall
		if f.At.After(in.Now) || (have && !f.At.After(last.At)) {
			continue
		}
		free, ok := freeOf(f, mount)
		if !ok {
			continue
		}
		if !have {
			first, have = f, true
		}
		last = f
		pts = append(pts, point{f.At.Sub(first.At).Hours(), free})
	}
	if !have || last.At.Sub(first.At) < slopeMinSpan || len(pts) < slopeMinSamples {
		return 0, false
	}
	var sx, sy float64
	for _, p := range pts {
		sx += p.x
		sy += p.y
	}
	n := float64(len(pts))
	mx, my := sx/n, sy/n
	var sxy, sxx float64
	for _, p := range pts {
		sxy += (p.x - mx) * (p.y - my)
		sxx += (p.x - mx) * (p.x - mx)
	}
	if sxx == 0 {
		return 0, false
	}
	return -sxy / sxx, true
}

func freeOf(f wire.Facts, mount string) (float64, bool) {
	for _, d := range f.Disk {
		if d.Mount == mount {
			return d.FreeGB, true
		}
	}
	return 0, false
}

// WorkerCrashLoop is "worker crash loop": ≥ 3 wwd starts within an hour that followed a run that did not end cleanly (push). A start after a planned
// restart (a deploy, systemctl restart, launchctl kickstart: all SIGTERM) is not one (#3843).
type WorkerCrashLoop struct{}

func (WorkerCrashLoop) Kind() wire.AlertKind { return wire.AlertWorkerCrash }

// crashStarts is the number of starts in crashWindow that makes a loop.
const (
	crashStarts = 3
	crashWindow = time.Hour
)

func (WorkerCrashLoop) Eval(in Input) (Verdict, error) {
	if in.UncleanStartsSince == nil {
		return Verdict{}, errors.New("the start log is not available")
	}
	n, err := in.UncleanStartsSince(in.Now.Add(-crashWindow))
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: "worker_crash_loop", Level: wire.AlertPush}
	if n >= crashStarts {
		v.Holds, v.Detail = true, fmt.Sprintf("wwd 一小時內當機後重啟 %d 次", n)
	}
	return v, nil
}
