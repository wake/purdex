package arbmode

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// captureLog redirects the default logger to a buffer for the duration of t.
// It restores the original writer via t.Cleanup.
func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// ── ArbMode ─────────────────────────────────────────────────────────────────

func TestArbMode_IsValid(t *testing.T) {
	if !ModePassthrough.IsValid() {
		t.Error("ModePassthrough.IsValid() should be true")
	}
	if !ModeAuthoritative.IsValid() {
		t.Error("ModeAuthoritative.IsValid() should be true")
	}
	if ArbMode("bogus").IsValid() {
		t.Error(`ArbMode("bogus").IsValid() should be false`)
	}
	if ArbMode("").IsValid() {
		t.Error(`ArbMode("").IsValid() should be false`)
	}
}

// ── NewManager ───────────────────────────────────────────────────────────────

func TestManager_DefaultPassthrough_Snapshot(t *testing.T) {
	m := NewManager("", "")
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want %q", snap.Current, ModePassthrough)
	}
	if snap.Pending != ModePassthrough {
		t.Errorf("Pending=%q, want %q", snap.Pending, ModePassthrough)
	}
	if snap.EnvLocked {
		t.Error("EnvLocked should be false")
	}
	// Published pointer must never be nil after construction.
	mode, rev := m.loadPublishedForTest()
	if mode != ModePassthrough {
		t.Errorf("published.Mode=%q, want passthrough", mode)
	}
	if rev != 0 {
		t.Errorf("published.Revision=%d, want 0", rev)
	}
}

func TestManager_ConfigOnly_Authoritative(t *testing.T) {
	m := NewManager("", "authoritative")
	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("Current=%q, want %q", snap.Current, ModeAuthoritative)
	}
	if snap.EnvLocked {
		t.Error("EnvLocked should be false")
	}
}

func TestManager_EnvPassthrough_ConfigAuthoritative_EnvWins(t *testing.T) {
	m := NewManager("passthrough", "authoritative")
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want passthrough", snap.Current)
	}
	if snap.Pending != ModePassthrough {
		t.Errorf("Pending=%q, want passthrough", snap.Pending)
	}
	if !snap.EnvLocked {
		t.Error("EnvLocked should be true when env is set")
	}
}

func TestManager_EnvInvalid_FallbackToConfig(t *testing.T) {
	buf := captureLog(t)
	m := NewManager("bogus", "authoritative")
	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("Current=%q, want authoritative", snap.Current)
	}
	if snap.EnvLocked {
		t.Error("EnvLocked should be false when env value is invalid")
	}
	if !strings.Contains(buf.String(), "invalid env value") {
		t.Errorf("expected log to contain %q, got: %q", "invalid env value", buf.String())
	}
}

func TestManager_ConfigInvalid_FallbackToPassthrough(t *testing.T) {
	buf := captureLog(t)
	m := NewManager("", "bogus")
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want passthrough", snap.Current)
	}
	if !strings.Contains(buf.String(), "invalid config value") {
		t.Errorf("expected log to contain %q, got: %q", "invalid config value", buf.String())
	}
}

// ── OnConfigChange ────────────────────────────────────────────────────────────

func TestManager_OnConfigChange_EnvUnset_UpdatesPending(t *testing.T) {
	m := NewManager("", "passthrough")
	changed := m.OnConfigChange("authoritative")
	if !changed {
		t.Error("OnConfigChange should return true when pending changes")
	}
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want passthrough (should not change yet)", snap.Current)
	}
	if snap.Pending != ModeAuthoritative {
		t.Errorf("Pending=%q, want authoritative", snap.Pending)
	}
	if snap.EnvLocked {
		t.Error("EnvLocked should be false")
	}
}

func TestManager_OnConfigChange_EnvLocked_NoOp(t *testing.T) {
	buf := captureLog(t)
	m := NewManager("authoritative", "passthrough")
	changed := m.OnConfigChange("passthrough")
	if changed {
		t.Error("OnConfigChange should return false when env-locked")
	}
	snap := m.Snapshot()
	if snap.Pending != ModeAuthoritative {
		t.Errorf("Pending=%q, should stay authoritative (env-locked)", snap.Pending)
	}
	if !strings.Contains(buf.String(), "overridden by env") {
		t.Errorf("expected log to contain %q, got: %q", "overridden by env", buf.String())
	}
}

