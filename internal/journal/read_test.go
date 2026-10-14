package journal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// unitDir writes the entries into month files of a fresh folder, in the order given (as a drainer that writes rows of
// different turns in whatever order they become ready would), and returns the folder.
func unitDir(t *testing.T, es ...Entry) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "unit")
	for _, e := range es {
		if err := Upsert(MonthFile(dir, e.At), e); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func ids(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func at(month time.Month, day, hour int) time.Time {
	return time.Date(2026, month, day, hour, 0, 0, 0, time.Local)
}

// A unit's entries come back oldest first by (at, id) across every month file, whatever order the files hold them in:
// the drainer writes the rows of different entries independently, so a block order is not the turn order.
// Mutation gate: keep the file order -> the late-written early entry stays last (red); sort by at only (stable) -> two
// entries of one instant keep their file order (red); read only the newest month -> the older one is missing (red).
func TestReadUnit_SortedByAtThenIDAcrossMonths(t *testing.T) {
	dir := unitDir(t,
		Entry{ID: "late", Kind: KindExec, At: at(time.November, 3, 10), Text: "written first, happened last"},
		Entry{ID: "early", Kind: KindExec, At: at(time.November, 3, 9), Text: "written second, happened first"},
		Entry{ID: "b", Kind: KindComm, At: at(time.November, 3, 11), Text: "tie, written first"},
		Entry{ID: "a", Kind: KindComm, At: at(time.November, 3, 11), Text: "tie, written second"},
		Entry{ID: "oct", Kind: KindExec, At: at(time.October, 30, 8), Text: "the month before"},
	)
	got, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"oct", "early", "late", "a", "b"}
	if !slices.Equal(ids(got), want) {
		t.Fatalf("entries = %v, want %v", ids(got), want)
	}
}

// The sort key is the time in milliseconds, the precision of the API and of the cursor: two entries inside one millisecond
// sort by id, not by their nanoseconds, or a page cut at one of them would not agree with the order the reader saw.
// Mutation gate: sort by the full time -> the later-written nanosecond comes last whatever its id (red).
func TestReadUnit_SortKeyIsMilliseconds(t *testing.T) {
	base := at(time.November, 4, 10)
	dir := unitDir(t,
		Entry{ID: "b", Kind: KindComm, At: base.Add(100 * time.Microsecond), Text: "earlier by nanoseconds, later by id"},
		Entry{ID: "a", Kind: KindComm, At: base.Add(900 * time.Microsecond), Text: "later by nanoseconds, earlier by id"},
	)
	got, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"a", "b"}) {
		t.Fatalf("entries = %v, want [a b]", ids(got))
	}
}

