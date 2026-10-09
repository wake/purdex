package convfeed

import (
	"encoding/json"

	"github.com/wake/purdex/internal/convmodel"
)

// CopyLastTurns returns the newest n turns (oldest first) as a deep copy made under the entry's lock: nothing in it is
// shared with the entry, so a caller outside the package may read and change it while the entry is refreshed. A turn
// that cannot be copied is left out (the model's own JSON always round-trips; this only guards a damaged item).
// Offsets, which never go on the wire, are not kept.
func (e *Entry) CopyLastTurns(n int) []convmodel.Turn {
	e.mu.Lock()
	defer e.mu.Unlock()
	all := e.conv().Turns
	if n < 1 || len(all) == 0 {
		return nil
	}
	start := len(all) - n
	if start < 0 {
		start = 0
	}
	out := make([]convmodel.Turn, 0, len(all)-start)
	for _, t := range all[start:] {
		b, err := json.Marshal(t)
		if err != nil {
			continue
		}
		var c convmodel.Turn
		if err := json.Unmarshal(b, &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}
