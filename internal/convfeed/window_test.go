package convfeed

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func filled(t *testing.T, n int) *Entry {
	t.Helper()
	m := newMem(idle(n)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	return e
}

func indexes(w WindowResult) []int {
	var out []int
	for _, t := range w.Turns {
		out = append(out, t.Index)
	}
	return out
}

func TestWindow_LastNAndBefore(t *testing.T) {
	e := filled(t, 10)
	w := e.Window(3, -1, everything)
	if fmt.Sprint(indexes(w)) != "[7 8 9]" || w.FirstIndex != 7 || w.LastIndex != 9 || w.TotalTurns != 10 || !w.HasMoreBefore {
		t.Fatalf("last 3 = %+v", w)
	}
	w = e.Window(3, 7, everything)
	if fmt.Sprint(indexes(w)) != "[4 5 6]" || !w.HasMoreBefore {
		t.Fatalf("3 before 7 = %v more %v", indexes(w), w.HasMoreBefore)
	}
	w = e.Window(5, 2, everything)
	if fmt.Sprint(indexes(w)) != "[0 1]" || w.HasMoreBefore {
		t.Fatalf("5 before 2 = %v more %v", indexes(w), w.HasMoreBefore)
	}
	w = e.Window(20, -1, everything)
	if len(w.Turns) != 10 || w.HasMoreBefore || w.FirstIndex != 0 {
		t.Fatalf("all = %v more %v", indexes(w), w.HasMoreBefore)
	}
	if w := e.Window(3, 0, everything); len(w.Turns) != 0 || w.FirstIndex != -1 || w.TotalTurns != 10 {
		t.Fatalf("before 0 = %+v, want an empty window", w)
	}
	if w := NewEntry(sidA).Window(3, -1, everything); len(w.Turns) != 0 || w.TotalTurns != 0 {
		t.Fatalf("empty entry = %+v", w)
	}
}

func encodedLen(turns []convmodel.Turn) int {
	b, _ := json.Marshal(turns)
	return len(b)
}

func TestWindow_BudgetDropsOlderTurnsThenOldestItems(t *testing.T) {
	// ten turns of about 1 KiB each
	pad := strings.Repeat("p", 900)
	var lines [][]byte
	for i := 0; i < 10; i++ {
		lines = append(lines, userRow(fmt.Sprintf("u%d", i), float64(i*2), pad), assistantText(fmt.Sprintf("a%d", i), float64(i*2+1), "ok"))
	}
	m := newMem(lines...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))

	all := e.Window(10, -1, everything)
	one := encodedLen(all.Turns[9:])
	// room for about four turns: older turns drop, the newest stay
	w := e.Window(10, -1, func(b []byte) bool { return len(b) <= one*4+8 })
	if len(w.Turns) < 3 || len(w.Turns) > 4 || w.Turns[len(w.Turns)-1].Index != 9 || !w.HasMoreBefore || w.FirstIndex != w.Turns[0].Index {
		t.Fatalf("budget window = %v more %v first %d", indexes(w), w.HasMoreBefore, w.FirstIndex)
	}
	for _, tr := range w.Turns {
		if tr.OmittedItems != 0 {
			t.Fatalf("a turn lost items while whole turns could still be dropped: %+v", tr)
		}
	}

	// one turn with many items, room for only a few of them
	var big [][]byte
	big = append(big, userRow("u0", 0, "start"))
	for i := 0; i < 30; i++ {
		big = append(big, toolUseRow(fmt.Sprintf("a%d", i), float64(i+1), fmt.Sprintf("toolu_%d", i), "echo "+pad[:200]))
	}
	m2 := newMem(big...)
	e2 := NewEntry(sidA)
	refresh(t, e2, src(m2, "f1", false))
	whole := e2.Window(1, -1, everything)
	if len(whole.Turns) != 1 || len(whole.Turns[0].Items) != 31 {
		t.Fatalf("setup: %d turns, %d items", len(whole.Turns), len(whole.Turns[0].Items))
	}
	cap := encodedLen(whole.Turns) / 3
	w2 := e2.Window(1, -1, func(b []byte) bool { return len(b) <= cap })
	tr := w2.Turns[0]
	if len(w2.Turns) != 1 || tr.OmittedItems == 0 || tr.OmittedItems+len(tr.Items) != 31 || w2.HasMoreBefore {
		t.Fatalf("items window: omitted %d kept %d turns %d", tr.OmittedItems, len(tr.Items), len(w2.Turns))
	}
	if b, _ := json.Marshal(w2.Turns); len(b) > cap {
		t.Fatalf("the window still exceeds the budget: %d > %d", len(b), cap)
	}
	// the NEWEST items stay: the last item is the last one of the turn
	last := tr.Items[len(tr.Items)-1]
	if last.Step == nil || last.Step.ID != "toolu_29" {
		t.Fatalf("the newest item was dropped: %+v", last)
	}
	// the smallest drop that fits: one fewer omitted would not fit
	tr2 := whole.Turns[0]
	tr2.Items = tr2.Items[tr.OmittedItems-1:]
	tr2.OmittedItems = tr.OmittedItems - 1
	if b, _ := json.Marshal([]convmodel.Turn{tr2}); len(b) <= cap {
		t.Fatalf("a smaller omission also fits: the window dropped too many items")
	}
}

func TestWindow_TurnsThatAreDroppedDoNotSetOmittedItems(t *testing.T) {
	e := filled(t, 6)
	w := e.Window(6, -1, func(b []byte) bool { return len(b) < 900 })
	for _, tr := range w.Turns {
		if tr.OmittedItems != 0 {
			t.Fatalf("OmittedItems set on a turn that kept all its items: %+v", tr)
		}
	}
}
