package convfeed

import "testing"

// The copy shares nothing with the entry: a caller may change it freely.
// Mutation gate: return the entry's own slice → red.
func TestCopyLastTurns_SharesNothingWithTheEntry(t *testing.T) {
	e := filled(t, 4)
	got := e.CopyLastTurns(2)
	if len(got) != 2 || got[0].Index != 2 || got[1].Index != 3 {
		t.Fatalf("turns = %+v", got)
	}
	if len(got[1].Items) == 0 {
		t.Fatal("no items copied")
	}
	want := e.CopyLastTurns(2)
	// scribble over everything the copy holds
	got[1].ID = "changed"
	got[1].Items[0].User.Text = "changed"
	if got[1].Items[1].AgentText != nil {
		got[1].Items[1].AgentText.Markdown = "changed"
	}
	got[1].Items = got[1].Items[:0]
	again := e.CopyLastTurns(2)
	for i := range want {
		if again[i].ID != want[i].ID || len(again[i].Items) != len(want[i].Items) {
			t.Fatalf("the entry moved: %+v vs %+v", again[i], want[i])
		}
		if again[i].Items[0].User == nil || again[i].Items[0].User.Text != want[i].Items[0].User.Text {
			t.Fatalf("an item's text moved: %+v", again[i].Items[0].User)
		}
	}
}

func TestCopyLastTurns_FewerThanAskedAndNone(t *testing.T) {
	if got := filled(t, 2).CopyLastTurns(5); len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got := NewEntry(sidA).CopyLastTurns(3); len(got) != 0 {
		t.Fatalf("empty entry gave %d turns", len(got))
	}
	if got := filled(t, 2).CopyLastTurns(0); len(got) != 0 {
		t.Fatalf("n=0 gave %d turns", len(got))
	}
}
