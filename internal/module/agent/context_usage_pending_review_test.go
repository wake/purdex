package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/statuspending"
)

// codex R1 + attack on #2549.

// A timestamp from the future (a clock that stepped back after the proxy wrote) is not a reading to believe: it would beat every
// later live POST. Dropped, file deleted. Mutation: no future bound → applied (red).
func TestPendingStatuslines_AFutureTimestampIsDropped(t *testing.T) {
	m, dir := pendingModule(t)
	statuspending.Write(dir, statusline("S", 1, "from-the-future"), usageNow()+10*60*1000)
	m.applyPendingStatuslines(context.Background())
	if _, ok := m.ContextUsage("S"); ok {
		t.Fatal("a future payload was applied")
	}
	if got, _ := statuspending.Load(dir); len(got) != 0 {
		t.Fatalf("file left: %+v", got)
	}
}

// The file is the only copy until the reading is on disk: it is deleted after the flush that persisted it, and kept when the
// flush failed (the next boot retries). Mutations: delete before the flush → the failure case loses it (red); never flush → the
// row is missing (red).
func TestPendingStatuslines_AreDeletedOnlyOnceTheyArePersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	a.pendingDir = filepath.Join(t.TempDir(), "p")
	statuspending.Write(a.pendingDir, statusline("S", 7, "pending-model"), usageNow()-1000)
	a.applyPendingStatuslines(context.Background())
	rows, err := a.usage.LoadAll()
	if err != nil || len(rows) != 1 || rows[0].ModelID != "pending-model" {
		t.Fatalf("persisted rows = %+v (%v)", rows, err)
	}
	if got, _ := statuspending.Load(a.pendingDir); len(got) != 0 {
		t.Fatalf("file left after the reading was persisted: %+v", got)
	}

	// a flush that fails keeps the file
	b := moduleOn(t, path)
	b.pendingDir = filepath.Join(t.TempDir(), "p")
	statuspending.Write(b.pendingDir, statusline("S", 8, "second"), usageNow()-500)
	if _, err := b.events.ExecRawForTest(`DROP TABLE context_usage`); err != nil { // the rows' table is gone: Upsert fails
		t.Fatal(err)
	}
	b.applyPendingStatuslines(context.Background())
	if u, ok := b.ContextUsage("S"); !ok || u.ModelID != "second" {
		t.Fatalf("the reading was not applied in memory: %+v ok=%v", u, ok)
	}
	if got, _ := statuspending.Load(b.pendingDir); len(got) != 1 {
		t.Fatalf("the file was deleted although the reading never reached the disk: %+v", got)
	}
}

// The daemon deletes the version it loaded: a newer payload the proxy wrote after the Load stays. Mutation: unconditional Remove
// → the newer file is gone (red).
func TestPendingStatuslines_ANewerFileWrittenDuringTheBootStays(t *testing.T) {
	m, dir := pendingModule(t)
	statuspending.Write(dir, statusline("S", 1, "loaded"), usageNow()-2000)
	m.pendingAfterLoad = func() { statuspending.Write(dir, statusline("S", 2, "arrived-meanwhile"), usageNow()-1000) }
	m.applyPendingStatuslines(context.Background())
	got, _ := statuspending.Load(dir)
	if len(got) != 1 {
		t.Fatalf("load = %+v, want the newer file kept", got)
	}
}