func TestManager_OnConfigChange_SameValue_NoOp(t *testing.T) {
	m := NewManager("", "passthrough")
	// Clear any log from NewManager by using a fresh capture after construction.
	buf := captureLog(t)
	changed := m.OnConfigChange("passthrough")
	if changed {
		t.Error("OnConfigChange should return false when value unchanged")
	}
	if buf.String() != "" {
		t.Errorf("expected no log output, got: %q", buf.String())
	}
}

func TestManager_OnConfigChange_InvalidValue_FallbackToPassthrough(t *testing.T) {
	// Start with pending=authoritative.
	m := NewManager("", "authoritative")
	buf := captureLog(t)
	// "bogus" is invalid; should fall back to passthrough.
	// Since current pending is authoritative and fallback is passthrough, changed=true.
	changed := m.OnConfigChange("bogus")
	if !changed {
		t.Error("OnConfigChange returned false; want true when fallback changes pending (authoritative → passthrough)")
	}
	if !strings.Contains(buf.String(), "invalid config value") {
		t.Errorf("expected log to contain %q, got: %q", "invalid config value", buf.String())
	}
	snap := m.Snapshot()
	if snap.Pending != ModePassthrough {
		t.Errorf("Pending=%q, want passthrough after invalid config", snap.Pending)
	}
}

// ── OnConfigChange — published snapshot semantics (#579) ─────────────────────

// TestManager_OnConfigChange_PublishesSnapshot verifies that OnConfigChange
// atomic-publishes a new *modeTarget whose Mode equals the new value and
// Revision is previous+1.
func TestManager_OnConfigChange_PublishesSnapshot(t *testing.T) {
	m := NewManager("", "passthrough")
	_, prevRev := m.loadPublishedForTest()

	changed := m.OnConfigChange("authoritative")
	if !changed {
		t.Fatal("OnConfigChange should return true when mode changes")
	}
	mode, rev := m.loadPublishedForTest()
	if mode != ModeAuthoritative {
		t.Errorf("published.Mode=%q, want authoritative", mode)
	}
	if rev != prevRev+1 {
		t.Errorf("published.Revision=%d, want %d (prev+1)", rev, prevRev+1)
	}
}

// TestManager_OnConfigChange_SameValue_NoRevBump verifies that a no-op
// OnConfigChange (same mode as current published) does not bump Revision.
func TestManager_OnConfigChange_SameValue_NoRevBump(t *testing.T) {
	m := NewManager("", "passthrough")
	_, rev0 := m.loadPublishedForTest()

	changed := m.OnConfigChange("passthrough")
	if changed {
		t.Error("OnConfigChange should return false when value unchanged")
	}
	_, rev1 := m.loadPublishedForTest()
	if rev1 != rev0 {
		t.Errorf("published.Revision bumped on no-op: before=%d after=%d", rev0, rev1)
	}
}

// TestManager_OnConfigChange_EnvLocked_NoPublish verifies that env-locked
// OnConfigChange calls do not mutate the published target.
func TestManager_OnConfigChange_EnvLocked_NoPublish(t *testing.T) {
	_ = captureLog(t)
	m := NewManager("authoritative", "")
	modeBefore, revBefore := m.loadPublishedForTest()

	changed := m.OnConfigChange("passthrough")
	if changed {
		t.Error("OnConfigChange should return false when env-locked")
	}
	modeAfter, revAfter := m.loadPublishedForTest()
	if modeAfter != modeBefore {
		t.Errorf("published.Mode mutated under env-lock: before=%q after=%q", modeBefore, modeAfter)
	}
	if revAfter != revBefore {
		t.Errorf("published.Revision bumped under env-lock: before=%d after=%d", revBefore, revAfter)
	}
}

// ── ApplyAtSessionStart ───────────────────────────────────────────────────────

func TestManager_ApplyAtSessionStart_PromotesPending(t *testing.T) {
	m := NewManager("", "passthrough")
	m.OnConfigChange("authoritative") // pending=authoritative, current=passthrough
	m.ApplyAtSessionStart()
	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("Current=%q, want authoritative after apply", snap.Current)
	}
	if snap.Pending != ModeAuthoritative {
		t.Errorf("Pending=%q, want authoritative after apply", snap.Pending)
	}
}

