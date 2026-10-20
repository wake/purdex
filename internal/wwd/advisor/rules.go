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
	// slopeWindow is the span the fall is measured over, and slopeMinSpan the least the history must cover for a slope to count.
	slopeWindow  = time.Hour
	slopeMinSpan = 30 * time.Minute
	// slopeMinSamples is the fewest minutes the slope may stand on: a hour holds about 60, and one with a hole in it (the mount missing for a
	// while, the daemon down) would otherwise extrapolate two far-apart points six hours ahead.
	slopeMinSamples = 20
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

// DiskFalling is "disk falling": free < 14 GB, or falling ≥ 2 GB/h with < 10 GB projected within 6 h (digest).
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
	if ok && rate >= fallingGBPerHour && d.FreeGB-rate*fallingHorizon.Hours() < fallingFloorGB {
		v.Holds, v.Detail = true, fmt.Sprintf("%s 剩 %.1f GB，每小時少 %.1f GB", d.Mount, d.FreeGB, rate)
	}
	return v, nil
}

// fallRate is the GB per hour the volume at mount lost over the last hour of minute records, from the oldest to the newest of them; ok is
// false when there is no history or it covers less than slopeMinSpan.
func fallRate(in Input, mount string) (float64, bool) {
	if in.Minutes == nil {
		return 0, false
	}
	var first, last wire.Facts
	var have bool
	var firstFree, lastFree float64
	n := 0
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
			first, firstFree, have = f, free, true
		}
		last, lastFree = f, free
		n++
	}
	span := last.At.Sub(first.At)
	if !have || span < slopeMinSpan || n < slopeMinSamples {
		return 0, false
	}
	return (firstFree - lastFree) / span.Hours(), true
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
