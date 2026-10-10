package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/statuspending"
)

// #2545: at boot the daemon applies the payloads the statusline proxy could not deliver while it was down — only to the usage
// readings, only when they are newer than what it has, only for sessions that are live — and deletes the files.

// pendingModule is a module whose live session S has a stored reading (model old-model, taken at At), and a pending directory.
func pendingModule(t *testing.T) (*Module, string) {
	t.Helper()
	m := newTestModule(t)
	seedRootWithIdentity(t, m, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	m.pendingDir = filepath.Join(t.TempDir(), "statusline-pending")
	return m, m.pendingDir
}

func TestPendingStatuslines_AreAppliedAtBootWhenNewer(t *testing.T) {
	m, dir := pendingModule(t)
	usageNow = func() int64 { return 1000 }
	origNow := usageNow
	t.Cleanup(func() { usageNow = origNow })
	m.recordContextUsage(statusline("S", 10, "old-model"))
	if err := statuspending.Write(dir, statusline("S", 33, "new-model"), 2000); err != nil {
		t.Fatal(err)
	}
	m.applyPendingStatuslines(context.Background())
	u, ok := m.ContextUsage("S")
	if !ok || u.ModelID != "new-model" || u.At != 2000 || u.UsedPercentage == nil || *u.UsedPercentage != 33 {
		t.Fatalf("usage = %+v ok=%v, want new-model at 2000", u, ok)
	}
	if got, _ := statuspending.Load(dir); len(got) != 0 {
		t.Fatalf("the applied file stayed: %+v", got)
	}
}

// A session the daemon had no reading of is filled; an older pending payload never replaces a newer reading; both orders of
// "live POST" and "boot apply" end with the newer one. Mutations: apply regardless of age → red; apply skipping the lock → race.
func TestPendingStatuslines_NeverGoBackwards(t *testing.T) {
	m, dir := pendingModule(t)
	usageNow = func() int64 { return 5000 }
	origNow := usageNow
	t.Cleanup(func() { usageNow = origNow })
	m.recordContextUsage(statusline("S", 10, "live-model")) // At = 5000
	statuspending.Write(dir, statusline("S", 99, "stale-pending"), 3000)
	m.applyPendingStatuslines(context.Background())
	if u, _ := m.ContextUsage("S"); u.ModelID != "live-model" || u.At != 5000 {
		t.Fatalf("a pending payload older than the reading replaced it: %+v", u)
	}
	if got, _ := statuspending.Load(dir); len(got) != 0 {
		t.Fatal("a skipped file must still be deleted")
	}

	// apply first, then the live POST: the live one is newer
	m2, dir2 := pendingModule(t)
	statuspending.Write(dir2, statusline("S", 20, "pending-model"), 1000)
	m2.applyPendingStatuslines(context.Background())
	usageNow = func() int64 { return 6000 }
	m2.recordContextUsage(statusline("S", 30, "live-after"))
	if u, _ := m2.ContextUsage("S"); u.ModelID != "live-after" {
		t.Fatalf("after: %+v", u)
	}
}

// Not live at boot, expired, or not a valid entry: deleted, never applied.
func TestPendingStatuslines_DeadExpiredAndInvalidAreDroppedNotApplied(t *testing.T) {
	m, dir := pendingModule(t)
	usageNow = func() int64 { return 10 * statuspending.MaxAge }
	origNow := usageNow
	t.Cleanup(func() { usageNow = origNow })
	statuspending.Write(dir, statusline("GONE", 1, "m"), 10*statuspending.MaxAge-1) // no frame: not live
	statuspending.Write(dir, statusline("S", 1, "too-old"), 10*statuspending.MaxAge-statuspending.MaxAge-1)
	os.WriteFile(filepath.Join(dir, "junk.json"), []byte("nope"), 0o600)
	m.applyPendingStatuslines(context.Background())
	for _, sid := range []string{"GONE", "S"} {
		if _, ok := m.ContextUsage(sid); ok {
			t.Fatalf("%s was applied", sid)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

// Only the usage reading catches up: the UI's snapshot and the broadcasts are the live screen's and an old payload is not
// replayed into them. Mutation: apply via the whole status path → a snapshot appears (red).
func TestPendingStatuslines_OnlyTouchTheUsageReading(t *testing.T) {
	m, dir := pendingModule(t)
	statuspending.Write(dir, statusline("S", 5, "m"), usageNow()-1000)
	m.applyPendingStatuslines(context.Background())
	m.snapshotMu.RLock()
	n := len(m.statusSnapshots)
	m.snapshotMu.RUnlock()
	if n != 0 {
		t.Fatalf("%d status snapshots were created", n)
	}
	if _, ok := m.ContextUsage("S"); !ok {
		t.Fatal("the reading was not applied")
	}
}

// The persisted copy follows, so a restart after the catch-up keeps it. Mutation: applied without marking dirty → red.
func TestPendingStatuslines_AreFlushedLikeAnyReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	a.pendingDir = filepath.Join(t.TempDir(), "p")
	statuspending.Write(a.pendingDir, statusline("S", 7, "pending-model"), usageNow()-1000)
	a.applyPendingStatuslines(context.Background())
	if n := a.flushContextUsage(); n != 1 {
		t.Fatalf("flush wrote %d rows, want 1", n)
	}
	b := moduleOn(t, path)
	b.restoreContextUsage(context.Background())
	if u, ok := b.ContextUsage("S"); !ok || u.ModelID != "pending-model" {
		t.Fatalf("after a restart: %+v ok=%v", u, ok)
	}
}

// The daemon's own lifecycle: a reading stored before the outage, a payload the proxy kept during it, and Start brings the
// newer one in. Mutation: Start does not call the catch-up → the old reading stays (red).
func TestPendingStatuslines_StartCatchesUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	a.recordContextUsage(statusline("S", 10, "model-before-the-outage"))
	a.flushContextUsage()

	b := moduleOn(t, path)
	b.pendingDir = filepath.Join(t.TempDir(), "statusline-pending")
	statuspending.Write(b.pendingDir, statusline("S", 20, "model-during-the-outage"), usageNow()+1000)
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(context.Background()) })
	if u, ok := b.ContextUsage("S"); !ok || u.ModelID != "model-during-the-outage" {
		t.Fatalf("after Start: %+v ok=%v", u, ok)
	}
}

// The directory the module reads is the one the proxy writes: both come from statuspending.DirFor over the same config.
// Mutation: a different path in Init → red.
func TestPendingStatuslines_TheModuleReadsTheProxysDirectory(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	m := newTestModule(t)
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if m.pendingDir != statuspending.DirFor(cfg) {
		t.Fatalf("module dir %q, proxy dir %q", m.pendingDir, statuspending.DirFor(cfg))
	}
}
