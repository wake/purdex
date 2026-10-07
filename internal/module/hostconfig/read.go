package hostconfig

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// The lenient view (#1889). A PUT stores only what its normalize* accepts,
// but a row edited by hand is read back as it is. The GET, a PUT's answer and
// a 409's current therefore read each stored value with the same rules,
// row by row: a row that fails is left out and counted, so what the SPA
// shows (and sends back whole on its next PUT) always passes that PUT. A
// value that is not even the collection's container is invalid and answers
// the empty value. The rules themselves live in the check* functions shared
// with the strict normalize*.

// maxDroppedReasons and droppedReasonMaxBytes bound the reasons an answer
// carries: the count is exact, the reasons are a sample to find the rows by.
const (
	maxDroppedReasons     = 10
	droppedReasonMaxBytes = 200
)

// Dropped is an answer's `dropped` marker: how many rows (resume templates:
// agents; relay: prompt bodies) were left out, and why, for the first
// maxDroppedReasons of them.
type Dropped struct {
	Count   int      `json:"count"`
	Reasons []string `json:"reasons"`
}

// readout is one stored value as the lenient view reads it.
type readout struct {
	items   any      // what a PUT of the kept rows stores; the empty value when invalid
	invalid error    // why the value is not this collection at all
	dropped *Dropped // nil when nothing was left out
}

// drop counts one row left out, keeping its reason while there is room.
func (r *readout) drop(why string) {
	if r.dropped == nil {
		r.dropped = &Dropped{}
	}
	r.dropped.Count++
	if len(r.dropped.Reasons) < maxDroppedReasons {
		r.dropped.Reasons = append(r.dropped.Reasons, clipReason(why))
	}
}

// clipReason cuts why to droppedReasonMaxBytes on a rune boundary, marking
// the cut with "…": a reason quotes the offending value, which can be long.
func clipReason(why string) string {
	if len(why) <= droppedReasonMaxBytes {
		return why
	}
	cut := droppedReasonMaxBytes - len("…")
	for cut > 0 && !utf8.RuneStart(why[cut]) {
		cut--
	}
	return why[:cut] + "…"
}

// readRows is the lenient twin of a normalize* list: raw must be a JSON
// array, else the collection is invalid. Each element is decoded on its own
// (a non-object, or a field of the wrong type, is "not a <one>") and handed
// to check, which applies the PUT's rules in their order and answers the row
// as the PUT stores it; a row that fails is dropped as "item N: <why>", N
// its index in the stored array from 0. Once limit rows are kept, every
// later one is dropped as over the cap.
func readRows[T any](raw json.RawMessage, one, many string, limit int, check func(T) (T, error)) readout {
	var elems []json.RawMessage
	if err := decodeArray(raw, &elems); err != nil {
		return readout{items: []T{}, invalid: err}
	}
	var r readout
	kept := make([]T, 0, min(len(elems), limit))
	for i, elem := range elems {
		if len(kept) == limit {
			r.drop(fmt.Sprintf("item %d: at most %d %s", i, limit, many))
			continue
		}
		var row T
		if firstByte(elem) != '{' || json.Unmarshal(elem, &row) != nil {
			r.drop(fmt.Sprintf("item %d: not a %s", i, one))
			continue
		}
		row, err := check(row)
		if err != nil {
			r.drop(fmt.Sprintf("item %d: %v", i, err))
			continue
		}
		kept = append(kept, row)
	}
	r.items = kept
	return r
}

// readers is the lenient view of each key.
var readers = map[string]func(json.RawMessage) readout{
	KeyProjects:        readProjects,
	KeyCommands:        readCommands,
	KeyResumeTemplates: readResumeTemplates,
	KeyQuickReplies:    readQuickReplies,
	KeyRelay:           readRelay,
	KeyTeam:            readTeam,
}
