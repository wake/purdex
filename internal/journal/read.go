package journal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The read side of a unit's journal (plan J-1e): every month file of the unit's folder as one list, sorted, and the windows
// of it the API answers with. The drainer writes the rows of different entries independently, so the order of the blocks in
// a file is not the order the entries happened in: nothing here relies on it.

const (
	// DefaultPageLimit and MaxPageLimit bound a page of entries.
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ErrBadCursor is returned for a page cursor that is not `<month>:<id>` or names an entry that is not there.
var ErrBadCursor = errors.New("journal: bad cursor")

var (
	monthFileRE = regexp.MustCompile(`^(\d{4})-(0[1-9]|1[0-2])\.md$`)
	monthRE     = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
)

// less is the order of entries: by time in milliseconds (the API's precision, and the cursor's), then by id.
func less(a, b Entry) int {
	if am, bm := a.At.UnixMilli(), b.At.UnixMilli(); am != bm {
		if am < bm {
			return -1
		}
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

// Limits on what ReadUnit takes in: the journal branch can come from a remote, so a month file is not trusted to be small or
// to be a file at all. Variables so a test can lower them.
var (
	maxMonthBytes int64 = 16 << 20 // one month file
	maxUnitBytes  int64 = 64 << 20 // a unit's month files together
)

// monthFile is what ReadUnit needs of an open month file; a variable's worth of seam so a test can put something that
// misbehaves in its place.
type monthFile interface {
	io.Reader
	io.Closer
	Stat() (os.FileInfo, error)
}

// openMonth opens one month file (see openMonthFile); replaced in tests.
var openMonth = openMonthFile

// Skipped is a month file ReadUnit left out of the unit, and why.
type Skipped struct{ Name, Reason string }

// ReadUnit reads the month files (`YYYY-MM.md`) of dir and returns the entries oldest first by (at, id), and the month files
// it left out. A folder that is not there is an empty unit; other files are left alone; an id that appears in more than one
// file is one entry (the older file's). Each month file is opened once and checked on that descriptor (a regular file; a
// symlink is not followed, a FIFO is not waited for), then read through a limit: at most maxMonthBytes for one file, and the
// bytes actually read count toward maxUnitBytes for the unit, newest months first, so a file that grows or is swapped after
// a check cannot take more than the limits. A month that is refused, over a limit or unreadable is skipped and reported,
// never a reason to hide the other months. Only a folder that cannot be listed is an error.
func ReadUnit(dir string) ([]Entry, []Skipped, error) {
	kept, skipped, err := keptMonths(dir)
	if err != nil {
		return nil, nil, err
	}
	var all []Entry
	seen := map[string]bool{}
	for i := len(kept) - 1; i >= 0; i-- { // oldest first, so a duplicate id keeps the older file's entry
		for _, e := range parseLines(kept[i].name, splitLines(kept[i].text)) {
			if !seen[e.ID] {
				seen[e.ID] = true
				all = append(all, e)
			}
		}
	}
	slices.SortStableFunc(all, less)
	return all, skipped, nil
}

// takenMonth is a month file ReadUnit keeps: its name and its text.
type takenMonth struct{ name, text string }

// keptMonths picks the month files of dir that make the unit and reads them: newest first, each through the guard of
// readMonth, the newest taking the unit's byte budget first. What it leaves out is reported. ReadUnit and SetItemIn both
// use it, so the file that changes is always one the reader shows. A folder that is not there is no files.
func keptMonths(dir string) (kept []takenMonth, skipped []Skipped, err error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("journal: read %s: %w", dir, err)
	}
	var months []string
	for _, n := range names {
		if monthFileRE.MatchString(n.Name()) {
			months = append(months, n.Name())
		}
	}
	slices.Sort(months)
	remaining := maxUnitBytes
	for i := len(months) - 1; i >= 0; i-- { // the newest months take the budget first
		text, reason := readMonth(filepath.Join(dir, months[i]), min(maxMonthBytes, remaining), maxMonthBytes)
		if reason != "" {
			skipped = append(skipped, Skipped{months[i], reason})
			continue
		}
		remaining -= int64(len(text))
		kept = append(kept, takenMonth{months[i], text})
	}
	return kept, skipped, nil
}

// readMonth reads one month file through one descriptor: at most limit bytes. A reason, instead of text, says why the file
// was left out; fileCap is the per-file limit, which tells "too large" from "over the unit's size budget".
func readMonth(path string, limit, fileCap int64) (text, reason string) {
	f, err := openMonth(path)
	if err != nil {
		if st, serr := os.Lstat(path); serr == nil && !st.Mode().IsRegular() {
			return "", "not a regular file" // a symlink (O_NOFOLLOW refused it) or another kind of file
		}
		return "", "unreadable: " + err.Error()
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", "unreadable: " + err.Error()
	}
	if !st.Mode().IsRegular() {
		return "", "not a regular file"
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", "unreadable: " + err.Error()
	}
	if int64(len(b)) > limit {
		if limit < fileCap {
			return "", "over the unit's size budget"
		}
		return "", "too large"
	}
	return string(b), ""
}

// CursorOf is the cursor that pages to what comes before e: `<month>:<id>` (the month of e's time, the id). The month is
// for the reader and only has to be a month; the daemon finds the entry by its id.
func CursorOf(e Entry) string {
	return e.At.Format("2006-01") + ":" + e.ID
}

// Page is a window of a unit's entries, oldest first.
type Page struct {
	Entries []Entry
	More    bool // there are older entries than the window
}

// PageBefore returns the newest `limit` entries of all (sorted oldest first and unique by id, as ReadUnit returns them) that
// sort strictly before the cursor's entry; with no cursor, the newest `limit` of all. The cursor names its entry by id, and
// the page is cut at that entry's place in the order: ids and times can run opposite ways (the drainer writes rows in
// whatever order they become ready), so cutting by id alone would lose or repeat entries. A cursor whose entry is not there
// is ErrBadCursor. A limit below 1 is DefaultPageLimit, one above MaxPageLimit is MaxPageLimit.
func PageBefore(all []Entry, cursor string, limit int) (Page, error) {
	if limit < 1 {
		limit = DefaultPageLimit
	}
	limit = min(limit, MaxPageLimit)
	end := len(all)
	if cursor != "" {
		id, err := parseCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		i := slices.IndexFunc(all, func(e Entry) bool { return e.ID == id })
		if i < 0 {
			return Page{}, ErrBadCursor
		}
		end = i
	}
	start := max(0, end-limit)
	return Page{Entries: slices.Clone(all[start:end]), More: start > 0}, nil
}

// parseCursor reads `<month>:<id>` into the id; the id may itself contain colons.
func parseCursor(c string) (string, error) {
	month, id, ok := strings.Cut(c, ":")
	if !ok || !monthRE.MatchString(month) || id == "" {
		return "", ErrBadCursor
	}
	return id, nil
}

// Confirms is every confirm entry of all with an unchecked item, newest first by (at, id).
func Confirms(all []Entry) []Entry {
	var out []Entry
	for _, e := range all {
		if e.Kind == KindConfirm && slices.ContainsFunc(e.Items, func(it Item) bool { return !it.Done }) {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return -less(a, b) })
	return out
}

// OpenConfirmItems is the number of unresolved items of the confirm entries of all.
func OpenConfirmItems(all []Entry) int {
	n := 0
	for _, e := range all {
		if e.Kind != KindConfirm {
			continue
		}
		for _, it := range e.Items {
			if !it.Done {
				n++
			}
		}
	}
	return n
}
