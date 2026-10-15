package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// SetPinnedIn pins or unpins entry id in whichever month file of dir holds it, whatever the file's name says about the
// entry's time (a block moved by hand, or a merge, can sit in another month's file). The files are tried oldest first, the
// order ReadUnit keeps a repeated id in, so the entry the reader showed is the one that changes. A file the reader would
// leave out (a symlink, too large, not a regular file) is not touched. It reports whether anything changed; ErrNotFound
// if no file holds the id.
func SetPinnedIn(dir, id string, pinned bool) (bool, error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return false, fmt.Errorf("journal: read %s: %w", dir, err)
	}
	var months []string
	for _, n := range names {
		if monthFileRE.MatchString(n.Name()) {
			months = append(months, n.Name())
		}
	}
	slices.Sort(months)
	for _, name := range months {
		path := filepath.Join(dir, name)
		text, reason := readMonth(path, maxMonthBytes, maxMonthBytes)
		if reason != "" {
			continue
		}
		if !slices.ContainsFunc(parseLines(name, splitLines(text)), func(e Entry) bool { return e.ID == id }) {
			continue
		}
		changed, err := SetPinned(path, id, pinned)
		if errors.Is(err, ErrNotFound) {
			continue // edited away between the two reads
		}
		return changed, err
	}
	return false, fmt.Errorf("%w: %s", ErrNotFound, id)
}
