package journal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func pinnedIn(t *testing.T, file, id string) bool {
	t.Helper()
	es, err := Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.ID == id {
			return e.Pinned
		}
	}
	t.Fatalf("%s has no %s", file, id)
	return false
}

// An entry whose time is in October but whose block sits in the September file is found and pinned there.
// Mutation gate: derive the file from the entry's time (MonthFile(dir, e.At)) → not found (red).
func TestSetPinnedIn_FindsTheEntryWhateverTheFileIsNamed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "2026-09.md")
	oct := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	if err := Upsert(file, Entry{ID: "moved", Kind: KindComm, At: oct, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	changed, err := SetPinnedIn(dir, "moved", true)
	if err != nil || !changed || !pinnedIn(t, file, "moved") {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if changed, err = SetPinnedIn(dir, "moved", true); err != nil || changed {
		t.Fatalf("pinning again: changed=%v err=%v", changed, err)
	}
}

// A repeated id is pinned in the OLDER file, the one ReadUnit shows; the newer copy is left alone.
// Mutation gate: try the files newest first → the newer copy is pinned (red).
func TestSetPinnedIn_ARepeatedIDChangesTheOlderFile(t *testing.T) {
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "2026-08.md"), filepath.Join(dir, "2026-09.md")
	for _, f := range []string{older, newer} {
		if err := Upsert(f, Entry{ID: "dup", Kind: KindComm, At: t0, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SetPinnedIn(dir, "dup", true); err != nil {
		t.Fatal(err)
	}
	if !pinnedIn(t, older, "dup") || pinnedIn(t, newer, "dup") {
		t.Fatal("the older copy must be the pinned one, the newer left alone")
	}
}

// A month file the reader refuses (here a symlink to a file outside) is not read and not written, even when it holds the id.
// Mutation gate: skip the readMonth guard → the outside file is pinned (red).
func TestSetPinnedIn_SkipsAFileTheReaderRefuses(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := Upsert(outside, Entry{ID: "x", Kind: KindComm, At: t0, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(outside)
	if err := os.Symlink(outside, filepath.Join(dir, "2026-01.md")); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "2026-02.md")
	if err := Upsert(real, Entry{ID: "x", Kind: KindComm, At: t0, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPinnedIn(dir, "x", true); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(outside); string(after) != string(before) {
		t.Fatal("a file behind a symlink was written")
	}
	if !pinnedIn(t, real, "x") {
		t.Fatal("the real file was not pinned")
	}
}

// When the unit's byte budget leaves the older month out, the entry ReadUnit shows is the newer file's copy, and that is the
// one pinned; the older file, which the reader skipped, is not touched even though it holds the id.
// Mutation gate: read each file on its own limit, without the unit's budget → the skipped older copy is pinned (red).
func TestSetPinnedIn_FollowsTheUnitBudgetOfTheReader(t *testing.T) {
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "2026-08.md"), filepath.Join(dir, "2026-09.md")
	for _, f := range []string{older, newer} {
		if err := Upsert(f, Entry{ID: "dup", Kind: KindComm, At: t0, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(newer)
	if err != nil {
		t.Fatal(err)
	}
	withReadLimits(t, 1<<20, st.Size()) // room for the newer month only
	if _, skipped, _ := ReadUnit(dir); !slices.Equal(skippedNames(skipped), []string{"2026-08.md"}) {
		t.Fatalf("the setup must leave the older month out, skipped = %v", skipped)
	}
	before, _ := os.ReadFile(older)
	if changed, err := SetPinnedIn(dir, "dup", true); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !pinnedIn(t, newer, "dup") {
		t.Fatal("the copy the reader shows was not pinned")
	}
	if after, _ := os.ReadFile(older); string(after) != string(before) {
		t.Fatal("a month the reader left out was written")
	}
}

func TestSetPinnedIn_NoSuchEntryOrFolderIsNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := Upsert(filepath.Join(dir, "2026-10.md"), Entry{ID: "a", Kind: KindComm, At: t0, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPinnedIn(dir, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if _, err := SetPinnedIn(filepath.Join(dir, "absent"), "a", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing folder: %v", err)
	}
}
