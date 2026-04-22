package observation_test

import (
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// captureTraceIDLog redirects the default logger to a buffer for the duration
// of t. It is a per-test-file capture helper distinct from captureLog in
// builder_test.go to avoid a compile-time duplicate declaration in the same
// test package.
func captureTraceIDLog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// TestTraceIDRegistry_FreshInstance_EmptyState verifies that a newly created
// registry has no entries (simulates daemon restart).
func TestTraceIDRegistry_FreshInstance_EmptyState(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	id, ok := r.Get("s1", 1)
	if ok {
		t.Fatalf("expected miss on fresh registry, got hit with id=%q", id)
	}
	if id != "" {
		t.Fatalf("expected empty string on miss, got %q", id)
	}
}

// TestTraceIDRegistry_Mint_NewKey_UUIDv4 verifies that Mint returns a valid
// uuidv4 and that Get returns the same value.
func TestTraceIDRegistry_Mint_NewKey_UUIDv4(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	got := r.Mint("s1", 1)

	parsed, err := uuid.Parse(got)
	if err != nil {
		t.Fatalf("Mint returned non-UUID value %q: %v", got, err)
	}
	// version 4
	if parsed.Version() != 4 {
		t.Fatalf("expected UUID version 4, got version %d", parsed.Version())
	}

	got2, ok := r.Get("s1", 1)
	if !ok {
		t.Fatal("Get after Mint returned miss")
	}
	if got2 != got {
		t.Fatalf("Get returned different id: Mint=%q Get=%q", got, got2)
	}
}

// TestTraceIDRegistry_Mint_SameKeyTwice_ReturnsExisting verifies that a
// repeat Mint returns the existing value and logs a warning.
func TestTraceIDRegistry_Mint_SameKeyTwice_ReturnsExisting(t *testing.T) {
	buf := captureTraceIDLog(t)
	r := observation.NewTraceIDRegistry()

	first := r.Mint("s1", 1)
	second := r.Mint("s1", 1)

	if first != second {
		t.Fatalf("repeat Mint must return existing id: first=%q second=%q", first, second)
	}
	if !strings.Contains(buf.String(), "mint called twice") {
		t.Fatalf("expected 'mint called twice' warning in log, got: %q", buf.String())
	}
}

// TestTraceIDRegistry_Get_Hit verifies that Get returns (id, true) for a
// previously minted key.
func TestTraceIDRegistry_Get_Hit(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	minted := r.Mint("s1", 1)

	got, ok := r.Get("s1", 1)
	if !ok {
		t.Fatal("Get returned miss for minted key")
	}
	if got != minted {
		t.Fatalf("Get returned %q, want %q", got, minted)
	}
}

// TestTraceIDRegistry_Get_Miss verifies that Get returns ("", false) when no
// Mint has been called. The registry must produce no log output for a clean
// read-path miss.
func TestTraceIDRegistry_Get_Miss(t *testing.T) {
	buf := captureTraceIDLog(t)
	r := observation.NewTraceIDRegistry()

	got, ok := r.Get("s1", 1)
	if ok {
		t.Fatalf("expected miss, got hit with id=%q", got)
	}
	if got != "" {
		t.Fatalf("expected empty string on miss, got %q", got)
	}
	if buf.String() != "" {
		t.Fatalf("Get miss should produce no log output, got: %q", buf.String())
	}
}

// TestTraceIDRegistry_DifferentGeneration_DifferentID verifies that different
// generations for the same session receive different trace IDs.
func TestTraceIDRegistry_DifferentGeneration_DifferentID(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	id1 := r.Mint("s1", 1)
	id2 := r.Mint("s1", 2)

	if id1 == id2 {
		t.Fatalf("different generations must have different trace IDs, both got %q", id1)
	}

	got1, ok1 := r.Get("s1", 1)
	got2, ok2 := r.Get("s1", 2)
	if !ok1 || !ok2 {
		t.Fatalf("Get miss after Mint: ok1=%v ok2=%v", ok1, ok2)
	}
	if got1 != id1 {
		t.Fatalf("gen 1: want %q got %q", id1, got1)
	}
	if got2 != id2 {
		t.Fatalf("gen 2: want %q got %q", id2, got2)
	}

	// Both must be valid uuidv4.
	for _, id := range []string{id1, id2} {
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("Mint returned non-UUID %q: %v", id, err)
		}
	}
}

