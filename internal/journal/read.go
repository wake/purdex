package journal

import (
	"errors"
	"fmt"
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

// ReadUnit reads every month file (`YYYY-MM.md`) of dir and returns the entries oldest first by (at, id). A folder that is
// not there is an empty unit; other files are left alone; an id that appears in more than one file is one entry (the
// older file's). A file that cannot be read is an error, a comment that does not parse is skipped by Parse.
func ReadUnit(dir string) ([]Entry, error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journal: read %s: %w", dir, err)
	}
	var files []string
	for _, n := range names {
		if !n.IsDir() && monthFileRE.MatchString(n.Name()) {
			files = append(files, n.Name())
		}
	}
	slices.Sort(files) // oldest month first, so a duplicate id keeps the older file's entry
	var all []Entry
	seen := map[string]bool{}
	for _, f := range files {
		es, err := Parse(filepath.Join(dir, f))
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if !seen[e.ID] {
				seen[e.ID] = true
				all = append(all, e)
			}
		}
	}
	slices.SortStableFunc(all, less)
	return all, nil
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

// Pinned is every pinned entry of all, newest first by (at, id).
func Pinned(all []Entry) []Entry {
	var out []Entry
	for _, e := range all {
		if e.Pinned {
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