// A folder that is not there is an empty unit; files that are not month files are left alone; an id that sits in two month
// files is one entry.
func TestReadUnit_MissingFolderOtherFilesAndDuplicates(t *testing.T) {
	if got, err := ReadUnit(filepath.Join(t.TempDir(), "nope")); err != nil || len(got) != 0 {
		t.Fatalf("missing folder: %v, %v", got, err)
	}
	dir := unitDir(t,
		Entry{ID: "x", Kind: KindExec, At: at(time.October, 1, 1), Text: "in october"},
		Entry{ID: "y", Kind: KindExec, At: at(time.November, 1, 1), Text: "in november"},
	)
	// the same id again in another month's file (a hand copy): one entry
	if err := Upsert(MonthFile(dir, at(time.December, 1, 1)), Entry{ID: "x", Kind: KindExec, At: at(time.December, 1, 1), Text: "a copy"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# not a month\n<!-- j {\"id\":\"zz\",\"kind\":\"exec\",\"at\":\"2026-01-01T00:00:00Z\"} -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-13.md"), []byte("not a month either"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUnit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"x", "y"}) {
		t.Fatalf("entries = %v, want [x y]", ids(got))
	}
	if got[0].Text != "in october" {
		t.Errorf("the duplicate id kept %q, want the older file's entry", got[0].Text)
	}
}

func numbered(n int) []Entry {
	var es []Entry
	for i := 0; i < n; i++ {
		es = append(es, Entry{ID: "e" + string(rune('a'+i)), Kind: KindExec, At: at(time.November, 1, i), Text: "t"})
	}
	return es
}

// Without a cursor the newest `limit` entries come back (oldest first); with one, the `limit` entries just before it. `more`
// says whether anything older is left.
// Mutation gate: the page includes the cursor entry (red); the oldest instead of the newest are taken (red); `more` always
// false (red).
func TestPageBefore_WindowsAndMore(t *testing.T) {
	all := numbered(10) // ea … ej
	p, err := PageBefore(all, "", 4)
	if err != nil || !slices.Equal(ids(p.Entries), []string{"eg", "eh", "ei", "ej"}) || !p.More {
		t.Fatalf("newest page = %v more=%v err=%v", ids(p.Entries), p.More, err)
	}
	p, err = PageBefore(all, CursorOf(all[6]), 4) // before eg
	if err != nil || !slices.Equal(ids(p.Entries), []string{"ec", "ed", "ee", "ef"}) || !p.More {
		t.Fatalf("page before eg = %v more=%v err=%v", ids(p.Entries), p.More, err)
	}
	p, err = PageBefore(all, CursorOf(all[2]), 4) // before ec: two left
	if err != nil || !slices.Equal(ids(p.Entries), []string{"ea", "eb"}) || p.More {
		t.Fatalf("last page = %v more=%v err=%v", ids(p.Entries), p.More, err)
	}
	p, err = PageBefore(all, CursorOf(all[0]), 4) // before the oldest: nothing
	if err != nil || len(p.Entries) != 0 || p.More {
		t.Fatalf("before the oldest = %v more=%v err=%v", ids(p.Entries), p.More, err)
	}
	p, err = PageBefore(all, "", 10) // exactly everything: nothing more
	if err != nil || len(p.Entries) != 10 || p.More {
		t.Fatalf("a page of all = %d more=%v err=%v", len(p.Entries), p.More, err)
	}
	if p, err = PageBefore(nil, "", 5); err != nil || len(p.Entries) != 0 || p.More {
		t.Fatalf("empty unit = %v %v", p, err)
	}
}

// The cursor carries the whole sort key (at in milliseconds and id), and a page is what sorts strictly before it. The drainer
// writes entries in whatever order their rows become ready, so ids and times can run opposite ways: cutting by id alone
// would lose or repeat entries.
// Mutation gate: cut by id only -> the page before `y` is the wrong set (red); cut by at only -> a tie group loses entries
// (red); include the cursor's own key -> the entry repeats (red).
func TestPageBefore_IDsRunAgainstTimes(t *testing.T) {
	all := []Entry{ // sorted by (at, id): z, y, x, w - the ids descend while time ascends
		{ID: "z", Kind: KindExec, At: at(time.November, 1, 9), Text: "t"},
		{ID: "y", Kind: KindExec, At: at(time.November, 1, 10), Text: "t"},
		{ID: "x", Kind: KindExec, At: at(time.November, 1, 11), Text: "t"},
		{ID: "w", Kind: KindExec, At: at(time.November, 1, 12), Text: "t"},
	}
	p, err := PageBefore(all, CursorOf(all[1]), 5) // before y
	if err != nil || !slices.Equal(ids(p.Entries), []string{"z"}) || p.More {
		t.Fatalf("before y = %v more=%v err=%v, want [z]", ids(p.Entries), p.More, err)
	}
	p, err = PageBefore(all, CursorOf(all[3]), 2) // before w: the two newest of the three older
	if err != nil || !slices.Equal(ids(p.Entries), []string{"y", "x"}) || !p.More {
		t.Fatalf("before w = %v more=%v err=%v, want [y x] and more", ids(p.Entries), p.More, err)
	}
}

// The cursor names a sort key, not an entry that has to be there: one whose entry is gone still cuts the page.
func TestPageBefore_CursorOfAnEntryThatIsGone(t *testing.T) {
	all := numbered(5) // ea … ee, one hour apart
	gone := Entry{ID: "gone", Kind: KindExec, At: all[2].At.Add(30 * time.Minute)}
	p, err := PageBefore(all, CursorOf(gone), 10)
	if err != nil || !slices.Equal(ids(p.Entries), []string{"ea", "eb", "ec"}) {
		t.Fatalf("page before a vanished entry = %v, %v", ids(p.Entries), err)
	}
}

// Entries of one instant are before or after the cursor by id, so a page boundary in a tie group loses and repeats nothing.
func TestPageBefore_CursorInsideATieGroup(t *testing.T) {
	same := at(time.November, 5, 12)
	all := []Entry{
		{ID: "a", Kind: KindComm, At: same, Text: "t"},
		{ID: "b", Kind: KindComm, At: same, Text: "t"},
		{ID: "c", Kind: KindComm, At: same, Text: "t"},
		{ID: "d", Kind: KindComm, At: same, Text: "t"},
	}
	newest, _ := PageBefore(all, "", 2)
	older, err := PageBefore(all, CursorOf(newest.Entries[0]), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := append(ids(older.Entries), ids(newest.Entries)...); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("two pages = %v", got)
	}
}

// A cursor that is not `<month>:<id>`, names a month that is not one, or an entry that is not there is refused.
func TestPageBefore_BadCursors(t *testing.T) {
	all := numbered(3)
	for _, c := range []string{"nocolon", ":1:ea", "2026-11:", "2026-11:123", "2026-11:123:", "2026-11::ea", "2026-13:123:ea", "november:123:ea", "2026-11:abc:ea", "2026-11:-5:ea", "2026-11-01:123:ea", "2026-11:12.5:ea"} {
		if _, err := PageBefore(all, c, 2); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v, want ErrBadCursor", c, err)
		}
	}
	if c := CursorOf(all[1]); !strings.HasPrefix(c, "2026-11:") || !strings.HasSuffix(c, ":eb") || strings.Count(c, ":") != 2 {
		t.Errorf("CursorOf = %q", c)
	}
	// an id may contain colons (task:<team>:<seq>:new): the month and the time are what precede the first two
	e := Entry{ID: "task:7bcf08:3:done", Kind: KindTaskDone, At: at(time.November, 2, 2), Text: "#3 t"}
	p, err := PageBefore(append(all, e), CursorOf(e), 5)
	if err != nil || len(p.Entries) != 3 {
		t.Fatalf("cursor of an id with colons: %v, %v", p, err)
	}
}

