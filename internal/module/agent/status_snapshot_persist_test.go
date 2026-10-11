package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
	_ "modernc.org/sqlite"
)

// The UI's statusLine snapshot (usage, cwd, model...) per tmux session survives a daemon restart, persisted like the
// context usage: dirty-marked in memory, one coalesced flush, restored at boot for the sessions that still exist.

func sessionsOf(inst string, codes ...string) *fakeSessionProvider {
	infos := make([]session.SessionInfo, 0, len(codes))
	for _, c := range codes {
		infos = append(infos, session.SessionInfo{Code: c, Name: "n-" + c})
	}
	return &fakeSessionProvider{sessions: infos, tmuxInstance: inst}
}

func postSnap(m *Module, code, raw string) {
	body := fmt.Sprintf(`{"tmux_session":"n-%s","agent_type":"cc","raw_status":%s}`, code, raw)
	m.handleAgentStatus(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body)))
}

func withClock(t *testing.T, ms *int64) {
	t.Helper()
	old := usageNow
	usageNow = func() int64 { return *ms }
	t.Cleanup(func() { usageNow = old })
}

func replayOf(t *testing.T, m *Module) map[string]string {
	t.Helper()
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	m.sendStatuslineSnapshot(sub)
	out := map[string]string{}
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct{ Session, Value string }
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatal(err)
			}
			out[env.Session] = env.Value
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func atOf(t *testing.T, value string) int64 {
	t.Helper()
	var v struct {
		At int64 `json:"at"`
	}
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		t.Fatal(err)
	}
	return v.At
}

func TestStatusSnapshotPersist_SurvivesARestartWithItsTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	now := int64(1_000_000)
	withClock(t, &now)
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1", "c2")
	postSnap(a, "c1", `{"cwd":"/x"}`)
	now = 2_000_000
	postSnap(a, "c2", `{"cwd":"/y"}`)
	want := replayOf(t, a)
	if n := a.flushStatusSnapshots(); n != 2 {
		t.Fatalf("flush wrote %d, want 2", n)
	}

	now = 9_000_000
	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1", "c2")
	b.restoreStatusSnapshots(context.Background())
	got := replayOf(t, b)
	if len(got) != 2 || got["c1"] != want["c1"] || got["c2"] != want["c2"] {
		t.Fatalf("restored %v, want %v", got, want)
	}
	if atOf(t, got["c1"]) != 1_000_000 || atOf(t, got["c2"]) != 2_000_000 {
		t.Fatalf("at lost: %v", got)
	}
	if !strings.Contains(got["c1"], `"cwd":"/x"`) || !strings.Contains(got["c1"], `"agent_type":"cc"`) {
		t.Fatalf("content lost: %s", got["c1"])
	}
}

func TestStatusSnapshotPersist_SameValueDoesNotMarkDirty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	now := int64(1_000_000)
	withClock(t, &now)
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	postSnap(a, "c1", `{"cwd":"/x"}`)
	if n := a.flushStatusSnapshots(); n != 1 {
		t.Fatalf("first flush %d", n)
	}
	now += 1000
	postSnap(a, "c1", `{"cwd":"/x"}`) // same value, persisted copy fresh
	if n := a.flushStatusSnapshots(); n != 0 {
		t.Fatalf("same value rewrote %d rows", n)
	}
	now += 1000
	postSnap(a, "c1", `{"cwd":"/z"}`)
	if n := a.flushStatusSnapshots(); n != 1 {
		t.Fatalf("changed value wrote %d", n)
	}
	now += usageRefreshAfter.Milliseconds() + 1
	postSnap(a, "c1", `{"cwd":"/z"}`) // same value but persisted copy is stale
	if n := a.flushStatusSnapshots(); n != 1 {
		t.Fatalf("stale refresh wrote %d", n)
	}
}

func TestStatusSnapshotPersist_NewerSnapshotAfterBootIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	now := int64(1_000_000)
	withClock(t, &now)
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	postSnap(a, "c1", `{"cwd":"/old"}`)
	a.flushStatusSnapshots()

	now = 5_000_000
	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1")
	postSnap(b, "c1", `{"cwd":"/new"}`)
	b.restoreStatusSnapshots(context.Background())
	got := replayOf(t, b)
	if !strings.Contains(got["c1"], "/new") || atOf(t, got["c1"]) != 5_000_000 {
		t.Fatalf("live snapshot was overwritten: %s", got["c1"])
	}
}

