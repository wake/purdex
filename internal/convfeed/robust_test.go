package convfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/wake/purdex/internal/convmodel"
	"strings"
	"testing"
)

// The last fed line was an oversize one (skipped, never read): the fingerprint still covers the bytes before it, so
// a same-inode rewrite of the same size is a new epoch, not a model that goes on from a stale offset.
func TestEntry_RewriteAfterAnOversizeLastLineStartsANewEpoch(t *testing.T) {
	big := append([]byte(`{"type":"user","blob":"`), bytes.Repeat([]byte{'A'}, 9<<20)...)
	big = append(big, '"', '}')
	first := userRow("u0", 0, "original text")
	m := &memFile{}
	m.Append(first, big)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	old := e.Epoch()

	same := userRow("u1", 0, "original text") // as long as the first line; the uuid sits in its last 64 bytes
	if len(same) != len(first) {
		t.Fatalf("setup: %d vs %d", len(same), len(first))
	}
	rewritten := &memFile{}
	rewritten.Append(same, big)
	m.Set(rewritten.bytes())
	r := refresh(t, e, src(m, "f1", false))
	if !r.Reset || e.Epoch() == old {
		t.Fatalf("a same-size rewrite after an oversize last line was not noticed: %+v", r)
	}
}

// ReadAt answers short (the file shrank after its size was taken): the refresh fails and uses nothing of that chunk;
// the lines fed before stay whole, and the next refresh sees the new file.
func TestEntry_ShortReadIsAnErrorNotAPartialSnapshot(t *testing.T) {
	pad := strings.Repeat("x", 100_000)
	var lines [][]byte
	for i := 0; i < 70; i++ {
		lines = append(lines, userRow("u"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i), pad))
	}
	m := newMem(lines...)
	e := NewEntry(sidA)
	m.onRead = func(n int) {
		if n == 3 { // the file is cut after two chunks were read
			m.Set(joinLines(lines[:5]))
		}
	}
	_, err := e.Refresh(context.Background(), src(m, "f1", false))
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("err = %v, want ErrFileChanged", err)
	}
	fed := turnsOf(e)
	if fed == 0 || fed >= 70 {
		t.Fatalf("turns after the failed refresh = %d: the whole lines before the cut must stay, nothing past it", fed)
	}
	m.onRead = nil
	r := refresh(t, e, src(m, "f1", false)) // the file is now 5 lines: smaller than what was fed
	if !r.Reset || turnsOf(e) != 5 {
		t.Fatalf("after the cut: reset %v turns %d, want a new epoch with 5 turns", r.Reset, turnsOf(e))
	}
}

// A line past one chunk is read back whole once its newline is found; if the file shrank by then, nothing is fed.
func TestEntry_LongLineShortSecondReadFeedsNothing(t *testing.T) {
	long := userRow("uL", 1, strings.Repeat("y", 6<<20)) // between 2 MiB and 8 MiB: read back whole after its newline is found
	m := newMem(userRow("u0", 0, "first"), long)
	e := NewEntry(sidA)
	m.onRead = func(n int) {
		if n == 5 { // reads 1-4 scan the chunks of the line; the read-back is the 5th: cut before it
			m.Set(joinLines([][]byte{userRow("u0", 0, "first")}))
		}
	}
	_, err := e.Refresh(context.Background(), src(m, "f1", false))
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("err = %v, want ErrFileChanged", err)
	}
	if n := turnsOf(e); n != 1 {
		t.Fatalf("turns = %d: the long line that could not be read back must not be fed", n)
	}
}

func TestWindow_UnsatisfiableBudgetReturnsNothingOverTheCap(t *testing.T) {
	e := filled(t, 3)
	w := e.Window(3, -1, func([]byte) bool { return false })
	if len(w.Turns) != 0 || !w.OverBudget || w.FirstIndex != -1 || !w.HasMoreBefore {
		t.Fatalf("window under an unsatisfiable budget = %+v, want empty, OverBudget", w)
	}
	// a budget only an empty item list satisfies still returns the turn, all its items omitted
	one := e.Window(1, -1, everything).Turns[0]
	bare := one
	bare.Items = nil
	bareJSON, _ := json.Marshal([]convmodel.Turn{bare})
	w = e.Window(1, -1, func(b []byte) bool { return len(b) <= len(bareJSON)+20 })
	if w.OverBudget || len(w.Turns) != 1 || w.Turns[0].OmittedItems != len(one.Items) {
		t.Fatalf("window = %+v", w)
	}
}
