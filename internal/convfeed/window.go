package convfeed

import (
	"encoding/json"
	"sort"

	"github.com/wake/purdex/internal/convmodel"
)

// WindowResult is a window of a conversation's turns.
type WindowResult struct {
	Turns         []convmodel.Turn
	FirstIndex    int // Turn.Index of the first turn in the window; -1 when there is none
	LastIndex     int // likewise the last
	TotalTurns    int // turns the conversation has
	HasMoreBefore bool
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

	enc := make([][]byte, len(cand))
	for i, t := range cand {
		b, err := json.Marshal(t)
		if err != nil {
			b = []byte("null")
		}
		enc[i] = b
	}
	fits := func(k int) bool { // the newest k turns
		return budget(joinArray(enc[len(enc)-k:]))
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
		chosen[0] = dropOldestItems(chosen[0], budget)
	}
	res.Turns = chosen
	res.FirstIndex = chosen[0].Index
	res.LastIndex = chosen[len(chosen)-1].Index
	res.HasMoreBefore = start+(len(cand)-k) > 0
	return res
}

// dropOldestItems keeps the fewest-dropped suffix of the turn's items that
// fits (the smallest d such that dropping d oldest items fits); d = all items
// when none does.
func dropOldestItems(t convmodel.Turn, budget func([]byte) bool) convmodel.Turn {
	n := len(t.Items)
	try := func(d int) (convmodel.Turn, bool) {
		c := t
		c.Items = t.Items[d:]
		c.OmittedItems = d
		b, err := json.Marshal(c)
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
	c, _ := try(best)
	return c
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
