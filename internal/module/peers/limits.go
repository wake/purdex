// internal/module/peers/limits.go
package peers

import (
	"sync"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

// dedupSet tracks recently-seen ids (e.g. a delivery's msg_id) within a
// sliding window, so a caller can detect and refuse a duplicate delivery.
type dedupSet struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
	now    func() time.Time
}

// newDedupSet returns a dedupSet whose window is the duration an id is
// considered a duplicate after being first Seen, using now for the current
// time (a fake clock in tests).
func newDedupSet(window time.Duration, now func() time.Time) *dedupSet {
	return &dedupSet{
		seen:   make(map[string]time.Time),
		window: window,
		now:    now,
	}
}

// Seen records id as seen at the current time and reports whether it was
// already present within window before this call. Every expired entry
// (recorded window or longer ago) is pruned first, so the map never grows
// past the number of distinct ids seen within the last window.
func (d *dedupSet) Seen(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	for k, t := range d.seen {
		if now.Sub(t) >= d.window {
			delete(d.seen, k)
		}
	}

	_, present := d.seen[id]
	d.seen[id] = now
	return present
}

// pairKey identifies one sender/receiver process pair for rate limiting.
type pairKey struct {
	From, To ipeers.OriginKey
}

// windowLimiter enforces a sliding-window rate limit per key: at most
// limit calls to Allow may return true for one key within any
// window-length span of time. Two instantiations serve /deliver: the
// pairLimiter (per sender/receiver process pair, ipeers.PairRateLimit,
// audited) and the hostLimiter (per authenticated host, ipeers.HostRateLimit,
// checked before decode/dedup/audit/inventory so a paired host cannot
// drive those at HTTP rate with fresh ids and rotating tuples).
type windowLimiter[K comparable] struct {
	mu     sync.Mutex
	counts map[K][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

type (
	pairLimiter = windowLimiter[pairKey]
	hostLimiter = windowLimiter[string] // keyed by the principal's host id
)

func newWindowLimiter[K comparable](limit int, window time.Duration, now func() time.Time) *windowLimiter[K] {
	return &windowLimiter[K]{
		counts: make(map[K][]time.Time),
		limit:  limit,
		window: window,
		now:    now,
	}
}

// newPairLimiter returns a pairLimiter allowing at most limit requests per
// key within window, using now for the current time (a fake clock in
// tests).
func newPairLimiter(limit int, window time.Duration, now func() time.Time) *pairLimiter {
	return newWindowLimiter[pairKey](limit, window, now)
}

// newHostLimiter returns a hostLimiter allowing at most limit requests per
// host id within window, on the same clock seam.
func newHostLimiter(limit int, window time.Duration, now func() time.Time) *hostLimiter {
	return newWindowLimiter[string](limit, window, now)
}

// Allow reports whether one more request for k is allowed under the sliding
// window rate limit, recording it if so. Every key's timestamps older than
// window are pruned first (not just k's), and a key left with none is
// removed entirely rather than lingering as an empty slice — so a
// limiter serving many distinct, mostly-idle keys stays bounded by the
// number of keys actually active within the last window, not by every
// key ever seen.
func (l *windowLimiter[K]) Allow(k K) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	for key, times := range l.counts {
		kept := pruneTimesBefore(times, cutoff)
		if len(kept) == 0 {
			delete(l.counts, key)
		} else {
			l.counts[key] = kept
		}
	}

	kept := l.counts[k]
	if len(kept) >= l.limit {
		return false
	}

	l.counts[k] = append(kept, now)
	return true
}

// pruneTimesBefore returns the subset of times strictly after cutoff.
func pruneTimesBefore(times []time.Time, cutoff time.Time) []time.Time {
	var kept []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}
