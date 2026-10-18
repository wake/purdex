package derived

// MemFullPercent is the memory in use at and above which a host counts as full (internal/resources.MemFullPercent).
const MemFullPercent = 90

// FullLatch is the "host is full" flag, internal/resources.FullLatch copied because wwd must not import that package
// (it pulls in internal/agent); a test keeps the two equal until WWD-9 removes the Purdex one. Entering: load1 >= ncpu,
// or memory in use >= MemFullPercent, or pressure warn or worse. Leaving needs all three clear with the ExitMargin:
// load1 < 0.9 x ncpu, memory < 0.9 x MemFullPercent, pressure normal. A reading without a cpu count cannot be judged and
// counts as clear. The zero value is clear; not safe for concurrent use.
type FullLatch struct{ on bool }

// Update takes the newest reading and returns whether the host is full. memPct is the memory in use as a percentage,
// pressure the level (1 normal, 2 warning, 4 critical, 0 unknown).
func (l *FullLatch) Update(load1 float64, ncpu int, memPct float64, pressure int) bool {
	if ncpu <= 0 {
		l.on = false
		return false
	}
	n := float64(ncpu)
	if load1 >= n || memPct >= MemFullPercent || pressure >= 2 {
		l.on = true
		return true
	}
	if l.on && load1 < ExitMargin*n && memPct < ExitMargin*MemFullPercent && pressure < 2 {
		l.on = false
	}
	return l.on
}
