package arbitrator

import (
	"sync"
	"testing"
)

// TestMetrics_Inc_NoTags verifies a counter without tags increments.
func TestMetrics_Inc_NoTags(t *testing.T) {
	ResetForTesting()
	Inc("solo_counter")
	Inc("solo_counter")
	Inc("solo_counter")
	if got := Value("solo_counter"); got != 3 {
		t.Fatalf("Value(solo_counter) = %d, want 3", got)
	}
}

// TestMetrics_Inc_WithTag verifies a single-tag counter increments under its
// tagged identity and is isolated from the bare counter of the same name.
func TestMetrics_Inc_WithTag(t *testing.T) {
	ResetForTesting()
	Inc("dropped", "priority=committed")
	Inc("dropped", "priority=committed")
	Inc("dropped", "priority=proposed")

	if got := Value("dropped", "priority=committed"); got != 2 {
		t.Fatalf("Value(dropped, priority=committed) = %d, want 2", got)
	}
	if got := Value("dropped", "priority=proposed"); got != 1 {
		t.Fatalf("Value(dropped, priority=proposed) = %d, want 1", got)
	}
	// Bare counter with the same name should remain zero.
	if got := Value("dropped"); got != 0 {
		t.Fatalf("Value(dropped) = %d, want 0 (tagged increments should not leak)", got)
	}
}

// TestMetrics_Inc_MultiTags_OrderInvariant verifies tag order does not
// affect which counter is addressed.
func TestMetrics_Inc_MultiTags_OrderInvariant(t *testing.T) {
	ResetForTesting()
	Inc("multi", "a=1", "b=2")
	Inc("multi", "b=2", "a=1")
	Inc("multi", "a=1", "b=2")

	if got := Value("multi", "a=1", "b=2"); got != 3 {
		t.Fatalf("Value(multi, a=1, b=2) = %d, want 3", got)
	}
	if got := Value("multi", "b=2", "a=1"); got != 3 {
		t.Fatalf("Value(multi, b=2, a=1) = %d, want 3 (order invariant)", got)
	}
}

// TestMetrics_Value_Miss_ReturnsZero verifies reading an unknown counter
// returns zero (rather than panic or stale).
func TestMetrics_Value_Miss_ReturnsZero(t *testing.T) {
	ResetForTesting()
	if got := Value("never_incremented"); got != 0 {
		t.Fatalf("Value(never_incremented) = %d, want 0", got)
	}
	if got := Value("never_incremented", "tag=v"); got != 0 {
		t.Fatalf("Value(never_incremented, tag=v) = %d, want 0", got)
	}
}

// TestMetrics_ResetForTesting verifies Reset clears all accumulated counters.
func TestMetrics_ResetForTesting(t *testing.T) {
	Inc("reset_me")
	Inc("reset_me", "tag=a")
	if got := Value("reset_me"); got == 0 {
		t.Fatalf("Value(reset_me) should be >0 before reset")
	}

	ResetForTesting()

	if got := Value("reset_me"); got != 0 {
		t.Fatalf("Value(reset_me) after reset = %d, want 0", got)
	}
	if got := Value("reset_me", "tag=a"); got != 0 {
		t.Fatalf("Value(reset_me, tag=a) after reset = %d, want 0", got)
	}
}

// TestMetrics_Concurrent_Inc verifies concurrent Inc calls are race-free
// and account for every increment.
func TestMetrics_Concurrent_Inc(t *testing.T) {
	ResetForTesting()
	const workers = 16
	const per = 1000

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				Inc("concurrent", "w=shared")
			}
		}()
	}
	wg.Wait()

	want := int64(workers * per)
	if got := Value("concurrent", "w=shared"); got != want {
		t.Fatalf("Value(concurrent, w=shared) = %d, want %d", got, want)
	}
}
