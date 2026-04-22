package observation_test

import (
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// Compile-time assertions that the exported interfaces exist with the
// expected shape at the package boundary. The stronger assertion that
// *traceIDRegistry satisfies both interfaces lives in trace_id.go, where
// the unexported concrete type is nameable.
var (
	_ observation.TraceIDMinter = (observation.TraceIDMinter)(nil)
	_ observation.TraceIDLookup = (observation.TraceIDLookup)(nil)
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

// TestNewTraceIDRegistry_TwoViews_ShareUnderlying verifies that the two
// interface views returned by NewTraceIDRegistry share the same underlying
// state: minting via the Minter is immediately observable via the Lookup.
func TestNewTraceIDRegistry_TwoViews_ShareUnderlying(t *testing.T) {
	minter, lookup := observation.NewTraceIDRegistry()

	minted := minter.Mint("s1", 1)
	if minted == "" {
		t.Fatalf("Mint returned empty string on fresh registry")
	}

	got, ok := lookup.Get("s1", 1)
	if !ok {
		t.Fatal("Lookup view missed the id minted via the Minter view; views do not share state")
	}
	if got != minted {
		t.Fatalf("Lookup returned different id than Mint: minted=%q got=%q", minted, got)
	}
}

// TestNewTraceIDRegistry_MinterPruneReflectsInLookup verifies that pruning via
// the Minter view invalidates reads from the Lookup view, confirming shared
// state across both interfaces.
func TestNewTraceIDRegistry_MinterPruneReflectsInLookup(t *testing.T) {
	minter, lookup := observation.NewTraceIDRegistry()

	minter.Mint("s1", 1)
	minter.Mint("s1", 2)
	id3 := minter.Mint("s1", 3)

	removed := minter.PruneSessionBefore("s1", 3)
	if removed != 2 {
		t.Fatalf("PruneSessionBefore returned %d, want 2", removed)
	}

	if _, ok := lookup.Get("s1", 1); ok {
		t.Error("gen 1 should be pruned from Lookup view")
	}
	if _, ok := lookup.Get("s1", 2); ok {
		t.Error("gen 2 should be pruned from Lookup view")
	}

	got3, ok3 := lookup.Get("s1", 3)
	if !ok3 {
		t.Error("gen 3 should survive PruneSessionBefore and be visible via Lookup")
	}
	if got3 != id3 {
		t.Fatalf("gen 3 id changed after prune: want %q got %q", id3, got3)
	}

	// PruneSession via Minter view also reflects in Lookup.
	minter.PruneSession("s1")
	if _, ok := lookup.Get("s1", 3); ok {
		t.Error("gen 3 should be pruned after PruneSession via Minter view")
	}
}

// TestTraceIDRegistry_FreshInstance_EmptyState verifies that a newly created
// registry has no entries (simulates daemon restart).
func TestTraceIDRegistry_FreshInstance_EmptyState(t *testing.T) {
	_, lookup := observation.NewTraceIDRegistry()
	id, ok := lookup.Get("s1", 1)
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
	minter, lookup := observation.NewTraceIDRegistry()
	got := minter.Mint("s1", 1)

	parsed, err := uuid.Parse(got)
	if err != nil {
		t.Fatalf("Mint returned non-UUID value %q: %v", got, err)
	}
	if parsed.Version() != 4 {
		t.Fatalf("expected UUID version 4, got version %d", parsed.Version())
	}
	if parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("expected UUID variant RFC4122, got %v", parsed.Variant())
	}

	got2, ok := lookup.Get("s1", 1)
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
	minter, _ := observation.NewTraceIDRegistry()

	first := minter.Mint("s1", 1)
	second := minter.Mint("s1", 1)

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
	minter, lookup := observation.NewTraceIDRegistry()
	minted := minter.Mint("s1", 1)

	got, ok := lookup.Get("s1", 1)
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
	_, lookup := observation.NewTraceIDRegistry()

	got, ok := lookup.Get("s1", 1)
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
	minter, lookup := observation.NewTraceIDRegistry()
	id1 := minter.Mint("s1", 1)
	id2 := minter.Mint("s1", 2)

	if id1 == id2 {
		t.Fatalf("different generations must have different trace IDs, both got %q", id1)
	}

	got1, ok1 := lookup.Get("s1", 1)
	got2, ok2 := lookup.Get("s1", 2)
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
// generation for different sessions receives different trace IDs, and that
// Get returns each session's id independently.
func TestTraceIDRegistry_DifferentSession_DifferentID(t *testing.T) {
	minter, lookup := observation.NewTraceIDRegistry()
	id1 := minter.Mint("s1", 1)
	id2 := minter.Mint("s2", 1)

	if id1 == id2 {
		t.Fatalf("different sessions must have different trace IDs, both got %q", id1)
	}

	got1, ok1 := lookup.Get("s1", 1)
	if !ok1 || got1 != id1 {
		t.Fatalf("Get(s1,1) = (%q,%v); want (%q,true)", got1, ok1, id1)
	}
	got2, ok2 := lookup.Get("s2", 1)
	if !ok2 || got2 != id2 {
		t.Fatalf("Get(s2,1) = (%q,%v); want (%q,true)", got2, ok2, id2)
	}
}

// TestTraceIDRegistry_PruneSessionBefore verifies that PruneSessionBefore
// removes entries with generation < threshold and leaves ≥ threshold intact.
func TestTraceIDRegistry_PruneSessionBefore(t *testing.T) {
	minter, lookup := observation.NewTraceIDRegistry()
	minter.Mint("s1", 1)
	minter.Mint("s1", 2)
	id3 := minter.Mint("s1", 3)

	removed := minter.PruneSessionBefore("s1", 3)
	if removed != 2 {
		t.Fatalf("PruneSessionBefore returned %d, want 2", removed)
	}

	if _, ok := lookup.Get("s1", 1); ok {
		t.Error("gen 1 should have been pruned")
	}
	if _, ok := lookup.Get("s1", 2); ok {
		t.Error("gen 2 should have been pruned")
	}

	got3, ok3 := lookup.Get("s1", 3)
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
	minter, lookup := observation.NewTraceIDRegistry()
	minter.Mint("s1", 1)
	minter.Mint("s1", 2)
	id2 := minter.Mint("s2", 1)

	removed := minter.PruneSession("s1")
	if removed != 2 {
		t.Fatalf("PruneSession returned %d, want 2", removed)
	}

	if _, ok := lookup.Get("s1", 1); ok {
		t.Error("s1 gen 1 should have been pruned")
	}
	if _, ok := lookup.Get("s1", 2); ok {
		t.Error("s1 gen 2 should have been pruned")
	}

	got, ok := lookup.Get("s2", 1)
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
	minter, _ := observation.NewTraceIDRegistry()
	removed := minter.PruneSessionBefore("unknown", 99)
	if removed != 0 {
		t.Fatalf("expected 0 removed for unknown session, got %d", removed)
	}
}

// TestTraceIDRegistry_PruneSession_NonExistent verifies that PruneSession on an
// unknown session returns 0 and does not panic (symmetric with PruneSessionBefore).
func TestTraceIDRegistry_PruneSession_NonExistent(t *testing.T) {
	minter, _ := observation.NewTraceIDRegistry()
	removed := minter.PruneSession("unknown")
	if removed != 0 {
		t.Fatalf("expected 0 removed for unknown session, got %d", removed)
	}
}

// TestTraceIDRegistry_Mint_StaleAfterPrune_ReturnsEmpty verifies that a Mint
// for a generation below the watermark (set by PruneSessionBefore) returns ""
// and logs a rejection message, while Mint above the watermark still works.
func TestTraceIDRegistry_Mint_StaleAfterPrune_ReturnsEmpty(t *testing.T) {
	buf := captureTraceIDLog(t)
	minter, _ := observation.NewTraceIDRegistry()

	minter.Mint("s1", 1)
	minter.Mint("s1", 2)
	minter.Mint("s1", 3)

	minter.PruneSessionBefore("s1", 3) // watermark = 3; gens 1,2 pruned; gen 3 survives

	// Stale mint for pruned generation must return empty string.
	got := minter.Mint("s1", 2)
	if got != "" {
		t.Fatalf("Mint for pruned generation should return empty, got %q", got)
	}
	if !strings.Contains(buf.String(), "stale mint rejected") {
		t.Errorf("expected 'stale mint rejected' in log, got: %q", buf.String())
	}

	// Mint at the watermark boundary (generation == floor) is allowed.
	id4 := minter.Mint("s1", 4) // above watermark — fresh generation
	if id4 == "" {
		t.Error("Mint above watermark should succeed, got empty string")
	}
}

// TestTraceIDRegistry_Mint_AtWatermark_Rejected verifies watermark boundary
// semantics: PruneSessionBefore(s1, 5) deletes gens < 5, watermark = 5.
// generation 4 → rejected (< 5); generation 5 → allowed (>= 5).
func TestTraceIDRegistry_Mint_AtWatermark_Rejected(t *testing.T) {
	minter, _ := observation.NewTraceIDRegistry()

	minter.PruneSessionBefore("s1", 5) // sets watermark = 5; nothing to delete

	// Generation strictly below watermark → rejected.
	got4 := minter.Mint("s1", 4)
	if got4 != "" {
		t.Fatalf("Mint(s1, 4) below watermark 5 should return empty, got %q", got4)
	}

	// Generation equal to watermark → allowed (exclusive-floor semantics).
	got5 := minter.Mint("s1", 5)
	if got5 == "" {
		t.Error("Mint(s1, 5) at watermark floor should succeed, got empty string")
	}

	// Generation above watermark → allowed.
	got6 := minter.Mint("s1", 6)
	if got6 == "" {
		t.Error("Mint(s1, 6) above watermark should succeed, got empty string")
	}
}

// TestTraceIDRegistry_PruneSession_ResetsWatermark verifies that PruneSession
// clears the per-session watermark, allowing subsequent Mints for any
// generation (fresh-session semantics after full session cleanup).
func TestTraceIDRegistry_PruneSession_ResetsWatermark(t *testing.T) {
	minter, _ := observation.NewTraceIDRegistry()

	minter.Mint("s1", 1)
	minter.PruneSessionBefore("s1", 2) // watermark = 2; gen 1 pruned

	// Confirm stale mint is blocked before PruneSession.
	staleBefore := minter.Mint("s1", 0)
	if staleBefore != "" {
		t.Fatalf("expected stale mint to be blocked, got %q", staleBefore)
	}

	// Full session cleanup resets watermark.
	minter.PruneSession("s1")

	// After PruneSession, even generation 0 is allowed (watermark cleared).
	gotAfter := minter.Mint("s1", 0)
	if gotAfter == "" {
		t.Error("Mint after PruneSession should succeed (watermark cleared), got empty string")
	}
}

// TestTraceIDRegistry_Concurrent_Race exercises Mint and PruneSessionBefore
// concurrently to surface data races under -race. A panic or race report is
// the failure signal.
func TestTraceIDRegistry_Concurrent_Race(t *testing.T) {
	minter, lookup := observation.NewTraceIDRegistry()

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
				minter.Mint(sessionID, g)
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
				minter.PruneSessionBefore(sessionID, g)
			}
		}
	}()

	// Goroutine C: Concurrently Get (exercises RLock vs Prune's Lock).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := 0; s < sessions; s++ {
			sessionID := "race-session-" + string(rune('A'+s))
			for g := int64(0); g < pairs; g++ {
				_, _ = lookup.Get(sessionID, g)
			}
		}
	}()

	wg.Wait()
}
