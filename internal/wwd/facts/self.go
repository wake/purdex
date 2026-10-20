package facts

import (
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// RawSelf is one reading of wwd's own use: the CPU time it has spent since it started (user and system) and its memory.
type RawSelf struct {
	CPUTime  time.Duration
	RSSBytes uint64
}

// SelfBuilder turns readings of wwd's own use into the wire record; one value belongs to one goroutine.
type SelfBuilder struct {
	prev    RawSelf
	prevAt  time.Time
	hasPrev bool
}

func NewSelfBuilder() *SelfBuilder { return &SelfBuilder{} }

// Build turns the reading taken at now into the record. cpu_pct is the CPU time spent since the previous reading over the wall time
// between the two (one core = 100), by the real interval, not the nominal 5 s. It is null for the first reading, and when the clock did
// not move or the counter went back; the baseline is the latest reading either way, so one bad pair costs one figure.
func (b *SelfBuilder) Build(now time.Time, raw RawSelf) wire.Self {
	rss := int(raw.RSSBytes / mib)
	out := wire.Self{RSSMB: &rss}
	if dt := now.Sub(b.prevAt); b.hasPrev && dt > 0 && raw.CPUTime >= b.prev.CPUTime {
		v := 100 * float64(raw.CPUTime-b.prev.CPUTime) / float64(dt)
		out.CPUPct = &v
	}
	b.prev, b.prevAt, b.hasPrev = raw, now, true
	return out
}
