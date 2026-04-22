package arbmode

import (
	"log"
	"strings"
	"sync"
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

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestManager_Snapshot_NotTornDuringConfigChange(t *testing.T) {
	m := NewManager("", "passthrough") // current=passthrough, pending=passthrough

	const iters = 10_000
	var wg sync.WaitGroup

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
			// current must stay passthrough (we never call ApplyAtSessionStart).
			if snap.Current != ModePassthrough {
				t.Errorf("torn read: Current=%q, want passthrough", snap.Current)
				return
			}
			// EnvLocked must stay false (no env set).
			if snap.EnvLocked {
				t.Error("torn read: EnvLocked should be false")
				return
			}
			// Pending must be one of the two valid modes (never zero/invalid).
			if !snap.Pending.IsValid() {
				t.Errorf("torn read: Pending=%q is invalid", snap.Pending)
				return
			}
		}
	}()

	wg.Wait()
}
