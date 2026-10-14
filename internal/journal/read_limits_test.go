package journal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func withReadLimits(t *testing.T, month, unit int64) {
	t.Helper()
	oldMonth, oldUnit := maxMonthBytes, maxUnitBytes
	maxMonthBytes, maxUnitBytes = month, unit
	t.Cleanup(func() { maxMonthBytes, maxUnitBytes = oldMonth, oldUnit })
}

func skippedNames(skipped []Skipped) []string {
	var out []string
	for _, s := range skipped {
		out = append(out, s.Name)
	}
	slices.Sort(out)
	return out
}

// A month file that is a symlink (it could point at a device or at a file anywhere) or a directory is not read; the other
// months are, and the skip is reported to the caller.
// Mutation gate: follow symlinks -> the linked entry is read (red); do not report -> the skip is invisible (red).
func TestReadUnit_SkipsWhatIsNotARegularFile(t *testing.T) {
	dir := unitDir(t, Entry{ID: "ok", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"})
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := Upsert(target, Entry{ID: "linked", Kind: KindExec, At: at(time.October, 1, 1), Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "2026-10.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "2026-09.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"ok"}) {
		t.Fatalf("entries = %v, want [ok]", ids(got))
	}
	if !slices.Equal(skippedNames(skipped), []string{"2026-09.md", "2026-10.md"}) {
		t.Fatalf("skipped = %+v", skipped)
	}
	for _, s := range skipped {
		if s.Reason != "not a regular file" {
			t.Errorf("skip of %s: reason %q, want \"not a regular file\"", s.Name, s.Reason)
		}
	}
}

// A month file past the size cap is skipped whole; the months that fit are still read.
// Mutation gate: no per-file cap -> the big file is read (red).
func TestReadUnit_SkipsAnOversizedMonth(t *testing.T) {
	withReadLimits(t, 1500, 1<<30)
	dir := unitDir(t,
		Entry{ID: "small", Kind: KindExec, At: at(time.October, 1, 1), Text: "t"},
		Entry{ID: "big", Kind: KindExec, At: at(time.November, 1, 1), Text: "t", Detail: strings.Repeat("x", 3000)},
	)
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"small"}) || !slices.Equal(skippedNames(skipped), []string{"2026-11.md"}) || skipped[0].Reason != "too large" {
		t.Fatalf("entries = %v, skipped = %+v", ids(got), skipped)
	}
}

// A unit is read up to a total size, newest months first: when the months together are too much, the oldest are left out.
// Mutation gate: no unit budget -> every month is read (red); oldest first -> the newest months are the ones dropped (red).
func TestReadUnit_UnitBudgetKeepsTheNewestMonths(t *testing.T) {
	dir := unitDir(t,
		Entry{ID: "sep", Kind: KindExec, At: at(time.September, 1, 1), Text: "t"},
		Entry{ID: "oct", Kind: KindExec, At: at(time.October, 1, 1), Text: "t"},
		Entry{ID: "nov", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"},
	)
	size := func(name string) int64 {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return st.Size()
	}
	withReadLimits(t, 1<<30, size("2026-10.md")+size("2026-11.md")+1) // room for two months, not three
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"oct", "nov"}) || !slices.Equal(skippedNames(skipped), []string{"2026-09.md"}) || skipped[0].Reason != "over the unit's size budget" {
		t.Fatalf("entries = %v, skipped = %+v", ids(got), skipped)
	}
}

// A month file that cannot be read is skipped and reported, not a reason to hide the rest of the unit.
// Mutation gate: a read error fails the whole unit (red).
func TestReadUnit_AnUnreadableMonthDoesNotHideTheOthers(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := unitDir(t,
		Entry{ID: "readable", Kind: KindExec, At: at(time.November, 1, 1), Text: "t"},
		Entry{ID: "locked", Kind: KindExec, At: at(time.October, 1, 1), Text: "t"},
	)
	locked := filepath.Join(dir, "2026-10.md")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })
	got, skipped, err := ReadUnit(dir)
	if err != nil {
		t.Fatalf("an unreadable month failed the unit: %v", err)
	}
	if !slices.Equal(ids(got), []string{"readable"}) || !slices.Equal(skippedNames(skipped), []string{"2026-10.md"}) || !strings.HasPrefix(skipped[0].Reason, "unreadable: ") {
		t.Fatalf("entries = %v, skipped = %+v", ids(got), skipped)
	}
}
