package arbitrator

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// counters holds in-process metrics counters keyed by a canonical
// name+sorted-tag string. Tags take the shape "k=v"; order-invariance is
// enforced by sorting before composing the key.
//
// This is a placeholder for a real metrics subsystem (prometheus, statsd,
// etc.); it exists so the Arbitrator pipeline can emit structured counts
// today without taking a hard dependency on a full metrics stack.
var counters sync.Map // key: string → *atomic.Int64

// Inc atomically increments the counter identified by name and tags.
// Tag order is not significant; Inc("x", "a=1", "b=2") and
// Inc("x", "b=2", "a=1") address the same counter.
func Inc(name string, tags ...string) {
	key := counterKey(name, tags)
	if v, ok := counters.Load(key); ok {
		v.(*atomic.Int64).Add(1)
		return
	}
	// Slow path: first observation of this counter.
	created := new(atomic.Int64)
	actual, loaded := counters.LoadOrStore(key, created)
	actual.(*atomic.Int64).Add(1)
	_ = loaded // either we stored `created` or we got a concurrent winner; both fine.
}

// Value returns the current count for the counter identified by name and
// tags. Unknown counters return 0. Tag order is not significant.
func Value(name string, tags ...string) int64 {
	key := counterKey(name, tags)
	if v, ok := counters.Load(key); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

// ResetForTesting clears all counters. Intended for test setup only —
// never call from production code paths.
func ResetForTesting() {
	counters.Range(func(k, _ any) bool {
		counters.Delete(k)
		return true
	})
}

// counterKey composes a stable key from name and (order-invariant) tags.
func counterKey(name string, tags []string) string {
	if len(tags) == 0 {
		return name
	}
	// Copy to avoid mutating the caller's slice.
	sorted := make([]string, len(tags))
	copy(sorted, tags)
	sort.Strings(sorted)

	var b strings.Builder
	b.Grow(len(name) + 1 + totalLen(sorted) + len(sorted))
	b.WriteString(name)
	for _, t := range sorted {
		b.WriteByte('|')
		b.WriteString(t)
	}
	return b.String()
}

func totalLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}
