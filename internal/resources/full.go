package resources

// ExitMargin is how far below the entry line a reading has to fall before
// the host stops counting as full: load1 below 0.9 x ncpu, memory in use
// below 0.9 x MemFullPercent. Entering follows R5 exactly; the margin only
// keeps the flag from flapping while load1 wobbles around the core count (the
// P0 acceptance saw it between 9.5 and 10.5 for ten minutes on 10 cores).
const ExitMargin = 0.9

// FullLatch turns the stateless readings of HostUse into the "host is full"
// flag the sampler publishes. The zero value is clear. One latch belongs to
// one sampler goroutine; it is not safe for concurrent use.
type FullLatch struct{ on bool }

// Update takes the newest reading and returns whether the host is full.
//
// Entering (R5): load1 >= ncpu, or memory in use >= MemFullPercent, or memory
// pressure warn or worse. Leaving needs all three to be clear with the margin:
// load1 < ExitMargin x ncpu, memory < ExitMargin x MemFullPercent, pressure
// normal. A reading without a cpu count cannot be judged and counts as clear.
func (l *FullLatch) Update(h HostUse) bool {
	if h.NCPU <= 0 {
		l.on = false
		return false
	}
	ncpu := float64(h.NCPU)
	if h.Load1 >= ncpu || h.Mem >= MemFullPercent || h.Pressure >= 2 {
		l.on = true
		return true
	}
	if l.on && h.Load1 < ExitMargin*ncpu && h.Mem < ExitMargin*MemFullPercent && h.Pressure < 2 {
		l.on = false
	}
	return l.on
}