// TestTraceIDRegistry_DifferentSession_DifferentID verifies that the same
// generation for different sessions receives different trace IDs.
func TestTraceIDRegistry_DifferentSession_DifferentID(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	id1 := r.Mint("s1", 1)
	id2 := r.Mint("s2", 1)

	if id1 == id2 {
		t.Fatalf("different sessions must have different trace IDs, both got %q", id1)
	}
}

// TestTraceIDRegistry_PruneSessionBefore verifies that PruneSessionBefore
// removes entries with generation < threshold and leaves ≥ threshold intact.
func TestTraceIDRegistry_PruneSessionBefore(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	r.Mint("s1", 1)
	r.Mint("s1", 2)
	id3 := r.Mint("s1", 3)

	removed := r.PruneSessionBefore("s1", 3)
	if removed != 2 {
		t.Fatalf("PruneSessionBefore returned %d, want 2", removed)
	}

	if _, ok := r.Get("s1", 1); ok {
		t.Error("gen 1 should have been pruned")
	}
	if _, ok := r.Get("s1", 2); ok {
		t.Error("gen 2 should have been pruned")
	}

	got3, ok3 := r.Get("s1", 3)
	if !ok3 {
		t.Error("gen 3 should survive PruneSessionBefore")
	}
	if got3 != id3 {
		t.Fatalf("gen 3 id changed after prune: want %q got %q", id3, got3)
	}
}

// TestTraceIDRegistry_PruneSession verifies that PruneSession removes all
// entries for a session and does not affect other sessions.
func TestTraceIDRegistry_PruneSession(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	r.Mint("s1", 1)
	r.Mint("s1", 2)
	id2 := r.Mint("s2", 1)

	removed := r.PruneSession("s1")
	if removed != 2 {
		t.Fatalf("PruneSession returned %d, want 2", removed)
	}

	if _, ok := r.Get("s1", 1); ok {
		t.Error("s1 gen 1 should have been pruned")
	}
	if _, ok := r.Get("s1", 2); ok {
		t.Error("s1 gen 2 should have been pruned")
	}

	got, ok := r.Get("s2", 1)
	if !ok {
		t.Error("s2 gen 1 should survive PruneSession(s1)")
	}
	if got != id2 {
		t.Fatalf("s2 gen 1 id changed: want %q got %q", id2, got)
	}
}

// TestTraceIDRegistry_PruneSessionBefore_NonExistent verifies that pruning an
// unknown session returns 0 and does not panic.
func TestTraceIDRegistry_PruneSessionBefore_NonExistent(t *testing.T) {
	r := observation.NewTraceIDRegistry()
	removed := r.PruneSessionBefore("unknown", 99)
	if removed != 0 {
		t.Fatalf("expected 0 removed for unknown session, got %d", removed)
	}
}

// TestTraceIDRegistry_Concurrent_Race exercises Mint and PruneSessionBefore
// concurrently to surface data races under -race. A panic or race report is
// the failure signal.
func TestTraceIDRegistry_Concurrent_Race(t *testing.T) {
	r := observation.NewTraceIDRegistry()

	const sessions = 5
	const pairs = 100 // generations per session = 500 total Mint calls

	var wg sync.WaitGroup

	// Goroutine A: Mint 500 different (session, generation) pairs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := 0; s < sessions; s++ {
			sessionID := "race-session-" + string(rune('A'+s))
			for g := int64(0); g < pairs; g++ {
				r.Mint(sessionID, g)
			}
		}
	}()

	// Goroutine B: Concurrently prune sessions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := 0; s < sessions; s++ {
			sessionID := "race-session-" + string(rune('A'+s))
			for g := int64(0); g < pairs; g += 10 {
				r.PruneSessionBefore(sessionID, g)
			}
		}
	}()

	wg.Wait()
}
