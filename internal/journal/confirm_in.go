package journal

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// SetItemIn checks or unchecks item idx of confirm entry id in whichever month file of dir holds it, whatever the file's
// name says about the entry's time (a block moved by hand, or a merge, can sit in another month's file). The files are the
// ones ReadUnit keeps (keptMonths: the guard of each file and the unit's byte budget), tried oldest first, the order
// ReadUnit keeps a repeated id in, so the entry the reader showed is the one that changes; a file the reader leaves out is
// never touched. It reports whether anything changed; ErrNotFound if no kept file holds the id (or the folder is not
// there), ErrNotConfirm and ErrItemRange as SetItem.
func SetItemIn(dir, id string, idx int, done bool) (bool, error) {
	kept, _, err := keptMonths(dir)
	if err != nil {
		return false, err
	}
	for i := len(kept) - 1; i >= 0; i-- { // kept is newest first
		m := kept[i]
		if !slices.ContainsFunc(parseLines(m.name, splitLines(m.text)), func(e Entry) bool { return e.ID == id }) {
			continue
		}
		changed, err := SetItem(filepath.Join(dir, m.name), id, idx, done)
		if errors.Is(err, ErrNotFound) {
			continue // edited away between the two reads
		}
		return changed, err
	}
	return false, fmt.Errorf("%w: %s", ErrNotFound, id)
}
