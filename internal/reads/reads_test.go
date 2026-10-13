package reads

import (
	"encoding/json"
	"fmt"
	"testing"
)

// BG-4 (badge spec §7.2): a read is "through" a hook cursor, never a time.

func c(epoch string, seq uint64) Cursor { return Cursor{Epoch: epoch, Seq: seq} }

// The cover rule: same epoch and the unread's seq ≤ T.seq clears; a later seq or another epoch does not; an unread with no
// cursor is covered by any read. Mutation gate: ignore the epoch / compare seq with < → red.
func TestCursor_Covers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		read, seen Cursor
		want       bool
	}{
		{"same epoch, seq below", c("e1", 10), c("e1", 7), true},
		{"same epoch, seq equal", c("e1", 10), c("e1", 10), true},
		{"same epoch, later seq", c("e1", 10), c("e1", 11), false},
		{"another epoch, lower seq", c("e2", 10), c("e1", 3), false},
		{"another epoch, higher seq", c("e1", 10), c("e2", 99), false},
		{"an unread with no cursor is covered by any read", c("e1", 10), c("", 0), true},
		{"covered even by a read with no cursor", c("", 0), c("", 0), true},
		{"a cursorless read covers no cursored unread", c("", 0), c("e1", 1), false},
		{"seq 0 of a real epoch", c("e1", 0), c("e1", 0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.read.Covers(tc.seen); got != tc.want {
				t.Fatalf("%+v.Covers(%+v) = %v, want %v", tc.read, tc.seen, got, tc.want)
			}
		})
	}
}

func TestCursor_JSONShape(t *testing.T) {
	raw, _ := json.Marshal(Read{Code: "c1", Through: c("e1", 7)})
	if string(raw) != `{"code":"c1","through":{"epoch":"e1","seq":7}}` {
		t.Fatalf("%s", raw)
	}
}

// lastRead only moves forward: a smaller T, an equal T and another epoch's T never replace the current one; the store says
// whether a write was an advance (only an advance is announced).
func TestStore_OnlyMovesForward(t *testing.T) {
	s := NewStore(10)
	s.SetCurrentEpoch("e2")
	if !s.Advance("c1", c("e2", 5)) {
		t.Fatal("first read not accepted")
	}
	for name, stale := range map[string]Cursor{"smaller": c("e2", 4), "equal": c("e2", 5), "an older epoch": c("e1", 99), "no cursor": c("", 0)} {
		if s.Advance("c1", stale) {
			t.Fatalf("%s T replaced a current-epoch one", name)
		}
	}
	if got, _ := s.Get("c1"); got != c("e2", 5) {
		t.Fatalf("lastRead = %+v", got)
	}
	if !s.Advance("c1", c("e2", 9)) {
		t.Fatal("a larger T was refused")
	}
	if got, _ := s.Get("c1"); got != c("e2", 9) {
		t.Fatalf("lastRead = %+v", got)
	}
}

// A T of the daemon's current epoch replaces one of an older epoch (a restart); a cursorless one is replaced by any cursor;
// nothing replaces anything when the current epoch is unknown.
func TestStore_EpochTransitions(t *testing.T) {
	s := NewStore(10)
	s.Advance("old", c("e1", 50)) // a client's read from before a restart, current epoch unknown yet
	s.SetCurrentEpoch("e2")
	if !s.Advance("old", c("e2", 1)) {
		t.Fatal("the current epoch's T did not replace an older epoch's")
	}
	s2 := NewStore(10)
	s2.Advance("x", c("", 0))
	if !s2.Advance("x", c("e1", 3)) {
		t.Fatal("a cursor did not replace a cursorless read")
	}
	s3 := NewStore(10)
	s3.Advance("y", c("e1", 3))
	if s3.Advance("y", c("e2", 1)) {
		t.Fatal("another epoch replaced it with no current epoch known")
	}
}

// Bounded: the least recently updated code goes first; a write that does not advance is not an update.
// Mutation gate: no cap / evict the newest → red.
func TestStore_EvictsTheLeastRecentlyUpdated(t *testing.T) {
	s := NewStore(4)
	for i := 0; i < 4; i++ {
		s.Advance(fmt.Sprintf("c%d", i), c("e1", 1))
	}
	s.Advance("c0", c("e1", 2)) // c0 is now the most recent
	s.Advance("c1", c("e1", 1)) // not an advance: c1 stays the oldest
	s.Advance("c9", c("e1", 1)) // evicts c1
	if _, ok := s.Get("c1"); ok {
		t.Fatal("c1 should have been evicted")
	}
	for _, code := range []string{"c0", "c2", "c3", "c9"} {
		if _, ok := s.Get(code); !ok {
			t.Fatalf("%s was evicted", code)
		}
	}
	if s.Len() != 4 {
		t.Fatalf("len = %d", s.Len())
	}
}

func TestStore_4096IsTheDefaultBound(t *testing.T) {
	s := NewStore(0)
	for i := 0; i < 4096+10; i++ {
		s.Advance(fmt.Sprintf("c%05d", i), c("e1", 1))
	}
	if s.Len() != 4096 {
		t.Fatalf("len = %d, want 4096", s.Len())
	}
	if _, ok := s.Get("c00000"); ok {
		t.Fatal("the oldest survived")
	}
}

// Prune drops the codes a session list no longer has; Snapshot lists what is left, oldest update first.
func TestStore_PruneAndSnapshot(t *testing.T) {
	s := NewStore(10)
	s.Advance("a", c("e1", 1))
	s.Advance("b", c("e1", 2))
	s.Advance("c", c("e1", 3))
	if n := s.Prune(func(code string) bool { return code != "b" }); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	snap := s.Snapshot()
	if len(snap) != 2 || snap[0].Code != "a" || snap[1].Code != "c" || snap[1].Through != c("e1", 3) {
		t.Fatalf("snapshot = %+v", snap)
	}
	if empty := NewStore(3).Snapshot(); empty == nil || len(empty) != 0 {
		t.Fatalf("an empty snapshot must be [] (not null): %#v", empty)
	}
}
