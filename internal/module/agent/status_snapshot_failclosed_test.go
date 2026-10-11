package agent

import (
	"context"
	"path/filepath"
	"testing"
)

// Fail closed: a generation either side does not know never restores a row.
func TestStatusSnapshotPersist_UnknownGenerationFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")

	// flush with an unknown generation writes nothing
	a := moduleOn(t, path)
	a.sessions = sessionsOf("", "c1")
	postSnap(a, "c1", `{}`)
	if n := a.flushStatusSnapshots(); n != 0 {
		t.Fatalf("wrote %d rows without a generation", n)
	}
	if rows, _ := a.snaps.LoadAll(); len(rows) != 0 {
		t.Fatal("row persisted without a generation")
	}
	// once known, the still-dirty row is written
	a.sessions = sessionsOf("tm1", "c1")
	if n := a.flushStatusSnapshots(); n != 1 {
		t.Fatalf("wrote %d after generation known, want 1", n)
	}

	// current generation unknown: restore nothing, drop nothing
	b := moduleOn(t, path)
	b.sessions = sessionsOf("", "c1")
	b.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, b); len(got) != 0 {
		t.Fatalf("restored with unknown current generation: %v", got)
	}
	if rows, _ := b.snaps.LoadAll(); len(rows) != 1 {
		t.Fatalf("row dropped on a transient unknown: %d", len(rows))
	}

	// row without a generation is dropped even when the current one is known
	if err := b.snaps.Upsert(nil); err != nil {
		t.Fatal(err)
	}
	c := moduleOn(t, path)
	c.sessions = sessionsOf("tm1", "c1")
	if err := c.snaps.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	postSnap(c, "c1", `{}`)
	c.flushStatusSnapshots()
	rows, _ := c.snaps.LoadAll()
	rows[0].TmuxInstance = ""
	if err := c.snaps.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	d := moduleOn(t, path)
	d.sessions = sessionsOf("tm1", "c1")
	d.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, d); len(got) != 0 {
		t.Fatalf("restored a row with no generation: %v", got)
	}

	// same generation restores
	e := moduleOn(t, path)
	e.sessions = sessionsOf("tm1", "c1")
	postSnap(e, "c1", `{}`)
	e.flushStatusSnapshots()
	f := moduleOn(t, path)
	f.sessions = sessionsOf("tm1", "c1")
	f.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, f); got["c1"] == "" {
		t.Fatalf("same generation not restored: %v", got)
	}
}