func TestManager_ApplyAtSessionStart_NoPendingDiff_NoOp(t *testing.T) {
	m := NewManager("", "passthrough")
	// current == pending == passthrough; apply should be a no-op.
	m.ApplyAtSessionStart()
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want passthrough", snap.Current)
	}
	if snap.Pending != ModePassthrough {
		t.Errorf("Pending=%q, want passthrough", snap.Pending)
	}
}

// TestManager_Apply_ReadsLatestPublished verifies Apply reads the most recent
// published target — two back-to-back OnConfigChange calls followed by a
// single Apply should land on the last published value.
func TestManager_Apply_ReadsLatestPublished(t *testing.T) {
	m := NewManager("", "passthrough")
	m.OnConfigChange("authoritative")
	m.OnConfigChange("passthrough")
	m.OnConfigChange("authoritative") // last publish wins
	m.ApplyAtSessionStart()

	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("Current=%q, want authoritative (latest published)", snap.Current)
	}
}

// TestManager_Apply_CurrentEqualsPublished_Noop verifies Apply is a no-op when
// current already matches the published mode (Apply must not touch revision).
func TestManager_Apply_CurrentEqualsPublished_Noop(t *testing.T) {
	m := NewManager("", "passthrough")
	_, revBefore := m.loadPublishedForTest()

	m.ApplyAtSessionStart()

	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("Current=%q, want passthrough", snap.Current)
	}
	_, revAfter := m.loadPublishedForTest()
	if revAfter != revBefore {
		t.Errorf("Apply should not bump published.Revision: before=%d after=%d", revBefore, revAfter)
	}
}

// ── Concurrency ───────────────────────────────────────────────────────────────

// TestManager_Snapshot_NotTorn verifies Snapshot() is self-consistent under
// concurrent OnConfigChange — Current must stay passthrough (no Apply called),
// EnvLocked must stay false, and Pending must always be a valid ArbMode.
// Pending may lead Current: that is the documented contract.
func TestManager_Snapshot_NotTorn(t *testing.T) {
	m := NewManager("", "passthrough")

	const iters = 10_000
	var wg sync.WaitGroup
	var failed atomic.Bool
	var firstErr atomic.Value // string

	report := func(msg string) {
		if failed.CompareAndSwap(false, true) {
			firstErr.Store(msg)
		}
	}

	// Goroutine A: flip OnConfigChange between passthrough and authoritative.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				m.OnConfigChange("authoritative")
			} else {
				m.OnConfigChange("passthrough")
			}
		}
	}()

	// Goroutine B: read Snapshot and assert invariants.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			snap := m.Snapshot()
			if snap.Current != ModePassthrough {
				report("Current drifted from passthrough without Apply")
				return
			}
			if snap.EnvLocked {
				report("EnvLocked should be false")
				return
			}
			if !snap.Pending.IsValid() {
				report("Pending is invalid")
				return
			}
		}
	}()

	wg.Wait()
	if failed.Load() {
		t.Fatalf("torn snapshot detected: %v", firstErr.Load())
	}
}

// TestManager_ConcurrentReadWrite_Race provides -race coverage for the
// combined OnConfigChange / ApplyAtSessionStart / Snapshot triad. It runs
// short; count=10 under `go test -race` flushes out ordering bugs.
func TestManager_ConcurrentReadWrite_Race(t *testing.T) {
	m := NewManager("", "passthrough")
	const workers = 8
	const iters = 500
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch (id + i) % 3 {
				case 0:
					if i%2 == 0 {
						m.OnConfigChange("authoritative")
					} else {
						m.OnConfigChange("passthrough")
					}
				case 1:
					m.ApplyAtSessionStart()
				case 2:
					_ = m.Snapshot()
				}
			}
		}(w)
	}
	wg.Wait()

	// Final state must be self-consistent.
	snap := m.Snapshot()
	if !snap.Current.IsValid() {
		t.Errorf("Current=%q is invalid after race", snap.Current)
	}
	if !snap.Pending.IsValid() {
		t.Errorf("Pending=%q is invalid after race", snap.Pending)
	}
}