// A limit below 1 is the default page; one above the cap is the cap.
func TestPageBefore_LimitBounds(t *testing.T) {
	all := numbered(5)
	for _, limit := range []int{0, -3} {
		if p, err := PageBefore(all, "", limit); err != nil || len(p.Entries) != 5 {
			t.Errorf("limit %d: %d entries, %v", limit, len(p.Entries), err)
		}
	}
	big := make([]Entry, 0, MaxPageLimit+5)
	for i := 0; i < MaxPageLimit+5; i++ {
		big = append(big, Entry{ID: "n" + strings.Repeat("0", 4-len(itoa(i))) + itoa(i), Kind: KindExec, At: at(time.November, 1, 0).Add(time.Duration(i) * time.Second), Text: "t"})
	}
	if p, err := PageBefore(big, "", MaxPageLimit*10); err != nil || len(p.Entries) != MaxPageLimit || !p.More {
		t.Errorf("a limit past the cap gave %d entries (more %v), %v", len(p.Entries), p.More, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// The pinned band is every pinned entry of the unit, newest first by (at, id) descending.
// Mutation gate: oldest first (red); only entries of the last page (red); an unpinned entry included (red).
func TestPinned_NewestFirst(t *testing.T) {
	all := []Entry{
		{ID: "a", Kind: KindConfirm, At: at(time.October, 1, 1), Pinned: true, Items: []Item{{Text: "q"}}},
		{ID: "b", Kind: KindExec, At: at(time.October, 2, 1), Text: "not pinned"},
		{ID: "c", Kind: KindConfirm, At: at(time.November, 1, 1), Pinned: true, Items: []Item{{Text: "q"}}},
		{ID: "d", Kind: KindComm, At: at(time.November, 1, 1), Pinned: true, Text: "tie with c"},
	}
	got := Pinned(all)
	if !slices.Equal(ids(got), []string{"d", "c", "a"}) {
		t.Fatalf("pinned = %v, want [d c a]", ids(got))
	}
	if got := Pinned(nil); len(got) != 0 {
		t.Fatalf("pinned of nothing = %v", got)
	}
}

// open_confirms is the number of unresolved items of confirm entries: an item that is ticked, and any item of another kind,
// does not count.
// Mutation gate: count the ticked ones too (red); count every kind's items (red); count entries, not items (red).
func TestOpenConfirmItems(t *testing.T) {
	all := []Entry{
		{ID: "c1", Kind: KindConfirm, At: at(time.October, 1, 1), Items: []Item{{Text: "a"}, {Text: "b", Done: true}, {Text: "c"}}},
		{ID: "c2", Kind: KindConfirm, At: at(time.November, 1, 1), Items: []Item{{Text: "d", Done: true}}},
		{ID: "x", Kind: KindExec, At: at(time.November, 2, 1), Items: []Item{{Text: "not a question"}}},
		{ID: "c3", Kind: KindConfirm, At: at(time.November, 3, 1), Items: []Item{{Text: "e"}}},
	}
	if got := OpenConfirmItems(all); got != 3 {
		t.Fatalf("open confirm items = %d, want 3", got)
	}
	if got := OpenConfirmItems(nil); got != 0 {
		t.Fatalf("of nothing = %d", got)
	}
}
