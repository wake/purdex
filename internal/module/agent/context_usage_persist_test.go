package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// #2406: the last statusline reading of each CC session survives a daemon restart (it is reported again only when the
// session next has activity), restored only for sessions that are still live; writes are coalesced, never one per statusline.
// Mutation gates: no flush / restore of a dead session / a write per statusline → red.

// moduleOn is an agent module over the events DB at path, as a restarted daemon would open it.
func moduleOn(t *testing.T, path string) *Module {
	t.Helper()
	events, err := store.OpenAgentEvent(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { events.Close() })
	m, err := New(events)
	if err != nil {
		t.Fatal(err)
	}
	if m.traceSink != nil {
		t.Cleanup(func() { m.traceSink.Close() })
	}
	return m
}

func statusline(sid string, pct float64, model string) []byte {
	return []byte(fmt.Sprintf(`{"session_id":%q,"context_window":{"used_percentage":%v,"context_window_size":1000000},"model":{"id":%q},"effort":{"level":"low"}}`, sid, pct, model))
}

// A persisted reading is back after a restart, with its model, effort and original At.
func TestContextUsagePersist_SurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t) // the pid / start-time seams
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	a.recordContextUsage(statusline("S", 41.5, "claude-sonnet-5-5"))
	want, _ := a.ContextUsage("S")
	if n := a.flushContextUsage(); n != 1 {
		t.Fatalf("flush wrote %d rows, want 1", n)
	}

	b := moduleOn(t, path)
	b.restoreContextUsage(context.Background())
	got, ok := b.ContextUsage("S")
	if !ok || got.UsedPercentage == nil || *got.UsedPercentage != 41.5 || got.WindowSize != 1000000 || got.ModelID != "claude-sonnet-5-5" || got.Effort != "low" || got.At != want.At {
		t.Fatalf("restored = %+v ok=%v, want %+v", got, ok, want)
	}
}

// A session that is no longer live at boot is not read back, and its row is dropped.
func TestContextUsagePersist_ASessionGoneAtBootIsNotRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	seedRootWithIdentity(t, a, "%2", "cc", 102, "st-102", "T")
	a.recordContextUsage(statusline("S", 10, "m"))
	a.recordContextUsage(statusline("T", 20, "m"))
	a.recordContextUsage(statusline("NOFRAME", 30, "m")) // no terminal frame at all
	a.flushContextUsage()

	withLivePids(t, map[int]string{101: "st-101"}) // 102 is dead
	b := moduleOn(t, path)
	b.restoreContextUsage(context.Background())
	if _, ok := b.ContextUsage("S"); !ok {
		t.Fatal("the live session was not restored")
	}
	for _, sid := range []string{"T", "NOFRAME"} {
		if _, ok := b.ContextUsage(sid); ok {
			t.Fatalf("%s is not live at boot and must not be restored", sid)
		}
	}
	rows, err := b.usage.LoadAll()
	if err != nil || len(rows) != 1 || rows[0].SessionID != "S" {
		t.Fatalf("persisted rows after the restore = %+v (%v), want only S", rows, err)
	}
}

// A newer statusline reading replaces the restored one, and the replacement is what the next restart brings back.
func TestContextUsagePersist_ANewerReadingReplacesTheRestoredOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	a.recordContextUsage(statusline("S", 50, "m"))
	a.flushContextUsage()

	b := moduleOn(t, path)
	b.restoreContextUsage(context.Background())
	b.recordContextUsage(statusline("S", 60, "m"))
	if u, _ := b.ContextUsage("S"); u.UsedPercentage == nil || *u.UsedPercentage != 60 {
		t.Fatalf("after the new reading: %+v", u)
	}
	b.flushContextUsage()

	c := moduleOn(t, path)
	c.restoreContextUsage(context.Background())
	if u, _ := c.ContextUsage("S"); u.UsedPercentage == nil || *u.UsedPercentage != 60 {
		t.Fatalf("after the second restart: %+v, want the newer reading", u)
	}
	// and a restore never overwrites a reading that arrived before it ran
	d := moduleOn(t, path)
	d.recordContextUsage(statusline("S", 70, "m"))
	d.restoreContextUsage(context.Background())
	if u, _ := d.ContextUsage("S"); u.UsedPercentage == nil || *u.UsedPercentage != 70 {
		t.Fatalf("restore overwrote a fresher reading: %+v", u)
	}
}

// Writes are coalesced: a statusline stream that changes nothing writes nothing after the first flush, a change writes one
// row, and an unchanged value is rewritten only to keep its At from going stale (5 minutes).
func TestContextUsagePersist_WritesAreCoalesced(t *testing.T) {
	m := moduleOn(t, filepath.Join(t.TempDir(), "agent.db"))
	now := int64(1_000_000)
	prev := usageNow
	usageNow = func() int64 { return now }
	t.Cleanup(func() { usageNow = prev })

	for i := 0; i < 1000; i++ {
		now++
		m.recordContextUsage(statusline("S", 33, "m"))
	}
	if n := m.flushContextUsage(); n != 1 {
		t.Fatalf("1000 identical statuslines → %d rows written, want 1", n)
	}
	for i := 0; i < 1000; i++ {
		now++
		m.recordContextUsage(statusline("S", 33, "m"))
	}
	if n := m.flushContextUsage(); n != 0 {
		t.Fatalf("an unchanged stream wrote %d rows, want 0", n)
	}
	now++
	m.recordContextUsage(statusline("S", 34, "m"))
	m.recordContextUsage(statusline("S", 35, "m")) // two changes before the flush: one row
	if n := m.flushContextUsage(); n != 1 {
		t.Fatalf("a change wrote %d rows, want 1", n)
	}
	now += 6 * 60 * 1000 // the value is the same, but the persisted At is more than 5 minutes old
	m.recordContextUsage(statusline("S", 35, "m"))
	if n := m.flushContextUsage(); n != 1 {
		t.Fatalf("an unchanged value with a stale At wrote %d rows, want 1", n)
	}
}

// Stop writes what the flusher still holds, and Start brings it back for a live session: the daemon's own lifecycle, not
// only the two helpers.
func TestContextUsagePersist_StopFlushesAndStartRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	newTestModule(t)
	seedRootWithIdentity(t, a, "%1", "cc", 101, "st-101", "S")
	withLivePids(t, map[int]string{101: "st-101"})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.recordContextUsage(statusline("S", 44, "m"))
	if err := a.Stop(context.Background()); err != nil { // before any 10 s tick
		t.Fatal(err)
	}

	b := moduleOn(t, path)
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Stop(context.Background()) })
	if u, ok := b.ContextUsage("S"); !ok || u.UsedPercentage == nil || *u.UsedPercentage != 44 {
		t.Fatalf("after Stop → Start: %+v ok=%v", u, ok)
	}
}

// Removing the statusline wipes the persisted readings too: a stale one must not come back at the next boot.
func TestContextUsagePersist_RemovingTheStatuslineDropsThePersistedRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	m.recordContextUsage(statusline("S", 44, "m"))
	m.flushContextUsage()
	if rows, _ := m.usage.LoadAll(); len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", strings.NewReader(`{"action":"remove"}`))
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()
	m.handleStatuslineSetup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if rows, _ := m.usage.LoadAll(); len(rows) != 0 {
		t.Fatalf("persisted rows after the removal = %+v, want none", rows)
	}
	if _, ok := m.ContextUsage("S"); ok {
		t.Fatal("the in-memory reading stayed")
	}
}
