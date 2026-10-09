package convfeed

import (
	"encoding/json"
	"sort"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

// WindowResult is a window of a conversation's turns.
type WindowResult struct {
	Turns         []convmodel.Turn
	FirstIndex    int // Turn.Index of the first turn in the window; -1 when there is none
	LastIndex     int // likewise the last
	TotalTurns    int // turns the conversation has
	HasMoreBefore bool
	// OverBudget: not even the newest turn with every item dropped fits the budget, so no turn is returned (the
	// caller answers its own error); the window never carries a body known to exceed the cap.
	OverBudget bool
}

// Window is the last `turns` turns whose Index is below `before` (before < 0:
// the newest). The caller reports, through budget, whether the JSON encoding
// of the turn array still fits its size cap (the envelope around it is the
// caller's to measure): turns are dropped from the old end while it does not,
// down to one; if that turn alone does not fit, its oldest items are dropped
// and the turn carries OmittedItems.
func (e *Entry) Window(turns, before int, budget func([]byte) bool) WindowResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.windowLocked(turns, before, budget, nil)
}

// View is a window, the header and the cursor taken under one hold of the entry's lock: a refresh by another request
// cannot slip between them, so the cursor never claims changes the window does not show.
type View struct {
	WindowResult
	Header Header
	Cursor string
}

// View is Window plus the header and cursor of the same instant. envelope is given that header and cursor and returns
// the budget for the turn array (it knows what else the encoded body carries, which depends on the title); it runs
// under the entry's lock, so it must be quick.
//
// enc encodes one turn the way the caller will put it on the wire (so the budget measures what is really sent, e.g. with
// the API's per-item index); nil is the model's own JSON.
func (e *Entry) View(turns, before int, envelope func(h Header, cursor string) func([]byte) bool, enc TurnEncoder) View {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, cur := e.headerLocked(), e.cursorLocked()
	return View{WindowResult: e.windowLocked(turns, before, envelope(h, cur), enc), Header: h, Cursor: cur}
}

// ViewAround is View centred on the turn that holds the item itemID: the `turns` turns around it, as close to the middle
// as the ends allow. ok is false when no turn holds the item. When the size budget drops the older turns and the target
// is among them, the window is taken again with the target as its newest turn. shown is false when the item itself is
// not in the window: its turn alone is over the cap and the oldest items that were dropped include it (the caller says
// so instead of answering with a window that silently lacks what was asked for).
func (e *Entry) ViewAround(turns int, itemID string, envelope func(h Header, cursor string) func([]byte) bool, enc TurnEncoder) (v View, ok, shown bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	all := e.conv().Turns
	pos := -1
find:
	for i, t := range all {
		for _, it := range t.Items {
			if ccnorm.ItemID(it) == itemID {
				pos = i
				break find
			}
		}
	}
	if pos < 0 {
		return View{}, false, false
	}
	start := pos - (turns-1)/2
	if start+turns > len(all) {
		start = len(all) - turns
	}
	if start < 0 {
		start = 0
	}
	end := start + turns
	if end > len(all) {
		end = len(all)
	}
	h, cur := e.headerLocked(), e.cursorLocked()
	budget := envelope(h, cur)
	w := e.windowLocked(turns, all[end-1].Index+1, budget, enc)
	if !w.OverBudget && (w.FirstIndex < 0 || all[pos].Index < w.FirstIndex) {
		w = e.windowLocked(turns, all[pos].Index+1, budget, enc)
	}
	v = View{WindowResult: w, Header: h, Cursor: cur}
	target := all[pos].Index
	for _, t := range w.Turns {
		if t.Index != target {
			continue
		}
		for _, it := range t.Items {
			if ccnorm.ItemID(it) == itemID {
				shown = true
			}
		}
	}
	return v, true, shown
}

// TurnEncoder encodes one turn for the size budget.
type TurnEncoder func(convmodel.Turn) ([]byte, error)

func encodeOrModel(enc TurnEncoder) TurnEncoder {
	if enc != nil {
		return enc
	}
	return func(t convmodel.Turn) ([]byte, error) { return json.Marshal(t) }
}

func (e *Entry) windowLocked(turns, before int, budget func([]byte) bool, enc TurnEncoder) WindowResult {
	enc = encodeOrModel(enc)
	all := e.conv().Turns
	res := WindowResult{FirstIndex: -1, LastIndex: -1, TotalTurns: len(all)}
	if turns < 1 || len(all) == 0 {
		return res
	}
	end := len(all)
	if before >= 0 {
		end = sort.Search(len(all), func(i int) bool { return all[i].Index >= before })
	}
	if end == 0 {
		return res
	}
	start := end - turns
	if start < 0 {
		start = 0
	}
	cand := all[start:end]

	encoded := make([][]byte, len(cand))
	for i, t := range cand {
		b, err := enc(t)
		if err != nil {
			b = []byte("null")
		}
		encoded[i] = b
	}
	fits := func(k int) bool { // the newest k turns
		return budget(joinArray(encoded[len(encoded)-k:]))
	}
	k := len(cand)
	if !fits(k) {
		// the largest k in [1, len) that fits; 1 when none does
		lo, hi := 1, len(cand)-1 // fits(lo) unknown, fits(hi+1) false
		best := 0
		for lo <= hi {
			mid := (lo + hi) / 2
			if fits(mid) {
				best, lo = mid, mid+1
			} else {
				hi = mid - 1
			}
		}
		k = best
		if k == 0 {
			k = 1
		}
	}
	chosen := append([]convmodel.Turn(nil), cand[len(cand)-k:]...)
	if k == 1 && !fits(1) {
		t, ok := dropOldestItems(chosen[0], budget, enc)
		if !ok {
			res.OverBudget = true
			res.HasMoreBefore = true
			return res
		}
		chosen[0] = t
	}
	res.Turns = chosen
	res.FirstIndex = chosen[0].Index
	res.LastIndex = chosen[len(chosen)-1].Index
	res.HasMoreBefore = start+(len(cand)-k) > 0
	return res
}

// dropOldestItems keeps the fewest-dropped suffix of the turn's items that
// fits (the smallest d such that dropping d oldest items fits); d = all items
// when none does (then ok is false: the turn cannot fit at all).
func dropOldestItems(t convmodel.Turn, budget func([]byte) bool, enc TurnEncoder) (convmodel.Turn, bool) {
	n := len(t.Items)
	try := func(d int) (convmodel.Turn, bool) {
		c := t
		c.Items = t.Items[d:]
		c.OmittedItems = d
		b, err := enc(c)
		if err != nil {
			return c, false
		}
		return c, budget(joinArray([][]byte{b}))
	}
	lo, hi := 1, n // the smallest d in [1, n] that fits
	best := n
	for lo <= hi {
		mid := (lo + hi) / 2
		if _, ok := try(mid); ok {
			best, hi = mid, mid-1
		} else {
			lo = mid + 1
		}
	}
	return try(best)
}

func joinArray(parts [][]byte) []byte {
	n := 2
	for _, p := range parts {
		n += len(p) + 1
	}
	out := make([]byte, 0, n)
	out = append(out, '[')
	for i, p := range parts {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, p...)
	}
	return append(out, ']')
}
