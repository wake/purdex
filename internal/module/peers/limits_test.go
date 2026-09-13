// internal/module/peers/limits_test.go
package peers

import (
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

// manualClock is a settable clock for limiter/dedup tests: Now() returns
// the last value set by Advance/Set, letting a test move time forward by
// an exact duration rather than pre-scripting a fixed sequence of calls.
type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time          { return c.t }
func (c *manualClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// --- dedupSet ------------------------------------------------------------

func TestDedupSet_FirstSeenFalseThenTrue(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	d := newDedupSet(60*time.Second, clock.Now)

	if d.Seen("id1") {
		t.Errorf("Seen(id1) first call = true, want false")
	}
	if !d.Seen("id1") {
		t.Errorf("Seen(id1) second call = false, want true (still within window)")
	}
}

func TestDedupSet_ExpiresAfterWindow(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	d := newDedupSet(60*time.Second, clock.Now)

	if d.Seen("id1") {
		t.Fatalf("Seen(id1) first call = true, want false")
	}

	clock.Advance(60 * time.Second)
	if d.Seen("id1") {
		t.Errorf("Seen(id1) after window = true, want false (expired)")
	}
}

func TestDedupSet_IndependentIDs(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	d := newDedupSet(60*time.Second, clock.Now)

	if d.Seen("a") {
		t.Errorf("Seen(a) = true, want false")
	}
	if d.Seen("b") {
		t.Errorf("Seen(b) = true, want false (independent of a)")
	}
	if !d.Seen("a") {
		t.Errorf("Seen(a) second call = false, want true")
	}
}

// TestDedupSet_MapBoundedAfterPruning pins that Seen prunes every expired
// entry on each call, so the backing map does not grow without bound across
// many distinct short-lived ids.
func TestDedupSet_MapBoundedAfterPruning(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	d := newDedupSet(10*time.Second, clock.Now)

	for i := 0; i < 100; i++ {
		d.Seen(string(rune('a' + i%26)))
		clock.Advance(time.Second)
	}
	// 10s window: at most ~10 entries should remain live at any instant.
	if len(d.seen) > 15 {
		t.Errorf("len(d.seen) = %d, want a small bounded number (pruning not happening)", len(d.seen))
	}
}

// --- pairLimiter -----------------------------------------------------------

func testPairKey(from, to string) pairKey {
	return pairKey{
		From: ipeers.OriginKey{HostID: from},
		To:   ipeers.OriginKey{HostID: to},
	}
}

func TestPairLimiter_AllowsUpToLimitThenRefuses(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(30, 60*time.Second, clock.Now)
	k := testPairKey("a", "b")

	for i := 0; i < 30; i++ {
		if !l.Allow(k) {
			t.Fatalf("Allow #%d = false, want true (under limit)", i+1)
		}
	}
	if l.Allow(k) {
		t.Errorf("Allow #31 = true, want false (limit is 30)")
	}
}

func TestPairLimiter_AllowedAgainAfterWindow(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(30, 60*time.Second, clock.Now)
	k := testPairKey("a", "b")

	for i := 0; i < 30; i++ {
		l.Allow(k)
	}
	if l.Allow(k) {
		t.Fatalf("Allow #31 = true, want false")
	}

	clock.Advance(60 * time.Second)
	if !l.Allow(k) {
		t.Errorf("Allow after 60s = false, want true (old timestamps expired)")
	}
}

func TestPairLimiter_JustUnderWindow_StillRefused(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(30, 60*time.Second, clock.Now)
	k := testPairKey("a", "b")

	for i := 0; i < 30; i++ {
		l.Allow(k)
	}
	clock.Advance(59 * time.Second)
	if l.Allow(k) {
		t.Errorf("Allow at 59s = true, want false (window not yet elapsed)")
	}
}

func TestPairLimiter_IndependentPairsIndependent(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(2, 60*time.Second, clock.Now)
	k1 := testPairKey("a", "b")
	k2 := testPairKey("a", "c")

	if !l.Allow(k1) || !l.Allow(k1) {
		t.Fatalf("k1: first two Allow calls should succeed")
	}
	if l.Allow(k1) {
		t.Errorf("k1: third Allow call = true, want false (limit 2)")
	}
	// k2 is a distinct pair and must not be affected by k1's usage.
	if !l.Allow(k2) || !l.Allow(k2) {
		t.Errorf("k2: independent pair should still be allowed up to its own limit")
	}
}

// TestPairLimiter_MapBoundedAfterPruning pins that Allow prunes every key's
// expired timestamps on each call (not just the touched key's), so a
// long-lived key's slice does not grow past the limit across many windows.
func TestPairLimiter_MapBoundedAfterPruning(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(30, 60*time.Second, clock.Now)
	k := testPairKey("a", "b")

	for round := 0; round < 5; round++ {
		for i := 0; i < 30; i++ {
			l.Allow(k)
		}
		clock.Advance(60 * time.Second)
	}
	l.Allow(k)

	if got := len(l.counts[k]); got > 30 {
		t.Errorf("len(l.counts[key]) = %d, want <= limit (30) — old timestamps not pruned", got)
	}
}

// TestPairLimiter_IdlePairRemovedFromMap pins that a pair which goes
// entirely idle for a full window is dropped from the map on the next
// Allow call for ANY key — not just trimmed to an empty slice — so a
// limiter serving many short-lived pairs does not grow forever.
func TestPairLimiter_IdlePairRemovedFromMap(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newPairLimiter(30, 60*time.Second, clock.Now)
	idle := testPairKey("a", "b")
	other := testPairKey("c", "d")

	l.Allow(idle)
	if _, ok := l.counts[idle]; !ok {
		t.Fatalf("idle key missing from map right after Allow")
	}

	clock.Advance(60 * time.Second)
	l.Allow(other) // touches a different key; must still prune "idle"

	if _, ok := l.counts[idle]; ok {
		t.Errorf("idle key still present in map after its window fully elapsed")
	}
	if len(l.counts) != 1 {
		t.Errorf("len(l.counts) = %d, want 1 (only the just-touched key)", len(l.counts))
	}
}