func TestStatusSnapshotPersist_EmptyDBAndMissingTableStartFine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	a.restoreStatusSnapshots(context.Background()) // empty table
	if len(replayOf(t, a)) != 0 {
		t.Fatal("nothing to restore")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TABLE status_snapshots`); err != nil {
		t.Fatal(err)
	}
	a.restoreStatusSnapshots(context.Background()) // table gone: log only
	postSnap(a, "c1", `{}`)
	if n := a.flushStatusSnapshots(); n != 0 { // write fails: logged, retried later
		t.Fatalf("wrote %d into a missing table", n)
	}
	if len(replayOf(t, a)) != 1 {
		t.Fatal("memory snapshot must still work")
	}
}

func TestStatusSnapshotPersist_GoneSessionsAndOtherTmuxGenerationDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	now := int64(1_000_000)
	withClock(t, &now)
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1", "c2")
	postSnap(a, "c1", `{}`)
	postSnap(a, "c2", `{}`)
	a.flushStatusSnapshots()

	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1") // c2 is gone
	b.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, b); len(got) != 1 || got["c1"] == "" {
		t.Fatalf("pruning failed: %v", got)
	}
	rows, _ := b.snaps.LoadAll()
	if len(rows) != 1 {
		t.Fatalf("gone row still on disk: %d", len(rows))
	}

	c := moduleOn(t, path)
	c.sessions = sessionsOf("tm2", "c1") // tmux restarted: the code may be reused
	c.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, c); len(got) != 0 {
		t.Fatalf("other generation restored: %v", got)
	}
	rows, _ = c.snaps.LoadAll()
	if len(rows) != 0 {
		t.Fatal("other-generation row kept")
	}
}

func TestStatusSnapshotPersist_CapKeepsNewest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	now := int64(1_000_000)
	withClock(t, &now)
	a := moduleOn(t, path)
	codes := make([]string, 0, statusSnapshotCap+3)
	for i := 0; i < statusSnapshotCap+3; i++ {
		codes = append(codes, fmt.Sprintf("c%d", i))
	}
	a.sessions = sessionsOf("tm1", codes...)
	for _, c := range codes {
		now++
		postSnap(a, c, `{}`)
	}
	a.snapshotMu.RLock()
	n := len(a.statusSnapshots)
	_, oldest := a.statusSnapshots["c0"]
	a.snapshotMu.RUnlock()
	if n != statusSnapshotCap || oldest {
		t.Fatalf("cap: n=%d oldestKept=%v", n, oldest)
	}
	a.flushStatusSnapshots()
	rows, _ := a.snaps.LoadAll()
	if len(rows) != statusSnapshotCap {
		t.Fatalf("disk rows %d", len(rows))
	}
}

func TestStatusSnapshotPersist_ClearedDeletesDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	postSnap(a, "c1", `{}`)
	a.flushStatusSnapshots()
	a.clearStatusSnapshots()
	rows, _ := a.snaps.LoadAll()
	if len(rows) != 0 {
		t.Fatal("rows survived the clear")
	}
	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1")
	b.restoreStatusSnapshots(context.Background())
	if len(replayOf(t, b)) != 0 {
		t.Fatal("cleared snapshot came back")
	}
}

func TestStatusSnapshotPersist_StopFlushes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	a.startContextUsageFlush()
	postSnap(a, "c1", `{"cwd":"/s"}`)
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := a.snaps.LoadAll()
	if len(rows) != 1 {
		t.Fatalf("Stop did not flush: %d rows", len(rows))
	}
}

// Measured cost of a flush (alpha.610 lesson); run with -bench StatusSnapshotFlush.
func benchFlush(b *testing.B, n int) {
	path := filepath.Join(b.TempDir(), "agent.db")
	a := moduleOn(b, path)
	codes := make([]string, n)
	for i := range codes {
		codes[i] = fmt.Sprintf("c%d", i)
	}
	a.sessions = sessionsOf("tm1", codes...)
	payload := `{"cwd":"/Users/wake/Workspace/wake/purdex","model":{"id":"claude-opus-5-5","display_name":"Opus"},"context_window":{"used_percentage":41.5,"context_window_size":1000000},"rate_limits":{"five_hour":{"used_percentage":30,"resets_at":1790000000},"seven_day":{"used_percentage":55,"resets_at":1790500000}},"cost":{"total_cost_usd":12.3,"total_duration_ms":123456},"workspace":{"current_dir":"/Users/wake/Workspace/wake/purdex","project_dir":"/Users/wake/Workspace/wake/purdex"}}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer() // only the flush is measured
		for _, c := range codes {
			postSnap(a, c, strings.Replace(payload, "123456", fmt.Sprint(i), 1))
		}
		b.StartTimer()
		a.flushStatusSnapshots()
	}
}

func BenchmarkStatusSnapshotFlush13(b *testing.B)  { benchFlush(b, 13) }
func BenchmarkStatusSnapshotFlushCap(b *testing.B) { benchFlush(b, statusSnapshotCap) }
