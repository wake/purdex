package workbook

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// symlinkAt makes rel (inside wt) a symlink to target, with real folders above it.
func symlinkAt(t *testing.T, wt, rel, target string) {
	t.Helper()
	p := filepath.Join(wt, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// A folder of the unit's home that is a symlink (or not a directory) is refused wherever it sits on the way to the unit's
// files; real folders, folders that are not there yet, and a symlinked FILE at the very end (guarded where it is opened)
// pass. Mutation gate: skip the .purdex check / the journal check / the unit folder check / the tasks check / the leaf of
// the journal folder → that case passes (red).
func TestHomeVerify_RefusesASymlinkedFolderOnTheWay(t *testing.T) {
	u := UnitRow{Key: "team:t1", Label: "日誌"}
	slug := unitSlug(u.Label, u.Key)
	for _, rel := range []string{".purdex", filepath.Join(".purdex", "journal"), filepath.Join(".purdex", "journal", slug), filepath.Join(".purdex", "tasks")} {
		wt, outside := t.TempDir(), t.TempDir()
		u.WtPath = wt
		symlinkAt(t, wt, rel, outside)
		if err := unitHome(u).verify(); !errors.Is(err, errUnsafeHome) {
			t.Errorf("%s is a symlink: %v", rel, err)
		}
	}
	// a plain file where a folder must be
	wt := t.TempDir()
	u.WtPath = wt
	if err := os.WriteFile(filepath.Join(wt, ".purdex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unitHome(u).verify(); !errors.Is(err, errUnsafeHome) {
		t.Errorf(".purdex is a file: %v", err)
	}
	// nothing there yet, real folders, and a roadmap that is itself a symlink (its own guard refuses it when it is read)
	wt = t.TempDir()
	u.WtPath = wt
	if err := unitHome(u).verify(); err != nil {
		t.Errorf("an empty worktree: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(wt, ".purdex", "journal", slug), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "r.md"), filepath.Join(wt, ".purdex", "roadmap.md")); err != nil {
		t.Fatal(err)
	}
	if err := unitHome(u).verify(); err != nil {
		t.Errorf("real folders: %v", err)
	}
}

// A unit whose worktree path is empty or relative would have its home resolved against the daemon's working directory; both
// are refused, through unitHome as the API and the drainer use it.
// Mutation gate: drop the IsAbs check → the empty and the relative case pass (red).
func TestHomeVerify_RefusesAnEmptyOrRelativeWorktree(t *testing.T) {
	for _, wt := range []string{"", "relative/wt", "."} {
		if err := unitHome(UnitRow{Key: "team:t1", Label: "日誌", WtPath: wt}).verify(); !errors.Is(err, errUnsafeHome) {
			t.Errorf("WtPath %q: %v", wt, err)
		}
	}
}

// A target that is not under the worktree (a slug that climbs out) is refused, not walked.
// Mutation gate: accept a path outside the root → that case passes (red).
func TestHomeVerify_RefusesAPathOutsideTheWorktree(t *testing.T) {
	wt := t.TempDir()
	for _, target := range []string{filepath.Dir(wt), filepath.Join(wt, "..", "x", "y"), wt, filepath.Join(t.TempDir(), "other")} {
		if err := checkChain(wt, target, true); !errors.Is(err, errUnsafeHome) {
			t.Errorf("%s: %v", target, err)
		}
	}
	if err := checkChain(wt, filepath.Join(wt, ".purdex", "journal", "u"), true); err != nil {
		t.Errorf("a path under the worktree: %v", err)
	}
}

// With .purdex a symlink to a folder that holds a whole journal, none of the four routes reads it and the pin does not write
// it. Mutation gate: drop the verify call in resolveJournal → the routes answer 200 with the outside entry (red).
func TestJournalAPI_AFolderThatIsASymlinkIsNotFollowed(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("team:t1", "日誌")
	j.seats.seats["m1"] = teamSeat("t1")
	slug := unitSlug(u.Label, u.Key)
	outside := t.TempDir()
	month := filepath.Join(outside, "journal", slug, "2026-10.md")
	if err := journal.Upsert(month, journal.Entry{ID: "secret", Kind: journal.KindExec, At: tm(time.October, 5, 1), Actor: "A", Text: "outside the worktree"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "roadmap.md"), []byte("## F\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S-1 | d | 未開始 |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(month)
	if err := os.Symlink(outside, filepath.Join(j.wt, ".purdex")); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/api/journal?session_id=m1", "/api/journal/tasks?session_id=m1", "/api/journal/roadmap?session_id=m1"} {
		if code, body := j.request(http.MethodGet, url, nil); code != http.StatusInternalServerError {
			t.Errorf("GET %s: code %d: %s", url, code, body)
		}
	}
	if code, body := j.request(http.MethodPut, "/api/journal/confirm", map[string]any{"session_id": "m1", "entry_id": "secret", "idx": 0, "done": true}); code != http.StatusInternalServerError {
		t.Errorf("confirm: code %d: %s", code, body)
	}
	if after, _ := os.ReadFile(month); string(after) != string(before) {
		t.Fatal("a file outside the worktree was written")
	}
	if evs := j.changed(); len(evs) != 0 {
		t.Errorf("a refused confirm announced %+v", evs)
	}
}

// A block that sits in a month file whose name is not its month is still shown, and checking it works.
// Mutation gate: derive the file from the entry's time again → 404 (red).
func TestJournalAPI_AnEntryInTheWrongMonthFileCanBeChecked(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("team:t1", "日誌")
	j.seats.seats["m1"] = teamSeat("t1")
	if err := journal.Upsert(filepath.Join(unitHome(u).journalDir, "2026-09.md"),
		journal.Entry{ID: "moved", Kind: journal.KindConfirm, At: tm(time.November, 5, 1), Actor: "A", Text: "filed under September", Items: []journal.Item{{Text: "q"}}}); err != nil {
		t.Fatal(err)
	}
	if code, body := j.request(http.MethodPut, "/api/journal/confirm", map[string]any{"session_id": "m1", "entry_id": "moved", "idx": 0, "done": true}); code != http.StatusNoContent {
		t.Fatalf("confirm: code %d: %s", code, body)
	}
	_, p, _ := j.page("/api/journal?session_id=m1")
	if len(p.Confirms) != 0 || p.OpenConfirms != 0 {
		t.Errorf("confirms = %v, open_confirms = %d, want none", ids(p.Confirms), p.OpenConfirms)
	}
	if evs := j.changed(); len(evs) != 1 || evs[0].What != "entries" {
		t.Errorf("events = %+v", evs)
	}
}

// The drainer does not write through a folder that is a symlink: a turn row and a task row both stay undone (retried and
// logged), and nothing appears in the folder the link points at.
// Mutation gate: drop the verify call in writeRow / in writeTaskRow → the folder outside gets files (red).
func TestDrain_DoesNotWriteThroughASymlinkedFolder(t *testing.T) {
	k := newDrainKit(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(k.fp.wt, ".purdex")); err != nil {
		t.Fatal(err)
	}
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "紀錄。", false)
	if err := k.st.RecordJournalResult(journalOutcomeEvents(id)); err != nil {
		t.Fatal(err)
	}
	if err := k.st.ApplyTaskPage(TaskPage{Rows: []TaskOutboxRow{{UnitKey: "team:t1", Payload: map[string]any{"team": "t1", "lead": "s", "label": "專案 A"}}}}); err != nil {
		t.Fatal(err)
	}
	if n := k.drain(); n != 0 {
		t.Fatalf("drained %d rows through a symlinked folder", n)
	}
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Fatalf("the folder behind the link got %d entries", len(es))
	}
	if len(k.logs) == 0 {
		t.Fatal("a refused write was not logged")
	}
}
