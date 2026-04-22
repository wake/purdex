package arbitrator

import "time"

// hookStormGuard implements the D12 hook-storm throttle: at most `cap`
// observations may pass per session within a rolling `windowSize`. The
// window is fixed (not sliding) — once windowSize has elapsed since the
// current window's start, the counter resets on the next ShouldDrop call.
//
// The guard is single-owner (consumed by the Arbitrator goroutine) and
// carries no mutex; concurrent use is a programming error.
type hookStormGuard struct {
	windowSize time.Duration
	cap        int
	counters   map[string]*hookStormCounter
}

// hookStormCounter tracks a single session's current window.
type hookStormCounter struct {
	windowStart time.Time
	count       int
}

// newHookStormGuard returns a guard with the supplied window size and cap.
// Both values are caller-controlled; this package does not hardcode defaults
// (the Arbitrator wires D12's 10ms/50 values at Task 7).
func newHookStormGuard(windowSize time.Duration, cap int) *hookStormGuard {
	return &hookStormGuard{
		windowSize: windowSize,
		cap:        cap,
		counters:   make(map[string]*hookStormCounter),
	}
}

// ShouldDrop increments the per-session counter for sessionID and reports
// whether the observation should be dropped by the throttle.
//
// Semantics:
//   - First observation for a session (or first after window expiry) resets
//     the window to start at `now` with count=1 → returns false.
//   - Within the active window: if count < cap, increment and return false;
//     else leave the counter untouched and return true.
func (g *hookStormGuard) ShouldDrop(sessionID string, now time.Time) bool {
	c, ok := g.counters[sessionID]
	if !ok {
		g.counters[sessionID] = &hookStormCounter{windowStart: now, count: 1}
		return false
	}
	// Reset on window expiry.
	if !now.Before(c.windowStart.Add(g.windowSize)) {
		c.windowStart = now
		c.count = 1
		return false
	}
	if c.count < g.cap {
		c.count++
		return false
	}
	return true
}
