package fleet

import (
	"time"

	"github.com/wake/purdex/internal/hostmem"
)

// PageWindow turns a host's paging counter (pageouts + swapouts on Darwin, pswpin + pswpout on Linux) into pages per second
// over hostmem.RateWindow, the one window every host is judged on, so the 50 pages/s line means the same on all four (spec
// §5). It is never a one-frame delta. Not safe for concurrent use: one per host session.
type PageWindow struct {
	samples []hostmem.PageSample
}

// Observe records the counter read at at and returns the rate against the oldest reading still inside the window. With
// fewer than two readings a second apart (the first frame, or a window reset) the rate is unknown: (0, false). A counter
// that went back (a reboot) clears the window and starts again.
func (w *PageWindow) Observe(at time.Time, total uint64) (rate float64, known bool) {
	if n := len(w.samples); n > 0 && (total < w.samples[n-1].Total || !at.After(w.samples[n-1].At)) {
		w.samples = w.samples[:0]
	}
	cur := hostmem.PageSample{At: at, Total: total}
	kept := w.samples[:0]
	var haveBase bool
	for _, s := range w.samples {
		age := at.Sub(s.At)
		if age > hostmem.RateWindow {
			continue
		}
		if age >= time.Second {
			haveBase = true
		}
		kept = append(kept, s)
	}
	w.samples = append(kept, cur)
	if !haveBase {
		return 0, false
	}
	return hostmem.RateOver(w.samples[:len(w.samples)-1], cur), true
}

// Reset forgets the readings: for a session that restarted, whose counters are not comparable with the old ones.
func (w *PageWindow) Reset() { w.samples = w.samples[:0] }