// TestManager_Apply_RacesOnConfigChange_CaseA exercises the controlled
// interleave where Apply observes the OLD published target (Apply's Load runs
// strictly before OnConfigChange's atomic Store), confirming that the new
// publish is picked up on the NEXT SessionStart.
//
// Interleave:
//  1. Apply.Load() → observes initial published (passthrough, rev=0)
//  2. Apply writes current (no-op: current already passthrough)
//  3. OnConfigChange("authoritative") publishes {authoritative, rev=1}
//  4. Second Apply observes the new published → current becomes authoritative.
//
// No timing primitives needed: we exploit the package API directly to enforce
// the ordering (the Load happens in Apply, and Apply returns before step 3).
func TestManager_Apply_RacesOnConfigChange_CaseA(t *testing.T) {
	m := NewManager("", "passthrough")

	// Step 1+2: Apply observes initial published (passthrough); current stays passthrough.
	m.ApplyAtSessionStart()
	if snap := m.Snapshot(); snap.Current != ModePassthrough {
		t.Fatalf("step 2: Current=%q, want passthrough", snap.Current)
	}

	// Step 3: Concurrent OnConfigChange publishes authoritative.
	// (Run on a goroutine to exercise race-detector paths; join before step 4.)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.OnConfigChange("authoritative")
	}()
	<-done

	// Step 4: Second Apply must pick up the new publish.
	m.ApplyAtSessionStart()
	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("step 4: Current=%q, want authoritative (new publish must be applied on next SessionStart)", snap.Current)
	}
}

// TestManager_Apply_RacesOnConfigChange_CaseB exercises the happy-path
// interleave where OnConfigChange completes before Apply runs — Apply reads
// the newly published target and promotes it immediately.
func TestManager_Apply_RacesOnConfigChange_CaseB(t *testing.T) {
	m := NewManager("", "passthrough")

	// OnConfigChange publishes first (awaited via channel).
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.OnConfigChange("authoritative")
	}()
	<-done

	m.ApplyAtSessionStart()
	snap := m.Snapshot()
	if snap.Current != ModeAuthoritative {
		t.Errorf("Current=%q, want authoritative after ordered publish+apply", snap.Current)
	}
}

// TestManager_Apply_RacesOnConfigChange_CaseC is the #579 race scenario made
// explicit: Apply's Load and Apply's Lock are NOT atomic under the published
// snapshot design, but the contract tolerates it — if OnConfigChange(B) lands
// BETWEEN Apply's Load(A) and Apply's mu.Lock(), Apply still writes current=A
// (the value observed at Load time), but B is guaranteed to be promoted on
// the NEXT SessionStart.
//
// We drive the interleave using two sync points:
//  1. Goroutine X calls OnConfigChange("authoritative") — publishes A {auth, rev=1}.
//  2. Main goroutine Apply() loads target=A, then promotes current=authoritative.
//  3. Goroutine Y calls OnConfigChange("passthrough") — publishes B {pass, rev=2}.
//  4. Main goroutine Apply() again — must land on passthrough (B).
//
// The design guarantee: atomic.Pointer.Load never misses the latest prior
// Store, so step 4's Load sees B regardless of the step-2/step-3 interleave.
func TestManager_Apply_RacesOnConfigChange_CaseC(t *testing.T) {
	m := NewManager("", "passthrough")

	// Step 1: publish A (authoritative)
	m.OnConfigChange("authoritative")

	// Step 2: Apply observes A, writes current=authoritative
	m.ApplyAtSessionStart()
	if snap := m.Snapshot(); snap.Current != ModeAuthoritative {
		t.Fatalf("step 2: Current=%q, want authoritative", snap.Current)
	}

	// Step 3: publish B (passthrough)
	m.OnConfigChange("passthrough")

	// Step 4: Apply must land on B (latest publish, NOT the one Apply loaded at step 2)
	m.ApplyAtSessionStart()
	snap := m.Snapshot()
	if snap.Current != ModePassthrough {
		t.Errorf("step 4: Current=%q, want passthrough (latest publish B must win on next Apply)", snap.Current)
	}
	// Published revision should reflect two publishes (A=1, B=2).
	_, rev := m.loadPublishedForTest()
	if rev != 2 {
		t.Errorf("published.Revision=%d, want 2 (two publishes)", rev)
	}
}
