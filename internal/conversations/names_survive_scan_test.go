package conversations

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

func openMetaForNames(t *testing.T) *store.MetaStore {
	t.Helper()
	m, err := store.OpenMeta(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func requireName(t *testing.T, m *store.MetaStore, sid, want string) {
	t.Helper()
	got, err := m.ConversationNames().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got[sid] != want {
		t.Fatalf("conversation_names[%s] = %q, want %q (all: %v)", sid, got[sid], want, got)
	}
}

// A registry name lives in its own table: the index's whole-row UpsertBatch
// (what a scan does) must not wash it out.
func TestConversationNames_SurviveWholeRowIndexOverwrite(t *testing.T) {
	m := openMetaForNames(t)
	ctx := context.Background()
	if err := m.ConversationNames().Upsert(ctx, sidA, "purdex-47", 1); err != nil {
		t.Fatal(err)
	}
	row := store.ConversationIndexRow{SessionID: sidA, TranscriptPath: "/x/a.jsonl", Cwd: "/w", FirstSeenAt: 1, LastSeenAt: 1}
	for i := 0; i < 2; i++ {
		row.LastSeenAt = int64(10 + i)
		if err := m.Conversations().UpsertBatch(ctx, []store.ConversationIndexRow{row}); err != nil {
			t.Fatal(err)
		}
		requireName(t, m, sidA, "purdex-47")
	}
}

// The same through the real Scan, over a file that is rescanned after it grew.
func TestConversationNames_SurviveAFullScan(t *testing.T) {
	m := openMetaForNames(t)
	ctx := context.Background()
	root, a, _ := fixtureRoot(t)
	if err := m.ConversationNames().Upsert(ctx, sidA, "purdex-47", 1); err != nil {
		t.Fatal(err)
	}
	runScan(t, root, m.Conversations(), t0)
	requireName(t, m, sidA, "purdex-47")

	f, err := os.OpenFile(a, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(lines(t, aiTitle("a newer title"))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	later := t0.Add(time.Minute)
	if err := os.Chtimes(a, later, later); err != nil {
		t.Fatal(err)
	}
	res := runScan(t, root, m.Conversations(), later)
	if res.Reread == 0 {
		t.Fatal("the grown file was not rescanned; the test would prove nothing")
	}
	requireName(t, m, sidA, "purdex-47")
	rows, err := m.Conversations().All(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("index rows = %d, %v", len(rows), err)
	}
}
