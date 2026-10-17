package workbook

import (
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// Haiku's open list leaves out an item a person set by hand (a hand uncheck must not be offered to Haiku to tick), and an
// entry whose only open items are hand-set is not counted as an ask.
// Mutation gate: drop the manual skip in ReadOpenItems -> the hand-unchecked item is listed (red).
func TestReadOpenItems_SkipsHandSetItems(t *testing.T) {
	dir := t.TempDir()
	at0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	e := journal.Entry{ID: "c1", Kind: journal.KindConfirm, At: at0, Actor: "88 → 使用者", Items: []journal.Item{{Text: "甲"}, {Text: "乙", Manual: true}, {Text: "丙"}}}
	e2 := journal.Entry{ID: "c2", Kind: journal.KindConfirm, At: at0.Add(time.Hour), Actor: "88 → 使用者", Items: []journal.Item{{Text: "丁", Manual: true}}}
	for _, x := range []journal.Entry{e, e2} {
		if err := journal.Upsert(journal.MonthFile(dir, at0), x); err != nil {
			t.Fatal(err)
		}
	}
	got := ReadOpenItems(dir, discardLog)
	want := []OpenItem{
		{N: 1, Asked: 1, Idx: 1, EntryID: "c1", ItemIndex: 0, Text: "甲"},
		{N: 2, Asked: 1, Idx: 3, EntryID: "c1", ItemIndex: 2, Text: "丙"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}
